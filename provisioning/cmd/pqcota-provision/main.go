// Command pqcota-provision — 확정 계획(FinalizedPlan JSON)에서 프로비저닝 산출물을 만든다:
//
//	(1) L1/L2 Ansible 플레이북 생성(규정서 §4.4 — core는 생성만, 실행은 사용자 Ansible)
//	    --rollback 시 역방향(롤백) 플레이북: forward가 배치한 파일 제거(§6A).
//	(2) 조치별 before 상태 캡처 → append-only 레코드로 영속(§6A 롤백 근거)
//
// L3(--level l3)는 계획의 activation 훅(사용자가 적은 비활성화·활성화·재시작 명령)을 의미 순서로
// 플레이북에 배치한다. 활성화 방법을 도구가 추측하지 않는다(§2.5).
//
// usage: pqcota-provision [--level l1|l2|l3] [--rollback] [--dsn <postgres>]
//
//	       [--allow-incomplete] [--allow-unverified-approvals] <plan.json>
//
//		--dsn 지정 시: 히스토리에서 before-findings를 읽어 레코드를 캡처·영속(같은 저장소).
//		미지정 시: 플레이북만 stdout(레코드 없음).
//		env PQCOTA_APPROVAL_KEYS   : `<승인자>=<base64 공개키>` 콤마 구분. 승인 서명을 **그 승인자의
//		                             키로** 검증한다(§3.3③). 하나라도 어긋나면 거절한다.
//		                             **없으면 거절한다** — 확인할 수 없는 승인은 누가 책임졌는지를
//		                             말해 주지 않는다. 알고 열려면 --allow-unverified-approvals.
//		env PQCOTA_REQUIRE_APPROVAL: "1"은 그대로 받는다. 이제 기본과 같은 뜻이라 아무것도 바꾸지 않는다.
//
// 승인 서명은 pqcota-approve가 붙인다. 키쌍은 pqcota-keygen이 낸다.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"github.com/randyinthedev-hash/pqcota-common/pkg/org"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"google.golang.org/protobuf/encoding/protojson"

	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
)

