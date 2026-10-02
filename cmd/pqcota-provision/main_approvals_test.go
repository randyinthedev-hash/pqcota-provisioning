package main_test

// TP-GATE-21·22 — 승인 서명을 **등록된 키로 확인하는 배선**이 CLI에서 실제로 막고 통과시키나.
//
// 서명을 만들고 확인하는 규칙은 pqcota-common의 sign 패키지가 시험한다(TP-GATE-6~8). 여기서 보는 것은
// pqcota-provision이 그 규칙을 **부르고, 그 결과로 거절하고 통과시키는가**다. 규칙이 옳아도 명령이 결과를
// 버리면 보장이 아니다. 이 시험이 없던 동안 이 배선은 Docker 데모가 손으로 지나갈 뿐이었다.
//
// --allow-unverified-approvals를 주지 않는다. 그 문을 열면 확인하는 경로를 지나지 않는다.

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"google.golang.org/protobuf/encoding/protojson"
)

// envWith — 개발자 셸의 PQCOTA_* 설정이 시험에 새지 않게 지우고, 준 것만 더한다.
func envWith(kv ...string) []string {
	drop := map[string]bool{
		"PQCOTA_APPROVAL_KEYS": true, "PQCOTA_REQUIRE_APPROVAL": true, "PQCOTA_ORG": true,
		"PQCOTA_REQUIRE_ORG": true, "PQCOTA_DSN": true,
	}
	var out []string
	for _, e := range os.Environ() {
		if k, _, _ := strings.Cut(e, "="); !drop[k] {
			out = append(out, e)
		}
	}
	return append(out, kv...)
}

// signedPlan — planOK에서 승인 서명을 비우고, 주어진 승인자들이 차례로 **실제로** 서명한 계획을 낸다.
// tamper가 있으면 서명한 뒤에 그 함수로 계획을 고친다(서명 뒤 수정은 승인을 무효로 만든다).
func signedPlan(t *testing.T, signers []struct{ name, priv string }, tamper func(*provisioningv1.FinalizedPlan), labels ...string) string {
	t.Helper()
	p := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal([]byte(planOK), p); err != nil {
		t.Fatal(err)
	}
	p.ApprovalSignatures = nil
	for _, s := range signers {
		sig, err := sign.SignApproval(s.priv, s.name, p)
		if err != nil {
			t.Fatal(err)
		}
		p.ApprovalSignatures = append(p.ApprovalSignatures, sig)
	}
	p.ApprovalSignatures = append(p.ApprovalSignatures, labels...)
	if tamper != nil {
		tamper(p)
	}
	out, err := protojson.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return writePlan(t, string(out))
}

func runProvision(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatal(err)
	}
	return so.String(), se.String(), code
}

