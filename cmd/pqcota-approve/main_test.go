// SPDX-FileCopyrightText: 2026 randyinthedev
// SPDX-License-Identifier: Apache-2.0

package main

// 빌드한 pqcota-approve를 실제로 부른다. 규칙(PrepareApproval)이 옳은지는 pkg/provisioning의
// 테스트가 보고, 여기서는 **제품 경로가 그 규칙을 부르고 그 순서를 지키는지** 본다 —
// 상태를 올린 뒤에 서명해야 서명이 올린 상태를 덮는다.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"google.golang.org/protobuf/encoding/protojson"
)

func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pqcota-approve")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func writePlan(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const judged = `{"id":"pqcaton:org://acme:01J","status":"PLAN_STATUS_IN_REVIEW","scope":"org://acme",
 "actions":[{"id":"a1","targetNodeId":"web-01","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]}`

const draft = `{"id":"p","status":"PLAN_STATUS_DRAFT","scope":"s",
 "actions":[{"id":"a1","targetNodeId":"web-01","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]}`

// FINALIZED라고 적혀 있는데 승인이 없다 — 상태만 바꿔 넣은 계획이다.
const forged = `{"id":"p","status":"PLAN_STATUS_FINALIZED","scope":"s","finalizedAt":"2026-09-10T00:00:00Z",
 "actions":[{"id":"a1","targetNodeId":"web-01","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]}`

func run(t *testing.T, bin, priv, plan string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(bin, "--approver", "reviewer-1", plan)
	cmd.Env = append(os.Environ(), "PQCOTA_APPROVAL_KEY="+priv)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

// TP-GATE-12 — 판정을 끝낸 계획을 주면 FINALIZED로 올리고 시각을 찍고 서명한다.
// 그 서명이 올린 상태 위에서 검증되어야 한다.
func TestApproveFinalizesJudgedPlan(t *testing.T) {
	pub, priv, err := sign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bin := buildCLI(t)
	out, errOut, err := run(t, bin, priv, writePlan(t, judged))
	if err != nil {
		t.Fatalf("깨끗한 IN_REVIEW 를 거부했다: %v\n%s", err, errOut)
	}
	p := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal([]byte(out), p); err != nil {
		t.Fatalf("stdout 이 계획이 아니다: %v\n%s", err, out)
	}
	if p.GetStatus() != provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED || p.GetFinalizedAt() == nil {
		t.Fatalf("상태를 올리지 않았거나 시각을 찍지 않았다: status=%s at=%v", p.GetStatus(), p.GetFinalizedAt())
	}
	chk := sign.VerifyApprovals(map[string]string{"reviewer-1": pub}, p)
	if len(chk.Approved) != 1 {
		t.Fatalf("서명이 올린 상태 위에서 검증되지 않는다 — 순서가 틀렸다: %+v", chk)
	}
	if !strings.Contains(errOut, "finalized") {
		t.Errorf("첫 승인이 무엇을 했는지 말하지 않는다: %s", errOut)
	}
}

// TP-GATE-12 — 승인할 대상이 아니거나 손상된 계획은 거절하고 stdout에 아무것도 내지 않는다.
// 거절하면서 계획을 함께 내면 그것을 받아 다음 단계에 넣는 사람이 생긴다.
func TestApproveRefuses(t *testing.T) {
	_, priv, _ := sign.Generate()
	bin := buildCLI(t)
	for _, tc := range []struct{ name, plan, want string }{
		{"DRAFT", draft, "PLAN_STATUS_DRAFT"},
		{"FINALIZED 인데 승인이 없다", forged, "no approval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := run(t, bin, priv, writePlan(t, tc.plan))
			if err == nil {
				t.Fatalf("서명했다 — 상태를 보지 않는다")
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("거절하면서 stdout 에 계획을 냈다:\n%s", out)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("왜 거절했는지 말하지 않는다 (%q 없음):\n%s", tc.want, errOut)
			}
		})
	}
}
