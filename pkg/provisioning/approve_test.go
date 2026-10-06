// SPDX-FileCopyrightText: 2026 randyinthedev
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"errors"
	"testing"
	"time"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 판정을 끝낸 쪽이 넘기는 모양. 상태는 IN_REVIEW, 승인도 확정 시각도 없다.
func judged() *provisioningv1.FinalizedPlan {
	return &provisioningv1.FinalizedPlan{
		Id: "pqcaton:org://acme:01J", Status: provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW, Scope: "org://acme",
		Actions: []*provisioningv1.RemediationAction{{
			Id: "a1", TargetNodeId: "web-01", Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
		}},
	}
}

var t0 = time.Unix(1700000000, 0).UTC()

// TP-GATE-9 — 승인할 대상이 아닌 상태는 거부한다.
//
// 전에는 approve가 상태를 아예 보지 않아 DRAFT 계획에도 서명이 찍혔다. 뒤에서 Executable이
// 막아 악용되지는 않았지만, 승인이라는 행위가 무엇에 대한 것인지 확인하지 않는 자리였다.
func TestDraftAndUnspecifiedCannotBeApproved(t *testing.T) {
	for _, st := range []provisioningv1.PlanStatus{
		provisioningv1.PlanStatus_PLAN_STATUS_DRAFT,
		provisioningv1.PlanStatus_PLAN_STATUS_UNSPECIFIED,
	} {
		p := judged()
		p.Status = st
		err := provisioning.PrepareApproval(p, t0)
		if !errors.Is(err, provisioning.ErrNotApprovable) {
			t.Errorf("%s: 승인 대상이 아닌데 거부하지 않았다: %v", st, err)
		}
		if p.GetStatus() != st {
			t.Errorf("%s: 거부하면서 상태를 바꿨다: %s", st, p.GetStatus())
		}
	}
	if err := provisioning.PrepareApproval(nil, t0); !errors.Is(err, provisioning.ErrNotApprovable) {
		t.Errorf("nil 을 거부하지 않았다: %v", err)
	}
}

// TP-GATE-9 — 승인 전에 물을 수 있는 구조는 승인 전에 묻는다. **두 상태 모두.**
//
// 여기서 걸리는 계획에 서명이 붙으면 **승인은 됐는데 실행할 수 없는 계획**이 생긴다. FINALIZED에서
// 건너뛰면 첫 승인 뒤에 조치가 지워진 계획에도 추가 승인이 붙는다. 거부할 때는 상태·시각·승인
// 목록이 들어온 그대로여야 한다 — 거부하면서 계획을 만지면 그 계획이 무엇인지 말할 수 없다.
func TestStructureIsCheckedBeforeApproval(t *testing.T) {
	_, priv, _ := sign.Generate()
	faults := []struct {
		what string
		mut  func(*provisioningv1.FinalizedPlan)
	}{
		{"조치 0건", func(p *provisioningv1.FinalizedPlan) { p.Actions = nil }},
		{"대상 노드 없음", func(p *provisioningv1.FinalizedPlan) { p.Actions[0].TargetNodeId = "" }},
		{"종류 미정", func(p *provisioningv1.FinalizedPlan) {
			p.Actions[0].Kind = provisioningv1.RemediationKind_REMEDIATION_KIND_UNSPECIFIED
		}},
	}
	// 두 상태의 깨끗한 출발점. FINALIZED는 실제로 한 번 승인을 거친 모양이다.
	starts := map[string]func() *provisioningv1.FinalizedPlan{
		"IN_REVIEW": judged,
		"FINALIZED": func() *provisioningv1.FinalizedPlan {
			p := judged()
			if err := provisioning.PrepareApproval(p, t0); err != nil {
				t.Fatal(err)
			}
			sig, _ := sign.SignApproval(priv, "alice", p)
			p.ApprovalSignatures = []string{sig}
			return p
		},
	}
	for state, start := range starts {
		for _, f := range faults {
			p := start()
			f.mut(p)
			wantStatus, wantAt, wantSigs := p.GetStatus(), p.GetFinalizedAt(), append([]string(nil), p.GetApprovalSignatures()...)

			err := provisioning.PrepareApproval(p, t0.Add(time.Hour))
			if !errors.Is(err, provisioning.ErrNotApprovable) {
				t.Errorf("%s · %s: 승인을 막지 않았다: %v", state, f.what, err)
			}
			if p.GetStatus() != wantStatus {
				t.Errorf("%s · %s: 거부하면서 상태를 바꿨다: %s", state, f.what, p.GetStatus())
			}
			if (p.GetFinalizedAt() == nil) != (wantAt == nil) || (wantAt != nil && !p.GetFinalizedAt().AsTime().Equal(wantAt.AsTime())) {
				t.Errorf("%s · %s: 거부하면서 확정 시각을 바꿨다", state, f.what)
			}
			if got := p.GetApprovalSignatures(); len(got) != len(wantSigs) || (len(got) > 0 && got[0] != wantSigs[0]) {
				t.Errorf("%s · %s: 거부하면서 승인 목록을 바꿨다", state, f.what)
			}
		}
	}
}

