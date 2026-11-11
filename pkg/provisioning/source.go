// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning

// 조치의 소스 근거(RemediationAction.source_evidence) — 외부 CBOM에서 변환해 저장한 근거 항목을 가리킨다.
// 단위 B 검토안(3판)의 규칙이다.
//
//   - 소스 근거만 있는 조치(finding_id·evidence_sources가 비고 source_evidence만 있음)는 코드·빌드를 고치는
//     조치(APP_RECONFIG·REBUILD)에서만 받는다. 다른 종류는 노드의 런타임 자산을 바꾸므로 런타임 근거가 있어야
//     하고, 어기면 승인도 실행도 거부한다(actionable).
//   - 소스 근거만 있는 조치는 모든 참조가 해석돼야 한다. 승인은 서명 전에, 생성은 **첫 출력 전에** 다시 본다.
//     하나라도 못 찾으면 계획 전체를 거부한다 — 플레이북도 기록도 내지 않는다.
//   - 런타임 근거와 함께 있는 소스 근거(혼합)는 못 찾아도 거부하지 않는다. 그 근거에 기대는 안내(소스 위치·가이드)만
//     내지 않고 미해결 사유를 남기며, 불완전으로 센다.

import (
	"errors"
	"fmt"
	"strconv"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/view"
)

// ErrSourceEvidenceUnresolved — 소스 근거만 있는 조치의 참조를 하나라도 해석하지 못했다.
var ErrSourceEvidenceUnresolved = errors.New("an action based only on source evidence has a reference that does not resolve")

// SourceEvidenceLookup — 소스 근거 저장소(inventory sourceevidence/store)에서 변환 결과를 읽는 자리.
// 조직에 묶인 저장소를 넘긴다. 없으면 nil, nil.
type SourceEvidenceLookup interface {
	Interpretation(id string) (*sourceevidence.Interpretation, error)
}

// FailingSourceLookup — 저장소를 열지 못했을 때 쓴다. 찾을 때마다 그 오류를 사유로 남긴다
// (「찾지 않았다」와 「열지 못했다」를 같은 말로 적지 않는다).
type FailingSourceLookup struct{ Err error }

func (f FailingSourceLookup) Interpretation(string) (*sourceevidence.Interpretation, error) {
	return nil, f.Err
}

// SourceOnly — 소스 근거만 있는 조치인가. 가짜 런타임 finding 없이 표현한 꼴이다.
func SourceOnly(a *provisioningv1.RemediationAction) bool {
	return a.GetFindingId() == "" && len(a.GetEvidenceSources()) == 0 && len(a.GetSourceEvidence()) > 0
}

// sourceOnlyKinds — 소스 근거만으로 받는 조치. 코드·빌드를 고치는 조치이고, 런타임 자산을 바꾸지 않는다.
func sourceOnlyKindAllowed(k provisioningv1.RemediationKind) bool {
	return k == provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG ||
		k == provisioningv1.RemediationKind_REMEDIATION_KIND_REBUILD
}

// PlanHasSourceOnly — 소스 근거만 있는 조치가 하나라도 있는가. 그러면 승인·생성에 저장소가 필요하다.
func PlanHasSourceOnly(p *provisioningv1.FinalizedPlan) bool {
	for _, a := range p.GetActions() {
		if SourceOnly(a) {
			return true
		}
	}
	return false
}

// PlanHasSourceEvidence — 소스 근거가 하나라도 있는가.
func PlanHasSourceEvidence(p *provisioningv1.FinalizedPlan) bool {
	for _, a := range p.GetActions() {
		if len(a.GetSourceEvidence()) > 0 {
			return true
		}
	}
	return false
}

// SourceResolution — 참조 하나를 찾은 결과. Lines는 화면·플레이북 주석에 그대로 쓸 수 있게 안전 처리한 위치 줄이다.
type SourceResolution struct {
	Ref      *provisioningv1.SourceEvidenceReference
	Resolved bool
	Reason   string
	Lines    []string
}