func main() {
	levelFlag := flag.String("level", "l2", "automation level: l1 (stage only) | l2 (through install) | l3 (through activation and restart, using the plan's activation hooks)")
	rollbackFlag := flag.Bool("rollback", false, "generate the reverse (rollback) playbook — removes the files the forward run staged")
	dsn := flag.String("dsn", "", "Postgres DSN for history and records; when given, captures the before state and persists it")
	allowIncomplete := flag.Bool("allow-incomplete", false, "exit 0 even when the plan is incomplete (the playbook is generated either way; without this the exit status is 3)")
	allowUnverified := flag.Bool("allow-unverified-approvals", false, "go on when there is no PQCOTA_APPROVAL_KEYS to check the plan's approvals with (default: refuse)")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: pqcota-provision [--level l1|l2|l3] [--rollback] [--dsn <postgres>] <plan.json>")
		os.Exit(2)
	}

	raw, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read plan:", err)
		os.Exit(1)
	}
	plan := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal(raw, plan); err != nil {
		fmt.Fprintln(os.Stderr, "parse plan:", err)
		os.Exit(1)
	}

	// §3.7 최강 게이트 — 실행 근거인지 판정하는 규칙은 provisioning.Executable 하나뿐이다.
	// 여기서 조건을 다시 적으면 규칙이 두 곳에 생기고, 실제로 그렇게 어긋났다: 상태만 비교하던
	// 동안 승인 서명·조치가 빈 FINALIZED 계획이 그대로 통과했다. 사유를 함께 싣는다 — 무엇이
	// 모자란지 말하지 않고 거절하면 사용자가 계획을 고칠 수 없다.
	if err := provisioning.Executable(plan); err != nil {
		// 꼬리말은 사유에 맞춰 붙인다. 조치 내용이 모자란 계획은 **이미 확정된** 것이라,
		// "확정된 계획만 근거가 된다"고 덧붙이면 고칠 자리를 잘못 가리킨다.
		tail := "Fill the action in — a finalized plan is the only grounds, and it has to say what to do."
		if errors.Is(err, provisioning.ErrNotFinalized) {
			tail = "Only a finalized plan justifies provisioning."
		}
		fmt.Fprintf(os.Stderr, "refused: %v. %s\n", err, tail)
		os.Exit(1)
	}

	// 승인 서명 검증(§3.3③) — Executable은 서명의 **개수**만 센다. 값이 맞는지, 누구 것인지는
	// 여기서 본다. 키 묶음이 없으면 확인할 수 없으므로 **확인했다고 하지 않는다**(§2.6).
	if err := checkApprovals(plan, *allowUnverified); err != nil {
		fmt.Fprintln(os.Stderr, "refused:", err)
		os.Exit(1)
	}

	// 모르는 값을 알리지 않고 L2로 삼키지 않는다. `--level L3`처럼 대소문자만 틀려도 **말한 것보다 낮은
	// 수준으로** 돌아 버리는데, 그러면 활성화·재시작이 빠진 산출물을 받고도 시킨 대로 됐다고 읽는다.
	var level provisioningv1.DeployAutomationLevel
	switch *levelFlag {
	case "l1":
		level = provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY
	case "l2":
		level = provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL
	case "l3":
		level = provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO
	default:
		fmt.Fprintf(os.Stderr, "unknown --level %q — it has to be l1, l2 or l3.\n", *levelFlag)
		os.Exit(2)
	}

	// (1) 플레이북 — stdout. --rollback이면 역방향(배치 파일 제거), 아니면 forward.
	if *rollbackFlag {
		fmt.Print(provisioning.GenerateRollbackPlaybook(plan, level))
	} else {
		fmt.Print(provisioning.GenerateProvisioningPlaybook(plan, level))
	}

	// (2) 계획이 모자라 산출물이나 이력이 불완전해지는 자리를 알린다.
	incomplete := reportWarnings(plan, level, *rollbackFlag)

	if *rollbackFlag {
		finish(incomplete, *allowIncomplete)
		return
	}

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "[provision] no --dsn → skipping the before capture and record persistence (playbook only).")
		finish(incomplete, *allowIncomplete)
		return
	}

	// (2) before 캡처 + 레코드 영속.
	ctx := context.Background()
	hist, err := history.NewPgStoreIn(ctx, *dsn, org.FromEnv())
	if err != nil {
		fmt.Fprintln(os.Stderr, "connecting to history:", err)
		os.Exit(1)
	}
	defer hist.Close()
	recs, err := provisioning.NewPgRecordStore(ctx, *dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "record store:", err)
		os.Exit(1)
	}
	defer recs.Close()

	// 노드별 최신 findings 캐시(before 기준·app_key 부착).
	byNode := map[string][]*discoveryv1.Finding{}
	findingByID := map[string]*discoveryv1.Finding{}
	load := func(node string) []*discoveryv1.Finding {
		if fs, ok := byNode[node]; ok {
			return fs
		}
		snap, err := hist.Latest(node)
		if err != nil {
			fmt.Fprintln(os.Stderr, "reading history:", err)
			os.Exit(1)
		}
		var fs []*discoveryv1.Finding
		if snap != nil {
			fs = snap.Findings
			for _, f := range fs {
				findingByID[f.GetId()] = f
			}
		}
		byNode[node] = fs
		return fs
	}

	n := 0
	resolved, unresolved := 0, 0
	for _, a := range plan.GetActions() {
		node := a.GetTargetNodeId()
		before := load(node)
		var appKeys []string // 근거 Finding의 자산이 어느 앱 것인지(§1.5) — 공유 .so면 다중
		if f := findingByID[a.GetFindingId()]; f != nil {
			appKeys = f.GetAppKeys()
		}
		rec := provisioning.NewProvisioningRecord(
			plan.GetId()+":"+a.GetId(), node, appKeys, plan.GetId(), a, before)
		// 스냅샷 참조를 이력에서 **실제로 찾아** 레코드에 남긴다. 제출된 참조와 찾은 id를 한 항목으로
		// 짝짓는다. 못 찾은 것은 불완전이다 — 원장 연결이 닫히지 않은 것이라 「연결 완료」와 다르다.
		rec.SnapshotResolutions = provisioning.ResolveAction(hist, plan, a)
		for _, r := range provisioning.Unresolved(rec.SnapshotResolutions) {
			if provisioning.IsInvalidReference(r) {
				continue // 모양이 틀린 것은 reportWarnings가 이미 알리고 셌다
			}
			fmt.Fprintf(os.Stderr, "⚠ [provision] action %s (node=%s): snapshot reference not resolved — %s\n", a.GetId(), node, r.GetReason())
			unresolved++
		}
		for _, r := range rec.SnapshotResolutions {
			if r.GetResolvedSnapshotId() != "" {
				resolved++
			}
		}
		if err := recs.Append(rec); err != nil {
			fmt.Fprintln(os.Stderr, "appending a record:", err)
			os.Exit(1)
		}
		n++
	}
	fmt.Fprintf(os.Stderr, "[provision] persisted %d records (before capture · STAGED · rollback basis) · snapshot references resolved %d, unresolved %d.\n", n, resolved, unresolved)
	// 찾지 못한 참조는 불완전이다. 모양이 틀린 것은 reportWarnings가 이미 셌으므로 여기서는 세지 않았다.
	incomplete += unresolved
	finish(incomplete, *allowIncomplete)
}