// ★ TP-GATE-10 — 첫 승인이 상태를 올리고 시각을 찍은 **뒤에** 서명한다.
//
// CanonicalPlan이 status와 finalized_at을 덮으므로, 순서가 바뀌면 방금 만든 서명이 깨진다.
// 상류 검증(VerifyApprovals)이 그 서명을 실제로 받아들이는지까지 본다.
func TestFirstApprovalFinalizesThenSigns(t *testing.T) {
	pub, priv, err := sign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	p := judged()
	if err := provisioning.PrepareApproval(p, t0); err != nil {
		t.Fatalf("깨끗한 IN_REVIEW 를 거부했다: %v", err)
	}
	if p.GetStatus() != provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED {
		t.Fatalf("첫 승인이 상태를 올리지 않았다: %s", p.GetStatus())
	}
	if !p.GetFinalizedAt().AsTime().Equal(t0) {
		t.Fatalf("확정 시각이 now 가 아니다: %v", p.GetFinalizedAt().AsTime())
	}
	sig, err := sign.SignApproval(priv, "alice", p)
	if err != nil {
		t.Fatal(err)
	}
	p.ApprovalSignatures = append(p.ApprovalSignatures, sig)

	chk := sign.VerifyApprovals(map[string]string{"alice": pub}, p)
	if len(chk.Approved) != 1 {
		t.Fatalf("올린 상태를 서명했는데 검증되지 않는다: %+v", chk)
	}
	if err := provisioning.Executable(p); err != nil {
		t.Errorf("승인된 계획이 실행 관문을 지나지 못한다: %v", err)
	}
}

// ★ TP-GATE-10 — 두 번째 승인은 아무것도 바꾸지 않는다. 바꾸면 첫 서명이 깨진다.
func TestSecondApprovalKeepsStatusAndTime(t *testing.T) {
	pubA, privA, _ := sign.Generate()
	pubB, privB, _ := sign.Generate()
	p := judged()
	if err := provisioning.PrepareApproval(p, t0); err != nil {
		t.Fatal(err)
	}
	sigA, _ := sign.SignApproval(privA, "alice", p)
	p.ApprovalSignatures = append(p.ApprovalSignatures, sigA)

	later := t0.Add(48 * time.Hour)
	if err := provisioning.PrepareApproval(p, later); err != nil {
		t.Fatalf("이미 FINALIZED 인 계획의 추가 승인을 거부했다: %v", err)
	}
	if !p.GetFinalizedAt().AsTime().Equal(t0) {
		t.Fatalf("두 번째 승인이 확정 시각을 다시 찍었다 — 첫 서명이 깨진다: %v", p.GetFinalizedAt().AsTime())
	}
	sigB, _ := sign.SignApproval(privB, "bob", p)
	p.ApprovalSignatures = append(p.ApprovalSignatures, sigB)

	chk := sign.VerifyApprovals(map[string]string{"alice": pubA, "bob": pubB}, p)
	if len(chk.Approved) != 2 {
		t.Fatalf("두 승인이 다 검증돼야 한다: %+v", chk)
	}
}

// ★ TP-GATE-11 — 상태가 말하는 것과 계획에 있는 것이 어긋나면 손상이다.
//
// IN_REVIEW 인데 승인이 있으면 누군가 이 상태에 서명을 달아 둔 것이고, FINALIZED 인데 승인이나
// 시각이 없으면 상태만 바꿔 넣은 것이다. 어느 쪽이든 이 계획이 무엇인지 말할 수 없다.
func TestInconsistentStatusIsCorrupt(t *testing.T) {
	_, priv, _ := sign.Generate()
	for _, tc := range []struct {
		what string
		mut  func(*provisioningv1.FinalizedPlan)
	}{
		{"IN_REVIEW 인데 승인이 있다", func(p *provisioningv1.FinalizedPlan) {
			sig, _ := sign.SignApproval(priv, "x", p)
			p.ApprovalSignatures = []string{sig}
		}},
		{"IN_REVIEW 인데 확정 시각이 있다", func(p *provisioningv1.FinalizedPlan) {
			p.FinalizedAt = timestamppb.New(t0)
		}},
		{"FINALIZED 인데 승인이 없다", func(p *provisioningv1.FinalizedPlan) {
			p.Status = provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED
			p.FinalizedAt = timestamppb.New(t0)
		}},
		{"FINALIZED 인데 확정 시각이 없다", func(p *provisioningv1.FinalizedPlan) {
			p.Status = provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED
			sig, _ := sign.SignApproval(priv, "x", p)
			p.ApprovalSignatures = []string{sig}
		}},
	} {
		p := judged()
		tc.mut(p)
		before := p.GetStatus()
		err := provisioning.PrepareApproval(p, t0)
		if !errors.Is(err, provisioning.ErrCorruptPlan) {
			t.Errorf("%s: 손상으로 거부하지 않았다: %v", tc.what, err)
		}
		if p.GetStatus() != before {
			t.Errorf("%s: 거부하면서 상태를 바꿨다", tc.what)
		}
	}
}