// Proto — 실행 기록에 남길 꼴.
func (r SourceResolution) Proto() *provisioningv1.SourceEvidenceResolution {
	return &provisioningv1.SourceEvidenceResolution{Submitted: r.Ref, Resolved: r.Resolved, Reason: r.Reason}
}

const sourceInvalidPrefix = "invalid source evidence reference: "

// 한 항목에서 위치를 몇 줄까지 보이나. 전부는 소스 근거 조회(pqcota-source-evidence --id)에 있다.
const maxSourceLines = 20

// ResolveSourceEvidence — 조치의 소스 근거를 차례로 찾는다. lookup이 nil이면 모양만 보고 찾지 않았다고 적는다.
func ResolveSourceEvidence(lookup SourceEvidenceLookup, a *provisioningv1.RemediationAction) []SourceResolution {
	refs := a.GetSourceEvidence()
	out := make([]SourceResolution, 0, len(refs))
	seen := map[string]bool{}
	cache := map[string]*sourceevidence.Interpretation{}
	for i, r := range refs {
		res := SourceResolution{Ref: r}
		key := r.GetInterpretationId() + "\x00" + r.GetItemId() + "\x00" + r.GetLocationId()
		switch {
		case r.GetInterpretationId() == "" || r.GetItemId() == "":
			res.Reason = fmt.Sprintf("%ssource_evidence[%d] needs interpretation_id and item_id", sourceInvalidPrefix, i)
		case !isSHA256Hex(r.GetInputSha256()):
			res.Reason = fmt.Sprintf("%ssource_evidence[%d] input_sha256 must be 64 lowercase hex characters", sourceInvalidPrefix, i)
		case seen[key]:
			res.Reason = fmt.Sprintf("%ssource_evidence[%d] repeats an earlier reference", sourceInvalidPrefix, i)
		case lookup == nil:
			res.Reason = "not looked up — no source evidence store was given (--dsn)"
		default:
			in, ok := cache[r.GetInterpretationId()]
			if !ok {
				var err error
				if in, err = lookup.Interpretation(r.GetInterpretationId()); err != nil {
					res.Reason = "source evidence lookup failed: " + err.Error()
					break
				}
				cache[r.GetInterpretationId()] = in
			}
			res.Resolved, res.Reason, res.Lines = match(in, r)
		}
		seen[key] = true
		out = append(out, res)
	}
	return out
}

func match(in *sourceevidence.Interpretation, r *provisioningv1.SourceEvidenceReference) (bool, string, []string) {
	if in == nil {
		return false, fmt.Sprintf("no interpretation %s in this organization's source evidence", view.Safe(r.GetInterpretationId())), nil
	}
	if in.InputSHA256 != r.GetInputSha256() {
		return false, fmt.Sprintf("interpretation %s was made from input %s, not %s", view.Safe(in.ID), in.InputSHA256, view.Safe(r.GetInputSha256())), nil
	}
	for _, it := range in.Items {
		if it.ID != r.GetItemId() {
			continue
		}
		name := ""
		for _, f := range it.Reported {
			if f.Name == "name" {
				name = f.Value
			}
		}
		head := fmt.Sprintf("%s (%s)", view.Safe(name), it.ComponentPointer)
		if r.GetLocationId() == "" {
			lines := []string{head + fmt.Sprintf(" — whole item, %d reported location(s)", len(it.Locations))}
			for i, l := range it.Locations {
				if i == maxSourceLines {
					lines = append(lines, fmt.Sprintf("… %d more — see pqcota-source-evidence --id %s", len(it.Locations)-maxSourceLines, in.ID))
					break
				}
				lines = append(lines, locationLine(l))
			}
			return true, "", lines
		}
		for _, l := range it.Locations {
			if l.ID == r.GetLocationId() {
				return true, "", []string{head, locationLine(l)}
			}
		}
		return false, fmt.Sprintf("item %s has no location %s", view.Safe(it.ID), view.Safe(r.GetLocationId())), nil
	}
	return false, fmt.Sprintf("interpretation %s has no item %s", view.Safe(in.ID), view.Safe(r.GetItemId())), nil
}