// reportWarnings — 경고를 stderr로 내고, 그중 **불완전**으로 세는 건수를 돌려준다.
//
// 가르는 기준은 「이 산출물이 계획대로의 일을 하는가」다. 조각이 참조되지 않거나 아무것도
// 켜지 않거나 이력에 근거가 남지 않으면 불완전이다. 반면 provider 자리 대체 고지는 **의도한
// 일이 그대로 일어난다는 안내**라 세지 않는다.
//
// 롤백은 보는 것이 다르다. 파일을 지우는 일에 목표 알고리즘이나 provider 클래스는 상관이
// 없지만, deactivate·restart가 없으면 활성화를 되돌리지 못하고, 근거가 없으면 무엇을
// 되돌리는지 되짚을 수 없다. 전에는 롤백 경로가 생성 직후 반환해 **경고를 하나도 내지 않았다.**
func reportWarnings(plan *provisioningv1.FinalizedPlan, level provisioningv1.DeployAutomationLevel, rollback bool) int {
	say := func(ws []string) int {
		for _, w := range ws {
			fmt.Fprintln(os.Stderr, "⚠ [provision] "+w)
		}
		return len(ws)
	}

	n := 0
	if !rollback {
		// 산출물이 그대로는 불완전한 조치(JCA provider_class 미확정 → java.security placeholder)를
		// 알리지 않고 통과시키지 않는다 — 조각 안 ⚠는 열어봐야 보이므로 여기서 stderr로 크게 알린다(§2.5).
		n += say(provisioning.ProviderClassWarnings(plan))
		// provider 주입은 java.security의 한 자리를 대체한다 — 무엇이 밀려나는지 알린다.
		// 이것은 안내라 불완전으로 세지 않는다.
		say(provisioning.ProviderSlotWarnings(plan))
		// 같은 런타임에 조각이 여러 개면 경로를 나눴다는 사실을 알린다 — 나눈 채 두면 참조되지 않는다.
		n += say(provisioning.ConfigConflictWarnings(plan))
		// 목표 알고리즘이 그룹으로 안 풀리면 조각의 Groups 줄이 주석으로 나간다 — 배치해도 아무것도
		// 켜지지 않는데, 그 사실이 조각 안에만 적혀 있어 열어보지 않으면 모른다.
		n += say(provisioning.TargetAlgorithmWarnings(plan))
	}
	// L3인데 훅이 비면 무엇이 **일어나지 않는지** 알린다 — 활성화 방법을 추측하지 않기 때문(§2.5).
	n += say(provisioning.ActivationWarnings(plan, level))
	// 위임 수준을 계획이 말하지 않으면 실행 수준이 서명 밖의 --level에서 온다. 정방향·롤백 모두
	// 그 값으로 갈리므로 양쪽에서 센다.
	n += say(provisioning.AutomationLevelWarnings(plan))
	// 무엇에서 뽑은 계획인지 되짚을 수 있는가(§1.2). 실행은 되지만 이력에 근거가 안 남는다.
	n += say(provisioning.TraceabilityWarnings(plan))
	// 스냅샷 참조의 **모양**은 이력이 없어도 본다. 틀린 참조는 이력이 있든 없든 틀린 것이고, DSN이
	// 있을 때만 알리면 로컬에서 만든 계획의 결함이 배포 직전에야 드러난다. 찾는 것은 아래 DSN 단계다.
	for _, a := range plan.GetActions() {
		for _, r := range provisioning.Unresolved(provisioning.ResolveAction(nil, plan, a)) {
			n += say([]string{fmt.Sprintf("action %s (node=%s): %s", a.GetId(), a.GetTargetNodeId(), r.GetReason())})
		}
	}
	return n
}

