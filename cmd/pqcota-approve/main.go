// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

// Command pqcota-approve — 계획에 **승인 서명**을 붙이고, 첫 승인이면 상태를 FINALIZED로 올린다
// (§3.3③ finalize 전제).
//
// 판정과 실행 승인은 다른 단계다. 판정을 끝낸 쪽은 IN_REVIEW로 넘기고, 이 명령이 FINALIZED로
// 올린다. 상태별 동작은 provisioning.PrepareApproval에 있다 — DRAFT는 거부하고, IN_REVIEW는
// 승인이 없어야 하며, FINALIZED는 승인이 있어야 한다. 그러고 나서 계획의 내용 전부(승인 서명
// 자신만 빼고)를 ed25519로 서명해 `approval_signatures`에 덧붙이고, 계획을 다시 낸다. 승인자
// id가 서명 문자열 안에 들어가므로, 검증하는 쪽은 **그 사람의 키로만** 확인한다
// (pqcota-provision의 PQCOTA_APPROVAL_KEYS).
//
// 상태와 확정 시각은 **서명 전에** 정한다. 서명이 그 둘을 덮으므로, 뒤에 바꾸면 방금 만든
// 서명이 깨진다. 두 번째 승인부터는 아무것도 바꾸지 않고 서명만 더한다.
//
// usage: pqcota-approve --approver <id> [--dsn <postgres-dsn>] <plan.json>
//
//	--approver <id>          : 승인자 id. ':'는 쓸 수 없다(서명 문자열의 구분자)
//	--dsn <dsn>              : 소스 근거 저장소(조회 전용). 소스 근거만 있는 조치가 있으면 필수 — 서명 전에 해석한다
//	env PQCOTA_APPROVAL_KEY  : base64 ed25519 **개인키**. pqcota-keygen이 낸 것
//
// 서명한 계획은 stdout으로 나간다. 원본을 덮어쓰지 않는 이유는, 승인 전 계획과 승인된 계획이
// 같은 파일이면 무엇에 서명했는지 되짚을 수 없기 때문이다.
//
// ★ 서명한 뒤에 계획을 고치면 **승인은 무효가 된다**. 그것이 요점이다: 승인자는 계획의 이름이
// 아니라 조치의 내용에 책임을 진다. 조각을 나중에 채우려면(FillPlan) 채운 뒤에 승인받는다.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"github.com/randyinthedev-hash/pqcota-common/pkg/org"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	sestore "github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/store"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func main() {
	approver := flag.String("approver", "", "approver id — it goes inside the signature so verification is bound to this person")
	dsn := flag.String("dsn", "", "Postgres DSN (read-only) for the source evidence store and the history; required when an action is based only on source evidence or a guide was chosen for some evidence")
	guideDir := flag.String("guide-dir", "", "local guide catalog; required when the plan names a guide — every guide is checked by hash before signing")
	review := flag.Bool("review", false, "do not sign: print what would be approved — evidence, guides, the confirmed purpose and the verified guide bodies — and exit 0 if it could be signed")
	flag.Parse()
	if flag.NArg() < 1 || (*approver == "" && !*review) {
		fmt.Fprintln(os.Stderr, "usage: pqcota-approve --approver <id> [--dsn <postgres-dsn>] [--guide-dir <dir>] <plan.json>   (env PQCOTA_APPROVAL_KEY = base64 ed25519 private key)")
		fmt.Fprintln(os.Stderr, "       pqcota-approve --review [--dsn <postgres-dsn>] [--guide-dir <dir>] <plan.json>")
		os.Exit(2)
	}

	key := os.Getenv("PQCOTA_APPROVAL_KEY")
	if key == "" && !*review {
		fmt.Fprintln(os.Stderr, "PQCOTA_APPROVAL_KEY is not set — there is no key to approve with. Generate one with pqcota-keygen.")
		os.Exit(2)
	}

	raw, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading the plan:", err)
		os.Exit(1)
	}
	plan := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal(raw, plan); err != nil {
		fmt.Fprintln(os.Stderr, "parsing the plan:", err)
		os.Exit(1)
	}

	// 승인 대상인지 보고, 첫 승인이면 상태를 올린다. **서명보다 먼저** — 서명이 상태를 덮는다.
	first := plan.GetStatus() == provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW
	// --review는 같은 구조 검사를 사본에 한다 — 들어온 계획의 상태·시각을 바꾸지 않은 채 보여 준다.
	target := plan
	if *review {
		target = proto.Clone(plan).(*provisioningv1.FinalizedPlan)
	}
	if err := provisioning.PrepareApproval(target, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "refusing to approve:", err)
		os.Exit(1)
	}

	// 근거와 가이드 — 서명 **전에** 본다. 승인자는 볼 수 있는 것에만 책임을 진다.
	//   - 소스 근거만 있는 조치의 참조가 하나라도 미해결이면 서명하지 않는다(단위 B).
	//   - 가이드는 하나라도 카탈로그와 맞지 않거나, 가이드가 고른 근거(런타임·소스)를 해석하지 못하면 서명하지 않는다.
	//     조치 종류와 상관없다 — 볼 수 없는 가이드에 승인하지 않는다.
	summary, bodies, problems := reviewPlan(plan, *dsn, *guideDir)
	if *review {
		fmt.Print(summary)
		fmt.Print(bodies)
		if len(problems) > 0 {
			fmt.Fprintln(os.Stderr, "✗ [approve] this plan would not be signed:")
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "  "+p)
			}
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "[approve] review only — nothing was signed. This plan can be signed.")
		return
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "refusing to approve:")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		os.Exit(1)
	}
	// 서명할 때도 무엇에 서명하는지 stderr에 낸다. 본문 전문은 --review에서 본 것이라 경로와 해시만 낸다.
	fmt.Fprint(os.Stderr, summary)

	sig, err := sign.SignApproval(key, *approver, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signing:", err)
		os.Exit(1)
	}
	plan.ApprovalSignatures = append(plan.ApprovalSignatures, sig)

	out, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "writing the plan:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
	what := "added an approval to"
	if first {
		what = "finalized"
	}
	fmt.Fprintf(os.Stderr, "[approve] %s %s plan %q (%d actions, %d approval(s)). The approval covers every field except the signatures themselves — editing the plan after this invalidates it.\n",
		*approver, what, plan.GetId(), len(plan.GetActions()), len(plan.GetApprovalSignatures()))
}

