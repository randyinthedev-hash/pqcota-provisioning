// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning

import (
	"fmt"
	"strings"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/view"
)

// ReviewSummary — 승인자가 서명 전에 볼 것(가이드 계약 §6). 조치마다 근거·가이드·작성자가 확인한 것·고른 근거·관측 한계.
// 「수정 완료」 같은 상태는 보이지 않는다. 계획·카탈로그·CBOM에서 온 글은 제어 문자를 이스케이프한다.
func ReviewSummary(p *provisioningv1.FinalizedPlan, src SourceEvidenceCheck, guides GuideCheck) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	s := view.Safe
	w("plan %s · status %s · %d action(s)", s(p.GetId()), p.GetStatus(), len(p.GetActions()))
	for ai, a := range p.GetActions() {
		w("")
		w("action[%d] %s · node %s · kind %s · target %s", ai, s(a.GetId()), s(a.GetTargetNodeId()), a.GetKind(), s(a.GetTargetAlgorithm()))
		for i, e := range a.GetEvidenceSources() {
			w("  runtime evidence [%d]: finding %s · snapshot reference from node %s", i, s(e.GetFindingId()), s(e.GetSnapshot().GetSourceNodeId()))
		}
		for i, r := range src.Of(ai) {
			if !r.Resolved {
				w("  source evidence [%d]: NOT resolved — %s", i, s(r.Reason))
				continue
			}
			for j, l := range r.Lines {
				if j == 0 {
					w("  source evidence [%d]: %s", i, l)
				} else {
					w("      %s", l)
				}
			}
		}
		if len(src.Of(ai)) > 0 {
			w("    (reported locations, for review — not checked against the current source)")
		}
		g := guides.Of(ai)
		if g == nil {
			if a.GetGuide() == nil {
				w("  guide: none")
			}
			continue
		}
		sel := g.Selection
		if !g.Resolved {
			w("  guide: %s revision %s — NOT resolved: %s", s(sel.GetGuideId()), sel.GetRevision(), s(g.Reason))
			continue
		}
		gd := g.Guide
		w("  guide: %s revision %s — %s", s(gd.ID), gd.Revision, s(gd.Title))
		w("    body: %s", s(gd.BodyPath))
		w("      sha256 %s — matches the plan", gd.BodySHA256)
		w("    manifest sha256 %s — matches the plan", gd.ManifestSHA256)
		for _, e := range gd.Examples {
			w("    example: %s sha256 %s — matches the manifest", s(e.Path), e.SHA256)
		}
		w("    purpose confirmed by plan author: %s", s(sel.GetPurpose()))
		for _, c := range sel.GetConditions() {
			w("    condition confirmed by plan author: %s", s(c))
		}
		if sel.GetRationale() != "" {
			w("    rationale: %s", s(sel.GetRationale()))
		}
		for _, c := range g.Chosen {
			w("    chosen for: %s", c)
		}
		for _, l := range gd.ObservationLimits {
			w("    observation limit: %s", s(l))
		}
	}
	return b.String()
}

// ReviewBodies — 검증한 본문 전문(가이드·판마다 한 번). 해시를 잰 그 바이트이고, 줄마다 제어 문자를 이스케이프한다.
func ReviewBodies(guides GuideCheck) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, g := range guides {
		if g == nil || !g.Resolved {
			continue
		}
		key := g.Guide.ID + "@" + g.Guide.Revision
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(&b, "\n===== guide body: %s revision %s · %s · sha256 %s =====\n", view.Safe(g.Guide.ID), g.Guide.Revision, view.Safe(g.Guide.BodyPath), g.Guide.BodySHA256)
		for _, l := range strings.Split(strings.TrimSuffix(string(g.Guide.Body), "\n"), "\n") {
			b.WriteString(view.Safe(l))
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "===== end of %s revision %s =====\n", view.Safe(g.Guide.ID), g.Guide.Revision)
	}
	return b.String()
}

// GuideResolutionProto — 실행 기록에 남길 꼴.
func (r *GuideResult) Proto(approvalsVerified bool) *provisioningv1.GuideResolution {
	return &provisioningv1.GuideResolution{Submitted: r.Selection, Resolved: r.Resolved, Reason: r.Reason,
		BodySha256Found: r.Found[0], ManifestSha256Found: r.Found[1], ApprovalsVerified: approvalsVerified}
}
