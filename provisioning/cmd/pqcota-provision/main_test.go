package main_test

// TP-PLAN-GATE — §3.7 실행 게이트가 **CLI에서** 실제로 막나.
//
// 규칙 자체는 pkg/provisioning/plan_test.go가 이미 덮는다. 여기서 보는 것은 **배선**이다.
// 규칙이 옳아도 제품 경로가 부르지 않으면 보장이 아니고, 실제로 한동안 그랬다: CLI가
// Executable을 부르지 않고 상태 비교만 인라인으로 해서, 승인 서명이나 조치가 빈 FINALIZED
// 계획도 플레이북을 받아 갔다.
//
// 그래서 함수가 아니라 **빌드한 CLI를 돌려서** 본다. 반환값을 버리는 식으로 배선이 헐거워져도
// 여기서 드러난다.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildCLI — 테스트 안에서 CLI를 빌드한다. 산출물은 t.TempDir이라 뒤에 남지 않는다.
func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pqcota-provision")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// 승인 검증은 이제 기본으로 닫혀 있다(TP-GATE-17). 아래 케이스들이 재는 것은 **다른 관문**이라,
// 그 문을 명시적으로 열어 두고 본다 — 열어 둔 채로도 그 관문이 제 일을 하는지가 요점이다.
const unverified = "--allow-unverified-approvals"

func writePlan(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	// 정상 — 견본(examples/provisioning/plans/openssl-3.5-config-only.json)과 같은 모양.
	// **빈칸이 없다**: 되짚을 근거(스냅샷·규칙 버전·확정 시각·finding)와 목표 알고리즘까지 채운다.
	// 여기가 모자라면 산출물은 나오지만 종료 상태가 3이 된다(TestIncompletePlanDoesNotExitZero).
	planOK = `{
  "id": "t-ok", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"],
  "derivedFromSnapshotId": "snap-1", "rulesetVersion": "rs-1",
  "finalizedAt": "2026-09-10T00:00:00Z",
  "actions": [{"id":"a1","targetNodeId":"n1","findingId":"f1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY",
    "targetAlgorithm":"ML-KEM (FIPS 203)",
    "automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL"}]
}`
	// 다른 빈칸은 없고 **위임 수준만** 말하지 않는다. 그래야 종료 3이 그 한 가지 때문임이 드러난다.
	planNoLevel = `{
  "id": "t-nolevel", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"],
  "derivedFromSnapshotId": "snap-1", "rulesetVersion": "rs-1",
  "finalizedAt": "2026-09-10T00:00:00Z",
  "actions": [{"id":"a1","targetNodeId":"n1","findingId":"f1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY",
    "targetAlgorithm":"ML-KEM (FIPS 203)"}]
}`
	// 실행 근거는 되지만 빈칸이 남았다 — 목표 알고리즘도 추적 정보도 없다.
	planIncomplete = `{
  "id": "t-inc", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"],
  "actions": [{"id":"a1","targetNodeId":"n1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]
}`
	// 스냅샷 참조의 모양이 틀렸다 — 원천 노드가 없다. 이력이 없어도 틀린 것이라 --dsn 없이 잡혀야 한다.
	// 다른 빈칸은 다 채워 이 하나만 남긴다.
	planBadRef = `{
  "id": "t-badref", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"], "rulesetVersion": "r", "finalizedAt": "2026-09-10T00:00:00Z",
  "actions": [{"id":"a1","targetNodeId":"n1","findingId":"f-1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY",
    "automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL","targetAlgorithm":"ML-KEM (FIPS 203)",
    "evidenceSources":[{"findingId":"f-1","snapshot":{"snapshotId":"ingest-1:n1"}}]}]
}`
	// 승인 서명이 없다 — FINALIZED이지만 실행 근거가 아니다(§3.3③).
	planNoSig = `{
  "id": "t-nosig", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "actions": [{"id":"a1","targetNodeId":"n1","findingId":"f1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]
}`
	// 조치가 없다 — 바꿀 것이 없는데 플레이북을 내면 빈 산출물이 실행 근거처럼 보인다.
	planNoActions = `{
  "id": "t-noact", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"]
}`
	// 확정되지 않았다 — 가장 바깥 게이트.
	planDraft = `{
  "id": "t-draft", "status": "PLAN_STATUS_DRAFT", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"],
  "actions": [{"id":"a1","targetNodeId":"n1","findingId":"f1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]
}`
)