// reviewPlan — 승인 전에 볼 것과, 서명하지 않을 이유. 저장소·이력은 조회 전용으로 연다.
func reviewPlan(plan *provisioningv1.FinalizedPlan, dsn, guideDir string) (summary, bodies string, problems []string) {
	var src provisioning.SourceEvidenceCheck
	if provisioning.PlanHasSourceEvidence(plan) {
		var lookup provisioning.SourceEvidenceLookup
		if dsn != "" {
			st, err := sestore.OpenPgStoreIn(context.Background(), dsn, org.FromEnv())
			if err != nil {
				lookup = provisioning.FailingSourceLookup{Err: fmt.Errorf("could not open the source evidence store: %w", err)}
			} else {
				defer st.Close()
				lookup = st
			}
		}
		var err error
		src, err = provisioning.CheckSourceEvidence(lookup, plan)
		if err != nil {
			problems = append(problems, err.Error())
			if dsn == "" {
				problems = append(problems, "(an action based only on source evidence must resolve before it is approved — give --dsn for the source evidence store)")
			}
		}
		for _, w := range src.Incomplete(plan) {
			fmt.Fprintln(os.Stderr, "⚠ [approve] "+w)
		}
	}
	var guides provisioning.GuideCheck
	if provisioning.PlanHasGuide(plan) {
		var hist history.SnapshotLookup
		if provisioning.PlanGuideChoosesRuntime(plan) && dsn != "" {
			h, err := history.OpenPgStoreIn(context.Background(), dsn, org.FromEnv())
			if err != nil {
				problems = append(problems, "could not open the history to check the runtime evidence a guide was chosen for: "+err.Error())
			} else {
				defer h.Close()
				hist = h
			}
		}
		guides, _ = provisioning.CheckGuides(plan, guideDir, src, hist)
		for ai, a := range plan.GetActions() {
			if g := guides.Of(ai); g != nil && !g.Resolved {
				problems = append(problems, fmt.Sprintf("action[%d] %s: guide %s revision %s does not resolve — %s",
					ai, a.GetId(), g.Selection.GetGuideId(), g.Selection.GetRevision(), g.Reason))
			}
		}
	}
	return provisioning.ReviewSummary(plan, src, guides), provisioning.ReviewBodies(guides), problems
}