func TestApprovalsAreCheckedWithTheApproversOwnKey(t *testing.T) {
	bin := buildCLI(t)
	pubA, privA, _ := sign.Generate()
	pubB, _, _ := sign.Generate()
	pubC, privC, _ := sign.Generate()
	signer := func(name, priv string) []struct{ name, priv string } {
		return []struct{ name, priv string }{{name, priv}}
	}
	edit := func(p *provisioningv1.FinalizedPlan) { p.Actions[0].TargetAlgorithm = "ML-KEM (FIPS 203) — edited" }

	for _, tc := range []struct {
		name     string
		plan     func() string
		keys     string // PQCOTA_APPROVAL_KEYS
		wantCode int
		wantErr  []string // stderr에 있어야 할 것
		wantOut  bool     // stdout에 플레이북이 나와야 하나
	}{
		{
			name:     "서명을 등록된 승인자의 키로 확인하면 통과하고, 확인된 승인자를 말한다",
			plan:     func() string { return signedPlan(t, signer("reviewer-1", privA), nil) },
			keys:     "reviewer-1=" + pubA,
			wantCode: 0, wantErr: []string{"approvals verified: reviewer-1"}, wantOut: true,
		},
		{
			name:     "다른 사람의 키가 그 이름으로 등록돼 있으면 거절한다",
			plan:     func() string { return signedPlan(t, signer("reviewer-1", privA), nil) },
			keys:     "reviewer-1=" + pubB,
			wantCode: 1, wantErr: []string{"approval signatures did not check out", "reviewer-1"},
		},
		{
			name:     "키가 등록되지 않은 승인자의 서명은 거절한다",
			plan:     func() string { return signedPlan(t, signer("reviewer-1", privA), nil) },
			keys:     "someone-else=" + pubA,
			wantCode: 1, wantErr: []string{"approval signatures did not check out", "no key is registered"},
		},
		{
			name:     "서명한 뒤 계획을 고치면 승인은 무효라 거절한다",
			plan:     func() string { return signedPlan(t, signer("reviewer-1", privA), edit) },
			keys:     "reviewer-1=" + pubA,
			wantCode: 1, wantErr: []string{"approval signatures did not check out", "does not match this plan"},
		},
		{
			name:     "서명이 아니라 이름표뿐이면 키가 있어도 확인된 승인이 없어 거절한다",
			plan:     func() string { return writePlan(t, planOK) }, // planOK의 승인은 이름표 "reviewer:test"
			keys:     "reviewer-1=" + pubA,
			wantCode: 1, wantErr: []string{"is not a signature", "no approval on this plan could be verified"},
		},
		{
			name:     "확인된 서명 옆의 이름표는 경고만 하고 통과한다",
			plan:     func() string { return signedPlan(t, signer("reviewer-1", privA), nil, "someone:agreed") },
			keys:     "reviewer-1=" + pubA,
			wantCode: 0, wantErr: []string{"is not a signature", "approvals verified: reviewer-1"}, wantOut: true,
		},
		{
			name: "유효한 서명 옆에 무효한 서명이 있으면 거절한다(하나라도 틀리면 거절)",
			plan: func() string {
				return signedPlan(t, []struct{ name, priv string }{{"reviewer-1", privA}, {"reviewer-2", privA}}, nil)
			},
			keys:     "reviewer-1=" + pubA + ",reviewer-2=" + pubB, // reviewer-2의 서명은 A의 키로 만들었다
			wantCode: 1, wantErr: []string{"approval signatures did not check out", "reviewer-2"},
		},
		{
			name: "서명이 둘 다 유효하면 둘 다 확인된 승인자로 말한다",
			plan: func() string {
				return signedPlan(t, []struct{ name, priv string }{{"reviewer-1", privA}, {"reviewer-2", privC}}, nil)
			},
			keys:     "reviewer-1=" + pubA + ",reviewer-2=" + pubC,
			wantCode: 0, wantErr: []string{"approvals verified: reviewer-1, reviewer-2"}, wantOut: true,
		},
		{
			name:     "키 목록의 모양이 틀리면 거절한다",
			plan:     func() string { return signedPlan(t, signer("reviewer-1", privA), nil) },
			keys:     "not-a-key-list",
			wantCode: 1, wantErr: []string{"PQCOTA_APPROVAL_KEYS"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runProvision(t, bin, envWith("PQCOTA_APPROVAL_KEYS="+tc.keys), "--level", "l2", tc.plan())
			if code != tc.wantCode {
				t.Fatalf("종료 코드 %d (want %d):\n%s", code, tc.wantCode, stderr)
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr에 %q가 없다:\n%s", w, stderr)
				}
			}
			if tc.wantOut != strings.Contains(stdout, "hosts:") {
				t.Errorf("플레이북 출력 여부가 다르다(want %v):\n%s", tc.wantOut, stdout)
			}
			// 거절이면 산출물이 한 줄도 나오면 안 된다.
			if !tc.wantOut && strings.TrimSpace(stdout) != "" {
				t.Errorf("거절하면서 산출물을 냈다:\n%s", stdout)
			}
		})
	}
}

// TP-GATE-22 — PQCOTA_REQUIRE_APPROVAL=1은 받아 주되 아무것도 바꾸지 않는다. 키 없이 거절하는 것이
// 이제 기본이라, 이 변수를 줘도 안 줘도 같은 일이 일어난다: **종료 코드, stdout, stderr가 모두 같다.**
func TestRequireApprovalVariableChangesNothing(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, planOK)
	for _, args := range [][]string{
		{"--level", "l2", plan},             // 키가 없다: 거절
		{"--level", "l2", unverified, plan}, // 알고 연다: 통과, 확인하지 않았다고 말한다
	} {
		so1, se1, c1 := runProvision(t, bin, envWith(), args...)
		so2, se2, c2 := runProvision(t, bin, envWith("PQCOTA_REQUIRE_APPROVAL=1"), args...)
		if c1 != c2 || so1 != so2 || se1 != se2 {
			t.Errorf("%v: 변수가 결과를 바꿨다 — 종료 코드 %d/%d, stdout 같음=%v, stderr 같음=%v\n--- 없이\n%s\n--- 줘서\n%s",
				args, c1, c2, so1 == so2, se1 == se2, se1, se2)
		}
		if c1 == 0 && !strings.Contains(se1, "**not checked**") {
			t.Errorf("%v: 열어 준 자리에서 '확인하지 않았다'가 사라졌다:\n%s", args, se1)
		}
	}
}