// TestPlanGateRefuses — 실행 근거가 아닌 계획은 **거절되고 플레이북이 한 줄도 나오지 않는다.**
// 종료 코드만 보면 모자란다: 거절하면서 산출물을 함께 내면 그것을 받아 돌리는 사람이 생긴다.
func TestPlanGateRefuses(t *testing.T) {
	bin := buildCLI(t)
	for _, tc := range []struct {
		name, plan, want string
	}{
		{"승인 서명이 없다", planNoSig, "no approval signature"},
		{"조치가 없다", planNoActions, "no actions"},
		{"확정되지 않았다", planDraft, "PLAN_STATUS_DRAFT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, args := range [][]string{
				{"--level", "l2"},
				{"--level", "l2", "--rollback"}, // 롤백 경로도 같은 게이트를 지난다
			} {
				cmd := exec.Command(bin, append(args, writePlan(t, tc.plan))...)
				var stdout, stderr strings.Builder
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()

				if err == nil {
					t.Fatalf("%v: 통과했다 — 게이트가 없다", args)
				}
				if out := strings.TrimSpace(stdout.String()); out != "" {
					t.Errorf("%v: 거절하면서 stdout에 산출물을 냈다:\n%s", args, out)
				}
				if !strings.Contains(stderr.String(), tc.want) {
					t.Errorf("%v: 무엇이 모자란지 말하지 않는다 (%q 없음):\n%s", args, tc.want, stderr.String())
				}
			}
		})
	}
}

// TestPlanGateAllows — 게이트가 정상 계획까지 막으면 그것도 결함이다.
func TestPlanGateAllows(t *testing.T) {
	bin := buildCLI(t)
	cmd := exec.Command(bin, "--level", "l2", unverified, writePlan(t, planOK))
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("정상 계획이 거절됐다: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hosts:") {
		t.Errorf("플레이북이 나오지 않았다:\n%s", stdout.String())
	}
}

// ★ 불완전한 계획을 **성공으로 끝내지 않는다.**
//
// 산출물은 그대로 낸다 — 사람이 빈칸을 손으로 채우는 것이 정당한 경로라 하드 블록하지 않는다.
// 그러나 종료 상태까지 0이면 표준 오류를 모으지 않는 자동화에서 **불완전한 플레이북이 정상
// 산출물로 남는다.** 생성물을 먼저 stdout에 내고 경고를 뒤에 stderr로 내는 순서라 더 그렇다.
// 그래서 기본은 3이고, 알고 넘기려면 --allow-incomplete를 적는다.
func TestIncompletePlanDoesNotExitZero(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, planIncomplete)

	var stdout, stderr strings.Builder
	cmd := exec.Command(bin, "--level", "l2", unverified, plan)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	if err == nil {
		t.Fatalf("불완전한 계획이 성공으로 끝났다:\n%s", stderr.String())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Errorf("종료 코드가 3이 아니다(%v) — 거절(1)과 갈라야 무엇을 고칠지가 다르다는 것이 보인다", err)
	}
	// 막지는 않는다. 산출물이 함께 나와야 손으로 채워 쓸 수 있다.
	if !strings.Contains(stdout.String(), "hosts:") {
		t.Errorf("플레이북이 나오지 않았다 — 이 관문은 막는 것이 아니라 드러내는 것이다:\n%s", stdout.String())
	}

	// --allow-incomplete면 같은 계획이 0으로 끝난다. 경고는 그대로 나온다.
	var out2, err2 strings.Builder
	cmd2 := exec.Command(bin, "--level", "l2", "--allow-incomplete", unverified, plan)
	cmd2.Stdout, cmd2.Stderr = &out2, &err2
	if e := cmd2.Run(); e != nil {
		t.Errorf("--allow-incomplete인데 실패했다: %v\n%s", e, err2.String())
	}
	if !strings.Contains(err2.String(), "⚠") {
		t.Errorf("--allow-incomplete가 경고까지 지웠다 — 넘기는 것이지 감추는 것이 아니다:\n%s", err2.String())
	}
}

