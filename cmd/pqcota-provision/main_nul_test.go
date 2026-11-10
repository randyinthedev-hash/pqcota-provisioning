// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main_test

// 문자열 속 NUL — 승인 정규형이 모호해지는 계획을 생성기와 승인 명령이 받지 않는다.
//
// v0.10.6까지 정규형은 필드를 NUL로만 끊어, provider_choice="BC\0X"·config_artifact="Y"인 계획 A의
// 승인이 provider_choice="BC"·config_artifact="X\0Y"인 계획 B에서 통과했고, 두 계획은 서로 다른
// 플레이북을 냈다(2026-11-10 재현).

import (
	"crypto/ed25519"
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"google.golang.org/protobuf/encoding/protojson"
)

func planFrom(t *testing.T, body string, edit func(*provisioningv1.FinalizedPlan)) *provisioningv1.FinalizedPlan {
	t.Helper()
	p := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal([]byte(body), p); err != nil {
		t.Fatal(err)
	}
	edit(p)
	return p
}

func writeProto(t *testing.T, p *provisioningv1.FinalizedPlan) string {
	t.Helper()
	b, err := protojson.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return writePlan(t, string(b))
}

// 승인을 확인하지 않는 경로(키 없음 + --allow-unverified-approvals)에서도 NUL 계획은 생성기로 넘어가지 않는다.
func TestNULPlanIsRefusedEvenWithoutApprovalCheck(t *testing.T) {
	bin := buildCLI(t)
	p := planFrom(t, planOK, func(p *provisioningv1.FinalizedPlan) { p.Actions[0].ProviderChoice = "BC\x00X" })
	out, errOut, code := runProvision(t, bin, envWith(), "--level", "l2", unverified, writeProto(t, p))
	if code == 0 || out != "" || !strings.Contains(errOut, "NUL") {
		t.Fatalf("NUL 계획이 승인 확인 없이 생성기로 넘어갔다(종료 %d, 산출물 %d바이트):\n%s", code, len(out), errOut)
	}
}

// 재현한 쌍: A에 한 실제 서명을 B에 옮겨도, A와 B 모두 승인 검증을 통과하지 않고 산출물이 나오지 않는다.
func TestNULCollisionPairDoesNotProvision(t *testing.T) {
	bin := buildCLI(t)
	pub, priv, err := sign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	a := planFrom(t, planOK, func(p *provisioningv1.FinalizedPlan) {
		p.ApprovalSignatures = nil
		p.Actions[0].ProviderChoice, p.Actions[0].ConfigArtifact = "BC\x00X", "Y"
	})
	b := planFrom(t, planOK, func(p *provisioningv1.FinalizedPlan) {
		p.ApprovalSignatures = nil
		p.Actions[0].ProviderChoice, p.Actions[0].ConfigArtifact = "BC", "X\x00Y"
	})
	// SignApproval은 이제 A에 서명하지 않으므로, 옛 판이 만든 서명처럼 정규형에 바로 서명한다.
	sk, _ := base64.StdEncoding.DecodeString(priv)
	sig := "alice:" + sign.Prefix + base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(sk), sign.CanonicalPlan(a)))
	for name, p := range map[string]*provisioningv1.FinalizedPlan{"A": a, "B": b} {
		p.ApprovalSignatures = []string{sig}
		out, errOut, code := runProvision(t, bin, envWith("PQCOTA_APPROVAL_KEYS=alice="+pub), "--level", "l2", writeProto(t, p))
		if code == 0 || out != "" || strings.Contains(errOut, "approvals verified") || !strings.Contains(errOut, "NUL") {
			t.Errorf("%s: NUL 계획이 승인을 통과했거나 산출물을 냈다(종료 %d, 산출물 %d바이트):\n%s", name, code, len(out), errOut)
		}
	}
}

// pqcota-approve는 NUL 계획에 서명하지 않는다(서명 생성 쪽).
func TestApproveRefusesNULPlan(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pqcota-approve")
	if out, err := exec.Command("go", "build", "-o", bin, "../pqcota-approve").CombinedOutput(); err != nil {
		t.Fatalf("build pqcota-approve: %v\n%s", err, out)
	}
	_, priv, err := sign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	p := planFrom(t, planOK, func(p *provisioningv1.FinalizedPlan) {
		p.Status = provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW
		p.ApprovalSignatures, p.FinalizedAt = nil, nil
		p.Actions[0].ConfigArtifact = "Groups = X25519MLKEM768\x00"
	})
	out, errOut, code := runProvision(t, bin, envWith("PQCOTA_APPROVAL_KEY="+priv), "--approver", "alice", writeProto(t, p))
	if code == 0 || out != "" || !strings.Contains(errOut, "NUL") {
		t.Fatalf("pqcota-approve가 NUL 계획에 서명했다(종료 %d, 출력 %d바이트):\n%s", code, len(out), errOut)
	}
}
