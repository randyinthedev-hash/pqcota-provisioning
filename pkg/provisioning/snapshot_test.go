// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"strings"
	"testing"

	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// 참조 해석 — 레코드 → 계획 → 스냅샷 사슬의 마지막 고리. 모양 검사는 이력이 없어도 하고, 찾는
// 것은 이력이 있을 때만 하며, 찾은 스냅샷에 그 finding이 실제로 있어야 한다.

const digest64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func idRef(node, id string) *provisioningv1.SnapshotReference {
	return &provisioningv1.SnapshotReference{SourceNodeId: node, Reference: &provisioningv1.SnapshotReference_SnapshotId{SnapshotId: id}}
}
func contentRef(node, ruleset, digest string) *provisioningv1.SnapshotReference {
	return &provisioningv1.SnapshotReference{SourceNodeId: node, Reference: &provisioningv1.SnapshotReference_Content{
		Content: &provisioningv1.SnapshotContentReference{FormatVersion: history.SnapshotContentFormatV1, Digest: digest, RulesetVersion: ruleset}}}
}

// 이력에 스냅샷 하나를 둔 저장소. 노드 web-01.corp, finding f-1.
func stored(t *testing.T) (*history.MemStore, *history.Snapshot) {
	t.Helper()
	m := history.NewMemStore()
	s := &history.Snapshot{ID: "ingest-1:web-01.corp", NodeID: "web-01.corp", RulesetVersion: "pqcota-enrich/v2",
		Findings: []*discoveryv1.Finding{{Id: "f-1", Algorithm: "X25519"}}}
	if err := m.Append(s); err != nil {
		t.Fatal(err)
	}
	return m, s
}

// TP-GATE-13 — 모양이 틀린 참조는 이력이 없어도 잘못된 참조다.
func TestReferenceShapeIsCheckedWithoutHistory(t *testing.T) {
	for name, r := range map[string]*provisioningv1.SnapshotReference{
		"원천 노드 없음":       idRef("", "s"),
		"빈 snapshot_id":  idRef("n", ""),
		"참조 종류 없음":       {SourceNodeId: "n"},
		"내용 참조의 셋 중 빈 것": {SourceNodeId: "n", Reference: &provisioningv1.SnapshotReference_Content{Content: &provisioningv1.SnapshotContentReference{FormatVersion: history.SnapshotContentFormatV1, Digest: digest64}}},
		"모르는 규격 판":       {SourceNodeId: "n", Reference: &provisioningv1.SnapshotReference_Content{Content: &provisioningv1.SnapshotContentReference{FormatVersion: "pqcota-snapshot-content/v9", Digest: digest64, RulesetVersion: "r"}}},
		"16진수 64자가 아님":   contentRef("n", "r", "abc"),
		"대문자 16진수":       contentRef("n", "r", strings.ToUpper(digest64)),
	} {
		res := provisioning.ResolveReference(nil, r, "")
		if !provisioning.IsInvalidReference(res) {
			t.Errorf("%s: 잘못된 참조로 보지 않았다: %q", name, res.GetReason())
		}
		if len(provisioning.Unresolved([]*provisioningv1.SnapshotResolution{res})) != 1 {
			t.Errorf("%s: 불완전으로 세지 않았다", name)
		}
	}
	// 멀쩡한 참조는 이력이 없으면 「찾지 않았다」이고 불완전이 아니다.
	res := provisioning.ResolveReference(nil, contentRef("n", "r", digest64), "")
	if provisioning.IsInvalidReference(res) || len(provisioning.Unresolved([]*provisioningv1.SnapshotResolution{res})) != 0 {
		t.Errorf("이력이 없어 찾지 않은 것을 불완전으로 셌다: %q", res.GetReason())
	}
}