// ★ --rollback도 경고를 낸다.
//
// 전에는 역방향 플레이북을 낸 직후 곧바로 반환해 **경고를 하나도 내지 않았다.** 되돌림에
// 목표 알고리즘은 상관이 없지만, 무엇을 되돌리는지 되짚을 근거가 없는 것은 정방향과 같은
// 무게다. 되돌림도 이력에 남아야 하는 조치이기 때문이다.
func TestRollbackAlsoReportsWhatIsMissing(t *testing.T) {
	bin := buildCLI(t)
	var stdout, stderr strings.Builder
	cmd := exec.Command(bin, "--level", "l2", "--rollback", unverified, writePlan(t, planIncomplete))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	if !strings.Contains(stderr.String(), "no derived_from_snapshot_id") {
		t.Errorf("롤백이 추적성 경고를 건너뛰었다:\n%s", stderr.String())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Errorf("롤백도 불완전하면 3으로 끝나야 한다(%v)", err)
	}
	// 목표 알고리즘은 파일을 지우는 일과 상관이 없다 — 롤백 경고에 끼워 넣지 않는다.
	if strings.Contains(stderr.String(), "target_algorithm is unset") {
		t.Errorf("롤백에 정방향 전용 경고가 섞였다:\n%s", stderr.String())
	}
	// 되돌림이 버전 롤백이 아니라는 사실은 산출물 자체에 적힌다.
	if !strings.Contains(stdout.String(), "not a version rollback") {
		t.Errorf("롤백 플레이북에 제한이 적히지 않았다:\n%s", stdout.String())
	}
}

// ★ 확인할 키가 없으면 **기본으로 거절한다.**
//
// 전에는 경고하고 통과시켰고, 닫는 것은 PQCOTA_REQUIRE_APPROVAL=1을 따로 건 배포에서만
// 일어났다. 그러면 승인 무결성은 "닫을 수 있는 수단"에 머물고 기본 경로는 열린 채다. 승인은
// 책임의 소재인데 아무 문자열이나 그 자리를 채울 수 있으면 그 자리는 비어 있는 것과 같다(§3.3③).
//
// 여는 문은 하나만 둔다 — 명령줄에 적어야 열린다. 환경변수로도 열리면 무엇이 검증됐는지가
// 셸 설정에 숨고, 그러면 로그만 보고는 이 산출물이 확인된 승인 위에 섰는지 알 수 없다.
func TestUnverifiableApprovalsAreRefusedByDefault(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, planOK) // 서명이 아니라 이름표를 달고 있다

	var stdout, stderr strings.Builder
	cmd := exec.Command(bin, "--level", "l2", plan)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("확인할 키가 없는데 통과했다:\n%s", stderr.String())
	}
	// 거절이므로 산출물이 한 줄도 나오면 안 된다 — 불완전(3)과 달리 이것은 근거 자체가 없다.
	if stdout.Len() != 0 {
		t.Errorf("거절하면서 플레이북을 함께 냈다:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--allow-unverified-approvals") {
		t.Errorf("어떻게 열지 말해 주지 않으면 사용자는 서명 자체를 지우는 쪽으로 간다:\n%s", stderr.String())
	}

	// 적으면 열린다. 그때는 확인하지 않았다는 사실이 크게 남는다.
	var out2, err2 strings.Builder
	cmd2 := exec.Command(bin, "--level", "l2", unverified, plan)
	cmd2.Stdout, cmd2.Stderr = &out2, &err2
	if e := cmd2.Run(); e != nil {
		t.Fatalf("%s를 적었는데 거절됐다: %v\n%s", unverified, e, err2.String())
	}
	if !strings.Contains(err2.String(), "**not checked**") {
		t.Errorf("열어 준 자리에서 '확인하지 않았다'가 사라졌다 — 넘기는 것이지 확인한 것이 아니다:\n%s", err2.String())
	}
}

