// SPDX-FileCopyrightText: 2026 randyinthedev
// SPDX-License-Identifier: Apache-2.0

package provisioning

import (
	"fmt"
	"strings"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
)

// 스냅샷 참조를 해석한다 — 레코드 → 계획 → 스냅샷으로 되짚는 사슬의 마지막 고리.
//
// 이력 계층(history)은 참조의 **형식**을 모른다. 그것은 이 계약의 일이라 여기서 푼다. 이력에는 좁은
// 조회(history.SnapshotLookup)만 있다.
//
// 참조가 가리키는 것은 「관측 사건」이 아니라 「중복 억제된 스냅샷 상태」다.

// knownFormats — 아는 지문 규격. 모르는 값은 잘못된 참조다 — 다른 규칙으로 계산한 값을 이 규칙으로
// 찾으면 우연히 맞거나 못 찾을 뿐 무엇도 말하지 못한다.
var knownFormats = map[string]bool{history.SnapshotContentFormatV1: true}

// reasonNotLookedUp — 찾지 않았다. --dsn이 없다. 못 찾은 것이 아니므로 불완전이 아니다.
const reasonNotLookedUp = "not looked up (no history to look in)"

// ValidateReference — 참조의 **모양**. 이력이 있든 없든 틀린 것은 틀린 것이라 --dsn 없이도 본다.
// 여기서 걸린 참조를 DSN이 있을 때만 알리면 로컬에서 만든 계획의 결함이 배포 직전에야 드러난다.
func ValidateReference(r *provisioningv1.SnapshotReference) error {
	if r == nil {
		return fmt.Errorf("no snapshot reference")
	}
	if r.GetSourceNodeId() == "" {
		return fmt.Errorf("snapshot reference has no source_node_id — the history stores snapshots under the envelope's node, and without it nothing can be looked up")
	}
	switch ref := r.GetReference().(type) {
	case *provisioningv1.SnapshotReference_SnapshotId:
		if ref.SnapshotId == "" {
			return fmt.Errorf("snapshot reference names an empty snapshot_id")
		}
	case *provisioningv1.SnapshotReference_Content:
		c := ref.Content
		if c.GetFormatVersion() == "" || c.GetDigest() == "" || c.GetRulesetVersion() == "" {
			return fmt.Errorf("content reference needs format_version, digest and ruleset_version — all three")
		}
		if !knownFormats[c.GetFormatVersion()] {
			return fmt.Errorf("unknown snapshot content format %q — this build knows %s", c.GetFormatVersion(), history.SnapshotContentFormatV1)
		}
		if !isHex64(c.GetDigest()) {
			return fmt.Errorf("content digest must be 64 lowercase hex characters, got %q", c.GetDigest())
		}
	default:
		return fmt.Errorf("snapshot reference names neither a snapshot_id nor a content digest")
	}
	return nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// ResolveReference — 참조 하나를 해석한다. lookup이 nil이면(--dsn 없음) 모양 검사까지만 한다.
//
// 실제 id 면 찾은 스냅샷의 노드가 참조의 source_node_id와 같아야 한다 — 다른 노드의 스냅샷을 가리키면
// 찾은 것이 아니다. **조치의 target_node_id와 같아야 한다는 조건은 두지 않는다**: 원천 노드와 선언
// 노드는 다를 수 있고, 그 대응은 계획을 만든 쪽이 정한 것이라 여기서 검증하지 못한다.
//
// findingID가 비어 있지 않으면 **찾은 스냅샷에 그 finding이 있어야 한다.** 스냅샷 id·노드·지문만
// 맞고 finding이 그 안에 없는 짝이 통과하면, 참조는 맞는데 근거는 틀린 레코드가 남는다. 실제 id 참조와
// 내용 지문 참조 양쪽에서 본다.
func ResolveReference(lookup history.SnapshotLookup, r *provisioningv1.SnapshotReference, findingID string) *provisioningv1.SnapshotResolution {
	out := &provisioningv1.SnapshotResolution{What: &provisioningv1.SnapshotResolution_Submitted{Submitted: r}}
	if err := ValidateReference(r); err != nil {
		out.Reason = invalidPrefix + err.Error()
		return out
	}
	if lookup == nil {
		out.Reason = reasonNotLookedUp
		return out
	}
	var snap *history.Snapshot
	var err error
	switch ref := r.GetReference().(type) {
	case *provisioningv1.SnapshotReference_SnapshotId:
		snap, err = lookup.ByID(ref.SnapshotId)
		if err == nil && snap != nil && snap.NodeID != r.GetSourceNodeId() {
			out.Reason = fmt.Sprintf("snapshot %s exists but belongs to node %q, not the referenced source node %q",
				ref.SnapshotId, snap.NodeID, r.GetSourceNodeId())
			return out
		}
	case *provisioningv1.SnapshotReference_Content:
		c := ref.Content
		snap, err = lookup.ByContentHashV1(r.GetSourceNodeId(), c.GetRulesetVersion(), c.GetDigest())
	}
	if err != nil {
		out.Reason = "history lookup failed: " + err.Error()
		return out
	}
	if snap == nil {
		out.Reason = notFoundReason(r)
		return out
	}
	if findingID != "" && !hasFinding(snap, findingID) {
		out.Reason = fmt.Sprintf("snapshot %s was found but does not contain finding %s — the finding and the snapshot are mispaired", snap.ID, findingID)
		return out
	}
	out.ResolvedSnapshotId = snap.ID
	return out
}

// notFoundReason — 못 찾았을 때 가능한 이유를 **값으로** 알린다. 추측을 사람에게 떠넘기지 않는다.
func notFoundReason(r *provisioningv1.SnapshotReference) string {
	switch ref := r.GetReference().(type) {
	case *provisioningv1.SnapshotReference_SnapshotId:
		return fmt.Sprintf("no snapshot %s in this history — a different history, or the id was never ingested here", ref.SnapshotId)
	case *provisioningv1.SnapshotReference_Content:
		return fmt.Sprintf("no snapshot on node %q with ruleset %s and this digest — possible causes: a different history; "+
			"the node was ingested under another name; the ruleset differs (%s here); the asset-scope policy differed so the excluded count differs; "+
			"or the result set differs", r.GetSourceNodeId(), ref.Content.GetRulesetVersion(), ref.Content.GetRulesetVersion())
	}
	return "not found"
}

func hasFinding(s *history.Snapshot, id string) bool {
	for _, f := range s.Findings {
		if f.GetId() == id {
			return true
		}
	}
	return false
}

// ResolveAction — 조치의 근거를 전부 돈다. 근거가 하나도 없으면 계획 단위 derived_from_snapshot_id를
// **legacy 분기**로 읽는다 — SnapshotReference로 합성하지 않는다(source_node_id가 없어 ValidateReference를
// 지나지 못한다). ByID만 하고, 그때는 노드 일치도 finding 소속도 확인할 수 있는 것만 한다.
//
// **근거마다 finding_id가 있어야 한다.** 없으면 「무엇의 근거인가」를 말하지 못하는 근거다 — 스냅샷
// 참조만 맞으면 해결된 모양이 되는데, 그 해결은 아무것도 가리키지 않는다.
//
// **호환용 finding_id는 evidence_sources[0].finding_id와 무조건 같아야 한다.** 비어 있어도 예외가
// 아니다: 계약이 「같아야 하고 생성기가 검사한다」고 적었고, 빈값을 봐주면 새 소비자가 읽는 주 근거와
// 옛 소비자가 읽는 근거가 달라진다.
//
// 근거도 계획 단위 id도 없으면 빈 목록이다. 그것은 추적성 불완전이고 TraceabilityWarnings가 알린다.
//
// GATE: 배선 필수
func ResolveAction(lookup history.SnapshotLookup, plan *provisioningv1.FinalizedPlan, a *provisioningv1.RemediationAction) []*provisioningv1.SnapshotResolution {
	if srcs := a.GetEvidenceSources(); len(srcs) > 0 {
		out := make([]*provisioningv1.SnapshotResolution, 0, len(srcs))
		for i, e := range srcs {
			r := ResolveReference(lookup, e.GetSnapshot(), e.GetFindingId())
			// 모양 검사와 같은 층이다 — 이력이 있든 없든 틀린 것이고, 그래서 같은 접두어를 쓴다.
			if e.GetFindingId() == "" {
				r.ResolvedSnapshotId = ""
				r.Reason = fmt.Sprintf("%sevidence_sources[%d] names no finding_id — a reference with nothing it is evidence of resolves to nothing", invalidPrefix, i)
			}
			out = append(out, r)
		}
		if a.GetFindingId() != srcs[0].GetFindingId() {
			out[0].ResolvedSnapshotId = ""
			out[0].Reason = fmt.Sprintf("%scompat finding_id %q does not equal the primary evidence's finding %q — old and new consumers would read different bases. %s",
				invalidPrefix, a.GetFindingId(), srcs[0].GetFindingId(), strings.TrimPrefix(out[0].Reason, invalidPrefix))
		}
		return out
	}
	legacy := plan.GetDerivedFromSnapshotId()
	if legacy == "" {
		return nil
	}
	out := &provisioningv1.SnapshotResolution{What: &provisioningv1.SnapshotResolution_LegacyPlanSnapshotId{LegacyPlanSnapshotId: legacy}}
	if lookup == nil {
		out.Reason = reasonNotLookedUp
		return []*provisioningv1.SnapshotResolution{out}
	}
	snap, err := lookup.ByID(legacy)
	switch {
	case err != nil:
		out.Reason = "history lookup failed: " + err.Error()
	case snap == nil:
		out.Reason = fmt.Sprintf("no snapshot %s in this history (plan-level compat reference)", legacy)
	case a.GetFindingId() != "" && !hasFinding(snap, a.GetFindingId()):
		out.Reason = fmt.Sprintf("snapshot %s was found but does not contain finding %s (plan-level compat reference)", legacy, a.GetFindingId())
	default:
		out.ResolvedSnapshotId = snap.ID
	}
	return []*provisioningv1.SnapshotResolution{out}
}

const invalidPrefix = "invalid reference: "

// IsInvalidReference — 모양이 틀려서 해결되지 않은 것. 이력과 무관하게 틀린 것이라 DSN이 없는
// 단계에서 이미 세어진다. DSN 단계가 이것을 다시 세면 한 결함을 두 번 센다.
func IsInvalidReference(r *provisioningv1.SnapshotResolution) bool {
	return strings.HasPrefix(r.GetReason(), invalidPrefix)
}

// Unresolved — 해결되지 않은 것 가운데 **불완전으로 셀 것**. 찾지 않은 것(--dsn 없음)은 세지 않는다 —
// 찾지 않은 것이지 못 찾은 것이 아니다. 잘못된 참조는 DSN과 무관하게 센다.
func Unresolved(rs []*provisioningv1.SnapshotResolution) []*provisioningv1.SnapshotResolution {
	var out []*provisioningv1.SnapshotResolution
	for _, r := range rs {
		if r.GetResolvedSnapshotId() == "" && r.GetReason() != reasonNotLookedUp {
			out = append(out, r)
		}
	}
	return out
}