// locationLine — 보고된 위치 한 줄. 경로는 CBOM에서 온 글이라 제어 문자를 이스케이프한다(플레이북 주석을 깨지 않게).
func locationLine(l sourceevidence.Location) string {
	s := "reported location " + view.Safe(l.Reported.Path)
	if l.Reported.Line != nil {
		s += ":" + strconv.FormatInt(*l.Reported.Line, 10)
	}
	if l.Reported.Column != nil {
		s += ":" + strconv.FormatInt(*l.Reported.Column, 10)
	}
	if l.Normalized.AbsentReason != "" {
		s += " (" + view.Safe(l.Normalized.AbsentReason) + ")"
	}
	return s
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// SourceEvidenceCheck — 계획 전체의 소스 근거 해석 결과. 조치 id → 결과(소스 근거가 있는 조치만).
type SourceEvidenceCheck map[string][]SourceResolution

// CheckSourceEvidence — 승인·생성이 **출력 전에** 부른다. 소스 근거만 있는 조치의 참조가 하나라도 미해결이면
// ErrSourceEvidenceUnresolved(사유를 모두 싣는다). 혼합 근거 조치의 미해결은 결과에만 남는다(Incomplete).
//
// GATE: 배선 필수
func CheckSourceEvidence(lookup SourceEvidenceLookup, p *provisioningv1.FinalizedPlan) (SourceEvidenceCheck, error) {
	out := SourceEvidenceCheck{}
	var bad []string
	for _, a := range p.GetActions() {
		if len(a.GetSourceEvidence()) == 0 {
			continue
		}
		rs := ResolveSourceEvidence(lookup, a)
		out[a.GetId()] = rs
		if !SourceOnly(a) {
			continue
		}
		for i, r := range rs {
			if !r.Resolved {
				bad = append(bad, fmt.Sprintf("action %s (node=%s) source_evidence[%d]: %s", a.GetId(), a.GetTargetNodeId(), i, r.Reason))
			}
		}
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("%w:\n  %s", ErrSourceEvidenceUnresolved, joinLines(bad))
	}
	return out, nil
}

// Incomplete — 혼합 근거 조치에서 해석하지 못한 소스 근거(불완전으로 센다). 소스 근거만 있는 조치는
// CheckSourceEvidence가 이미 거부했으므로 여기 오지 않는다.
func (c SourceEvidenceCheck) Incomplete(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	for _, a := range p.GetActions() {
		for i, r := range c[a.GetId()] {
			if !r.Resolved {
				out = append(out, fmt.Sprintf("action %s (node=%s): source_evidence[%d] not resolved — %s. No source location or guide is given for it.",
					a.GetId(), a.GetTargetNodeId(), i, r.Reason))
			}
		}
	}
	return out
}

// Notes — 플레이북에 붙일 조치별 주석 줄. 찾은 소스 위치만 보이고, 못 찾은 근거는 사유만 적는다.
func (c SourceEvidenceCheck) Notes() map[string][]string {
	out := map[string][]string{}
	for id, rs := range c {
		var ls []string
		ls = append(ls, "source evidence (reported locations, for review — not checked against the current source):")
		for i, r := range rs {
			if !r.Resolved {
				ls = append(ls, fmt.Sprintf("  [%d] not resolved: %s — no source location or guide is given for it", i, r.Reason))
				continue
			}
			for j, l := range r.Lines {
				if j == 0 {
					ls = append(ls, fmt.Sprintf("  [%d] %s", i, l))
				} else {
					ls = append(ls, "      "+l)
				}
			}
		}
		out[id] = ls
	}
	return out
}

func joinLines(ss []string) string {
	s := ""
	for i, x := range ss {
		if i > 0 {
			s += "\n  "
		}
		s += x
	}
	return s
}