// ★ TP-GATE-14 — 실제 id와 내용 지문 양쪽으로 찾고, 찾은 스냅샷에 finding이 있어야 한다.
func TestResolveFindsAndChecksFindingMembership(t *testing.T) {
	m, s := stored(t)
	digest := history.ContentHashV1(s)

	for name, r := range map[string]*provisioningv1.SnapshotReference{
		"실제 id": idRef("web-01.corp", s.ID),
		"내용 지문": contentRef("web-01.corp", "pqcota-enrich/v2", digest),
	} {
		ok := provisioning.ResolveReference(m, r, "f-1")
		if ok.GetResolvedSnapshotId() != s.ID {
			t.Errorf("%s: 찾지 못했다: %q", name, ok.GetReason())
		}
		// 스냅샷은 맞는데 finding이 그 안에 없다 — 잘못 짝지어진 근거.
		bad := provisioning.ResolveReference(m, r, "f-9")
		if bad.GetResolvedSnapshotId() != "" || !strings.Contains(bad.GetReason(), "mispaired") {
			t.Errorf("%s: finding 이 없는데 통과했다: %q", name, bad.GetReason())
		}
		if len(provisioning.Unresolved([]*provisioningv1.SnapshotResolution{bad})) != 1 {
			t.Errorf("%s: 잘못 짝지어진 근거를 불완전으로 세지 않았다", name)
		}
	}
	// 실제 id 인데 노드가 다르다 — 찾은 것이 아니다.
	other := provisioning.ResolveReference(m, idRef("db-01", s.ID), "f-1")
	if other.GetResolvedSnapshotId() != "" || !strings.Contains(other.GetReason(), "belongs to node") {
		t.Errorf("다른 노드의 스냅샷을 찾았다고 했다: %q", other.GetReason())
	}
	// 규칙 판이 다르면 못 찾고, 사유에 규칙 판이 적힌다.
	rs := provisioning.ResolveReference(m, contentRef("web-01.corp", "pqcota-enrich/v1", digest), "f-1")
	if rs.GetResolvedSnapshotId() != "" || !strings.Contains(rs.GetReason(), "ruleset") {
		t.Errorf("규칙 판이 다른데 사유가 그것을 말하지 않는다: %q", rs.GetReason())
	}
}