// finish — 불완전한 계획을 **성공으로 끝내지 않는다.**
//
// 산출물은 그대로 낸다. 사람이 손으로 채우는 것이 정당한 경로라 하드 블록하지 않기 때문이다.
// 그러나 종료 상태까지 0이면 표준 오류를 모으지 않는 자동화에서 **불완전한 플레이북이 정상
// 산출물로 남는다.** 그래서 기본은 3으로 끝내고, 알고 넘기려면 --allow-incomplete를 적는다.
// 거절(1)과 가르는 이유는 무엇을 고쳐야 하는지가 다르기 때문이다 — 1은 계획이 실행 근거가
// 아니라는 뜻이고, 3은 근거는 되지만 빈칸이 남았다는 뜻이다.
func finish(incomplete int, allow bool) {
	if incomplete == 0 {
		return
	}
	if allow {
		fmt.Fprintf(os.Stderr, "[provision] %d incomplete spot(s) — passed over because --allow-incomplete was given.\n", incomplete)
		return
	}
	fmt.Fprintf(os.Stderr, "✗ [provision] the playbook was generated, but %d spot(s) above are incomplete. "+
		"Fill them in, or re-run with --allow-incomplete to accept it knowingly (exit 3).\n", incomplete)
	os.Exit(3)
}

// checkApprovals — 승인 서명이 등록된 승인자의 것인지 확인한다.
//
// PQCOTA_APPROVAL_KEYS가 `<승인자>=<base64 공개키>` 묶음이다. **키만 나열하지 않는 이유**가
// PQCOTA_VERIFY_KEY의 교훈이다: 키 목록은 "누군가 서명했다"까지만 답해서 어느 서명이 누구
// 것인지 말하지 못한다. 승인은 책임의 소재라 그 답으로는 부족하다.
//
// **확인할 키가 없으면 기본으로 거절한다.** 전에는 경고하고 통과시켰고, 닫는 것은
// PQCOTA_REQUIRE_APPROVAL=1을 따로 건 배포에서만 일어났다. 그러면 승인 무결성은 "닫을 수 있는
// 수단"에 머물고 기본 경로는 열린 채다 — 승인은 책임의 소재인데, 아무 문자열이나 그 자리를
// 채울 수 있으면 그 자리는 비어 있는 것과 같다(§3.3③).
//
// 알고 여는 길은 남긴다: --allow-unverified-approvals. 키 배포 전에 산출물만 보려는 자리가
// 실제로 있고, 막아 버리면 그 사람은 서명 자체를 지우는 쪽으로 간다. 다만 **적어야 열린다.**
// PQCOTA_REQUIRE_APPROVAL=1은 그대로 받는다(이제 기본과 같은 뜻이다) — 지우지 않고 더한다.
func checkApprovals(plan *provisioningv1.FinalizedPlan, allowUnverified bool) error {
	keys, err := sign.ParseKeyMap(os.Getenv("PQCOTA_APPROVAL_KEYS"))
	if err != nil {
		return fmt.Errorf("PQCOTA_APPROVAL_KEYS: %w", err)
	}
	if len(keys) == 0 {
		if !allowUnverified {
			return fmt.Errorf("no PQCOTA_APPROVAL_KEYS, so the %d approval entr(ies) on this plan cannot be checked — "+
				"an approval nobody can verify shows nothing about who took responsibility (§3.3③). "+
				"Register the approver's key, or pass --allow-unverified-approvals to go on knowing that",
				len(plan.GetApprovalSignatures()))
		}
		fmt.Fprintf(os.Stderr, "⚠ [provision] approval signatures: **not checked** — no PQCOTA_APPROVAL_KEYS to check them with. %d entries were counted, not verified.\n",
			len(plan.GetApprovalSignatures()))
		fmt.Fprintln(os.Stderr, "           you passed --allow-unverified-approvals, so this is a choice, not an oversight.")
		return nil
	}

	chk := sign.VerifyApprovals(keys, plan)
	for _, u := range chk.Unverifiable {
		// 이름표는 틀린 것이 아니라 **아무것도 증명하지 않는 것**이다. 거부와 다른 칸에 둔다.
		fmt.Fprintf(os.Stderr, "⚠ [provision] approval %q is not a signature — it is a label and proves nothing. Sign it with pqcota-approve.\n", u)
	}
	if len(chk.Rejected) > 0 {
		var names []string
		for _, r := range chk.Rejected {
			names = append(names, r.String())
		}
		return fmt.Errorf("approval signatures did not check out: %s. A plan carrying an approval that is not the approver's is worse than one carrying none (§3.3③)",
			strings.Join(names, ", "))
	}
	if len(chk.Approved) == 0 {
		return fmt.Errorf("no approval on this plan could be verified with the registered keys, so nothing shows who approved it (§3.3③)")
	}
	fmt.Fprintf(os.Stderr, "[provision] approvals verified: %s\n", strings.Join(chk.Approved, ", "))
	return nil
}