// ★ 모르는 `--level`을 알리지 않고 L2로 삼키지 않는다.
//
// `--level L3`처럼 대소문자만 틀려도 전에는 기본값 L2로 돌았다. 그러면 활성화·재시작이 빠진
// 산출물을 받고도 **시킨 대로 됐다고 읽는다.** 수준은 위험도에 따른 위임이라, 말한 것보다 낮게
// 도는 것이 말한 것보다 높게 도는 것만큼이나 잘못이다.
func TestUnknownLevelIsRefused(t *testing.T) {
	bin := buildCLI(t)
	for _, bad := range []string{"L3", "l4", "full"} {
		var stdout, stderr strings.Builder
		cmd := exec.Command(bin, "--level", bad, unverified, writePlan(t, planOK))
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 {
			t.Errorf("--level %q: 사용법 오류(2)로 끝나야 한다, got %v", bad, err)
		}
		if stdout.Len() != 0 {
			t.Errorf("--level %q: 모르는 수준인데 산출물을 냈다:\n%s", bad, stdout.String())
		}
	}
}

// ★ 위임 수준을 계획이 말하지 않으면 빈칸으로 센다.
//
// 말하지 않으면 실행 수준이 `--level`에서 오는데, 그 플래그는 **승인 서명 밖에 있다.** 서명은
// 계획을 덮지 계획을 부르는 명령줄을 덮지 않는다. 그래서 승인자가 서명한 것과 실제 실행 수준이
// 어긋날 수 있고, 계획이 값을 적으면 그 자리가 닫힌다.
func TestUnsetAutomationLevelCountsAsABlank(t *testing.T) {
	bin := buildCLI(t)
	var stdout, stderr strings.Builder
	cmd := exec.Command(bin, "--level", "l2", unverified, writePlan(t, planNoLevel))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	if !strings.Contains(stderr.String(), "automation_level is unset") {
		t.Errorf("위임 수준 빈칸을 알리지 않았다:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "the approval signature does not cover") {
		t.Errorf("왜 문제인지(서명 밖이라는 것)를 말하지 않으면 고칠 이유가 서지 않는다:\n%s", stderr.String())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Errorf("빈칸이 남았으므로 3으로 끝나야 한다(%v)", err)
	}
	// 막지는 않는다 — 수준을 명령줄에서 정하는 것이 정당한 자리가 있다.
	if !strings.Contains(stdout.String(), "hosts:") {
		t.Errorf("산출물이 나오지 않았다:\n%s", stdout.String())
	}
}

// TP-GATE-13 — 참조의 모양은 이력이 없어도 본다. 원천 노드가 없는 참조는 --dsn 없이도 불완전이고
// 종료 3이다. DSN이 있을 때만 알리면 로컬에서 만든 계획의 결함이 배포 직전에야 드러난다.
func TestMalformedSnapshotReferenceIsIncompleteWithoutDSN(t *testing.T) {
	bin := buildCLI(t)
	var stdout, stderr strings.Builder
	cmd := exec.Command(bin, "--level", "l2", unverified, writePlan(t, planBadRef))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("모양이 틀린 참조인데 종료 3이 아니다(%v):\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "source_node_id") {
		t.Errorf("무엇이 틀렸는지 말하지 않는다:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "hosts:") {
		t.Error("플레이북이 나오지 않았다 — 막는 것이 아니라 드러내는 것이다")
	}
}