// TP-GATE-14 — 조치 하나를 돈다. 근거가 있으면 각각, 없으면 계획 단위 legacy 분기.
func TestResolveActionWalksEvidenceThenLegacy(t *testing.T) {
	m, s := stored(t)
	plan := &provisioningv1.FinalizedPlan{DerivedFromSnapshotId: s.ID}

	// 근거 둘 — 하나는 찾아지고 하나는 못 찾는다. 결과가 순서대로 짝지어진다.
	a := &provisioningv1.RemediationAction{Id: "a1", FindingId: "f-1", EvidenceSources: []*provisioningv1.ActionEvidenceSource{
		{FindingId: "f-1", Snapshot: idRef("web-01.corp", s.ID)},
		{FindingId: "f-2", Snapshot: idRef("db-01", "ingest-1:db-01")},
	}}
	rs := provisioning.ResolveAction(m, plan, a)
	if len(rs) != 2 || rs[0].GetResolvedSnapshotId() != s.ID || rs[1].GetResolvedSnapshotId() != "" {
		t.Fatalf("근거마다 짝지어지지 않았다: %+v", rs)
	}
	if rs[1].GetSubmitted().GetSourceNodeId() != "db-01" {
		t.Error("못 찾은 항목이 제출된 참조를 잃었다")
	}

	// 호환용 finding_id가 주 근거와 다르다 — 옛 소비자와 새 소비자가 다른 근거를 읽는다.
	two := &provisioningv1.RemediationAction{Id: "a2", FindingId: "f-9", EvidenceSources: a.EvidenceSources[:1]}
	if r := provisioning.ResolveAction(m, plan, two)[0]; r.GetResolvedSnapshotId() != "" || !strings.Contains(r.GetReason(), "does not equal the primary evidence") {
		t.Errorf("호환 finding_id 와 주 근거의 불일치를 잡지 않았다: %q", r.GetReason())
	}

	// ★ 호환용 finding_id가 **비어 있어도** 예외가 아니다. 계약이 「같아야 하고 검사한다」고 적었다.
	// 빈값을 봐주면 옛 소비자는 근거 없이, 새 소비자는 f-1로 읽는다.
	blank := &provisioningv1.RemediationAction{Id: "a5", EvidenceSources: a.EvidenceSources[:1]}
	r0 := provisioning.ResolveAction(m, plan, blank)[0]
	if r0.GetResolvedSnapshotId() != "" || !provisioning.IsInvalidReference(r0) {
		t.Errorf("빈 호환 finding_id 를 예외로 두었다: %q", r0.GetReason())
	}

	// ★ 근거 자체에 finding_id가 없으면, 참조가 맞아도 아무것도 가리키지 못한다.
	noFinding := &provisioningv1.RemediationAction{Id: "a6", EvidenceSources: []*provisioningv1.ActionEvidenceSource{
		{Snapshot: idRef("web-01.corp", s.ID)},
	}}
	r1 := provisioning.ResolveAction(m, plan, noFinding)[0]
	if r1.GetResolvedSnapshotId() != "" || !strings.Contains(r1.GetReason(), "names no finding_id") {
		t.Errorf("finding 없는 근거가 해결된 모양이 됐다: %q", r1.GetReason())
	}
	if len(provisioning.Unresolved([]*provisioningv1.SnapshotResolution{r1})) != 1 {
		t.Error("finding 없는 근거를 불완전으로 세지 않았다")
	}

	// 근거가 없으면 계획 단위 id를 legacy 분기로 — 참조로 합성하지 않는다.
	old := &provisioningv1.RemediationAction{Id: "a3", FindingId: "f-1"}
	lr := provisioning.ResolveAction(m, plan, old)
	if len(lr) != 1 || lr[0].GetLegacyPlanSnapshotId() != s.ID || lr[0].GetResolvedSnapshotId() != s.ID {
		t.Fatalf("legacy 분기가 아니다: %+v", lr)
	}
	if lr[0].GetSubmitted() != nil {
		t.Error("legacy id 를 SnapshotReference 로 합성했다")
	}
	// legacy 에서도 finding 소속을 본다.
	oldBad := &provisioningv1.RemediationAction{Id: "a4", FindingId: "f-9"}
	if r := provisioning.ResolveAction(m, plan, oldBad)[0]; r.GetResolvedSnapshotId() != "" {
		t.Error("legacy 분기에서 finding 소속을 보지 않았다")
	}
	// 근거도 계획 단위 id도 없으면 빈 목록 — 추적성 경고의 몫이다.
	if got := provisioning.ResolveAction(m, &provisioningv1.FinalizedPlan{}, old); len(got) != 0 {
		t.Errorf("아무 참조도 없는데 결과가 있다: %+v", got)
	}
}

// TP-GATE-14 — 추적성 경고는 조치의 근거를 우선한다. 근거가 있으면 계획 단위 id가 비어도 경고하지 않는다.
func TestTraceabilityPrefersActionEvidence(t *testing.T) {
	with := &provisioningv1.FinalizedPlan{Id: "p", RulesetVersion: "r", Actions: []*provisioningv1.RemediationAction{
		{Id: "a1", TargetNodeId: "n", FindingId: "f-1", EvidenceSources: []*provisioningv1.ActionEvidenceSource{{FindingId: "f-1", Snapshot: idRef("n", "s")}}},
	}}
	for _, w := range provisioning.TraceabilityWarnings(with) {
		if strings.Contains(w, "snapshot") {
			t.Errorf("근거가 있는데 스냅샷 경고를 냈다: %s", w)
		}
	}
	without := &provisioningv1.FinalizedPlan{Id: "p", RulesetVersion: "r", Actions: []*provisioningv1.RemediationAction{{Id: "a1", TargetNodeId: "n", FindingId: "f-1"}}}
	found := false
	for _, w := range provisioning.TraceabilityWarnings(without) {
		found = found || strings.Contains(w, "evidence_sources")
	}
	if !found {
		t.Error("근거도 계획 단위 id 도 없는데 경고하지 않았다")
	}
}
