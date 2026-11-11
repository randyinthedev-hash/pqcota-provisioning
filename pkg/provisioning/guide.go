// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning

// 조치의 가이드(RemediationAction.guide) — 가이드 계약 검토안 2판(pqcota-aai plans/2026-11-11-guide-selection-contract.md).
//
//   - 가이드는 로컬 판별 카탈로그(<guide-dir>/<이름>/<판>/guide.json + 본문 + 예제)에서 해시로 대조한다. URL에서 받지 않고,
//     같은 id의 다른 판으로 바꾸지 않는다. 파일은 그 판의 디렉터리 안으로 한정하고, 한 번 읽은 바이트로 해시를 재고 그 바이트를 쓴다.
//   - 가이드가 고른 근거(런타임·소스 순번)도 출력 전에 해석해야 한다.
//   - 소스 근거만 있는 조치의 가이드가 맞지 않으면 계획 전체를 거부한다. 그 밖의 조치는 가이드만 빼고 불완전으로 센다.
//   - **승인 서명을 검증한 실행에서만 「approved guide」라고 적는다.** --allow-unverified-approvals로 돌면 매니페스트·본문이
//     맞아도 「계획에 적힌 가이드(승인 미확인)」로 적는다.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/view"
)

// ErrGuideUnresolved — 소스 근거만 있는 조치의 가이드가 카탈로그와 맞지 않거나, 가이드가 고른 근거를 해석하지 못했다.
var ErrGuideUnresolved = errors.New("an action based only on source evidence names a guide that does not resolve")

var (
	guideIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9./-]{0,127}$`)
	guideRevPattern = regexp.MustCompile(`^[0-9]{1,6}$`)
	guideDirName    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)
)

// 카탈로그 파일의 상한 — 매니페스트·본문·예제 하나.
const (
	maxManifestBytes = 64 << 10
	maxGuideFile     = 4 << 20
)

// Guide — 카탈로그에서 대조를 마친 가이드. Body·예제는 해시를 잰 그 바이트다.
type Guide struct {
	ID, Revision, Title string
	Purposes            []string
	ObservationLimits   []string
	Dir                 string // 판 디렉터리(실제 경로)
	BodyPath            string
	Body                []byte
	BodySHA256          string
	ManifestSHA256      string
	Examples            []GuideExample
}

// GuideExample — 매니페스트가 적은 예제 하나(해시가 맞은 것).
type GuideExample struct {
	Path   string
	SHA256 string
}

type guideManifest struct {
	ID                string   `json:"id"`
	Revision          string   `json:"revision"`
	Title             string   `json:"title"`
	Purposes          []string `json:"purposes"`
	ObservationLimits []string `json:"observationLimits"`
	Body              string   `json:"body"`
	Examples          []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"examples"`
}

// guideShape — 계약의 모양 규칙(저장소·카탈로그 없이). actionable이 부른다.
func guideShape(a *provisioningv1.RemediationAction) error {
	g := a.GetGuide()
	if g == nil {
		return nil
	}
	switch {
	case !guideIDPattern.MatchString(g.GetGuideId()):
		return fmt.Errorf("guide_id %q must match %s", g.GetGuideId(), guideIDPattern)
	case !guideRevPattern.MatchString(g.GetRevision()):
		return fmt.Errorf("guide revision %q must be 1 to 6 digits", g.GetRevision())
	case !isSHA256Hex(g.GetBodySha256()):
		return fmt.Errorf("guide body_sha256 must be 64 lowercase hex characters")
	case !isSHA256Hex(g.GetManifestSha256()):
		return fmt.Errorf("guide manifest_sha256 must be 64 lowercase hex characters")
	case g.GetPurpose() == "":
		return fmt.Errorf("guide purpose is empty — the plan author must name the confirmed purpose")
	case SourceOnly(a) && len(g.GetEvidenceSourceIndexes()) > 0:
		return fmt.Errorf("guide chooses runtime evidence, but the action is based only on source evidence")
	}
	for i, c := range g.GetConditions() {
		if c == "" {
			return fmt.Errorf("guide condition [%d] is empty", i)
		}
	}
	if err := indexSet(g.GetEvidenceSourceIndexes(), len(a.GetEvidenceSources()), "evidence_source_indexes"); err != nil {
		return err
	}
	return indexSet(g.GetSourceEvidenceIndexes(), len(a.GetSourceEvidence()), "source_evidence_indexes")
}

// indexSet — 오름차순·중복 없음·범위 안. 정렬된 집합이라 순서만 다른 같은 집합이 생기지 않는다.
func indexSet(ix []uint32, n int, name string) error {
	for i, v := range ix {
		if int(v) >= n {
			return fmt.Errorf("guide %s[%d]=%d is out of range (the action has %d)", name, i, v, n)
		}
		if i > 0 && v <= ix[i-1] {
			return fmt.Errorf("guide %s must be strictly increasing (no duplicates)", name)
		}
	}
	return nil
}

// LoadGuide — 카탈로그에서 그 판을 찾아 대조한다. 맞지 않으면 nil과 사유. found에는 잰 해시가 남는다.
func LoadGuide(dir string, sel *provisioningv1.GuideSelection) (g *Guide, reason string, found [2]string) {
	if dir == "" {
		return nil, "no guide catalog was given (--guide-dir) — the guide cannot be checked", found
	}
	name := sel.GetGuideId()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if !guideDirName.MatchString(name) {
		return nil, fmt.Sprintf("guide id %q does not end in a catalog directory name", view.Safe(sel.GetGuideId())), found
	}
	// 카탈로그와 판 디렉터리를 **디렉터리 핸들**(os.Root)로 연다. 경로를 확인한 뒤 다시 경로로 여는 방식은 두 호출
	// 사이에 경로가 바뀌면 확인한 경계가 지켜지지 않는다. os.Root는 여는 순간에 핸들 밖으로 나가는 경로(.., 밖을
	// 가리키는 심볼릭 링크)를 거부하고, 아래의 모든 파일은 이 핸들에서 열어 그 바이트로 해시를 잰다.
	cat, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "the guide catalog cannot be read: " + err.Error(), found
	}
	defer cat.Close()
	revName := filepath.Join(name, sel.GetRevision())
	rev, err := cat.OpenRoot(revName)
	if err != nil {
		if escapes(err) {
			return nil, "the revision directory resolves outside the guide catalog", found
		}
		return nil, fmt.Sprintf("the catalog has no %s revision %s%s", name, sel.GetRevision(), otherRevisions(cat, name)), found
	}
	defer rev.Close()
	realRev := filepath.Join(dir, revName) // 표시용 경로. 읽기는 rev 핸들로만 한다

	mraw, err := readWithin(rev, "guide.json", maxManifestBytes)
	if err != nil {
		return nil, "manifest: " + err.Error(), found
	}
	found[1] = sha256Hex(mraw)
	if found[1] != sel.GetManifestSha256() {
		return nil, fmt.Sprintf("manifest hash differs: the plan approved %s, the catalog has %s", sel.GetManifestSha256(), found[1]), found
	}
	var m guideManifest
	dec := json.NewDecoder(bytes.NewReader(mraw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, "manifest is not valid: " + err.Error(), found
	}
	// JSON 문서 하나만 받는다. 첫 객체 뒤에는 공백과 EOF만 — CBOM 파서(sourceevidence.decode)와 같다.
	if _, err := dec.Token(); err != io.EOF {
		return nil, "manifest is not valid: content after the JSON document", found
	}
	if m.ID != sel.GetGuideId() || m.Revision != sel.GetRevision() {
		return nil, fmt.Sprintf("manifest names %s revision %s, the plan names %s revision %s",
			view.Safe(m.ID), view.Safe(m.Revision), view.Safe(sel.GetGuideId()), sel.GetRevision()), found
	}
	body, err := readWithin(rev, m.Body, maxGuideFile)
	if err != nil {
		return nil, "body: " + err.Error(), found
	}
	found[0] = sha256Hex(body)
	if found[0] != sel.GetBodySha256() {
		return nil, fmt.Sprintf("body hash differs: the plan approved %s, the catalog has %s", sel.GetBodySha256(), found[0]), found
	}
	g = &Guide{ID: m.ID, Revision: m.Revision, Title: m.Title, Purposes: m.Purposes, ObservationLimits: m.ObservationLimits,
		Dir: realRev, BodyPath: filepath.Join(realRev, filepath.Clean(m.Body)), Body: body, BodySHA256: found[0], ManifestSHA256: found[1]}
	seen := map[string]bool{filepath.Clean(m.Body): true, "guide.json": true}
	for _, e := range m.Examples {
		p := filepath.Clean(e.Path)
		if seen[p] {
			return nil, fmt.Sprintf("example %s is listed twice (or is the body or the manifest)", view.Safe(e.Path)), found
		}
		seen[p] = true
		b, err := readWithin(rev, e.Path, maxGuideFile)
		if err != nil {
			return nil, "example: " + err.Error(), found
		}
		if got := sha256Hex(b); got != e.SHA256 {
			return nil, fmt.Sprintf("example %s hash differs: the manifest says %s, the file is %s", view.Safe(e.Path), view.Safe(e.SHA256), got), found
		}
		g.Examples = append(g.Examples, GuideExample{Path: p, SHA256: e.SHA256})
	}
	ok := false
	for _, p := range m.Purposes {
		if p == sel.GetPurpose() {
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Sprintf("purpose %q is not one the guide covers (%s)", view.Safe(sel.GetPurpose()), view.Safe(strings.Join(m.Purposes, ", "))), found
	}
	return g, "", found
}

// readWithin — 판 디렉터리 핸들 안의 상대 경로만 읽는다. `..`·절대 경로는 먼저 거부해 사유를 분명히 하고,
// 밖을 가리키는 심볼릭 링크는 os.Root가 여는 순간에 거부한다. 읽은 바이트를 그대로 돌려준다(그것으로 해시를 잰다).
func readWithin(root *os.Root, rel string, max int64) ([]byte, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return nil, fmt.Errorf("%q must be a relative path inside the guide directory", view.Safe(rel))
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return nil, fmt.Errorf("%q must not contain ..", view.Safe(rel))
		}
	}
	f, err := root.Open(rel)
	if err != nil {
		if escapes(err) {
			return nil, fmt.Errorf("%q resolves outside the guide directory", view.Safe(rel))
		}
		return nil, fmt.Errorf("%s: %v", view.Safe(rel), err)
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", view.Safe(rel))
	}
	var b bytes.Buffer
	n, err := b.ReadFrom(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if n > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", view.Safe(rel), max)
	}
	return b.Bytes(), nil
}

// escapes — os.Root가 핸들 밖으로 나가는 경로를 거부한 오류인가.
func escapes(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func otherRevisions(cat *os.Root, name string) string {
	d, err := cat.Open(name)
	if err != nil {
		return ""
	}
	defer d.Close()
	es, err := d.ReadDir(-1)
	if err != nil {
		return ""
	}
	var rs []string
	for _, e := range es {
		if e.IsDir() {
			rs = append(rs, e.Name())
		}
	}
	if len(rs) == 0 {
		return ""
	}
	sort.Strings(rs)
	return " (it has " + strings.Join(rs, ", ") + " — another revision is not used in its place)"
}

// GuideResult — 조치 하나의 가이드 대조 결과.
type GuideResult struct {
	Selection *provisioningv1.GuideSelection
	Guide     *Guide // 맞았을 때
	Resolved  bool
	Reason    string
	Found     [2]string // 잰 본문·매니페스트 해시
	Chosen    []string  // 고른 근거의 표시 줄(안전 처리 끝)
}

// GuideCheck — 조치 순번 → 결과(가이드가 없는 조치는 nil). SourceEvidenceCheck와 같이 순번으로 잇는다.
type GuideCheck []*GuideResult

// Of — i번째 조치의 결과.
func (c GuideCheck) Of(i int) *GuideResult {
	if i < 0 || i >= len(c) {
		return nil
	}
	return c[i]
}

// PlanHasGuide — 가이드가 하나라도 있는가.
func PlanHasGuide(p *provisioningv1.FinalizedPlan) bool {
	for _, a := range p.GetActions() {
		if a.GetGuide() != nil {
			return true
		}
	}
	return false
}

// PlanGuideChoosesRuntime — 가이드가 런타임 근거를 고른 조치가 있는가. 그러면 승인·생성이 출력 전에 이력을 조회한다.
func PlanGuideChoosesRuntime(p *provisioningv1.FinalizedPlan) bool {
	for _, a := range p.GetActions() {
		if len(a.GetGuide().GetEvidenceSourceIndexes()) > 0 {
			return true
		}
	}
	return false
}

// CheckGuides — 승인·생성이 **출력 전에** 부른다. 카탈로그 대조와, 가이드가 고른 근거(소스: src의 결과, 런타임: hist 조회)를 본다.
// 소스 근거만 있는 조치의 가이드가 맞지 않으면 ErrGuideUnresolved. 그 밖의 미해결은 결과에만 남는다.
//
// GATE: 배선 필수
func CheckGuides(p *provisioningv1.FinalizedPlan, dir string, src SourceEvidenceCheck, hist history.SnapshotLookup) (GuideCheck, error) {
	out := make(GuideCheck, len(p.GetActions()))
	var bad []string
	for ai, a := range p.GetActions() {
		sel := a.GetGuide()
		if sel == nil {
			continue
		}
		r := &GuideResult{Selection: sel}
		out[ai] = r
		g, reason, found := LoadGuide(dir, sel)
		r.Found = found
		if g == nil {
			r.Reason = reason
		} else {
			r.Guide = g
			r.Reason = r.chosen(a, ai, src, hist)
			r.Resolved = r.Reason == ""
		}
		if !r.Resolved && SourceOnly(a) {
			bad = append(bad, fmt.Sprintf("action[%d] %s (node=%s) guide %s revision %s: %s", ai, a.GetId(), a.GetTargetNodeId(),
				view.Safe(sel.GetGuideId()), sel.GetRevision(), r.Reason))
		}
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("%w:\n  %s", ErrGuideUnresolved, joinLines(bad))
	}
	return out, nil
}

// chosen — 가이드가 고른 근거를 해석한다. 하나라도 미해결이면 그 사유(가이드가 그 근거로 골라졌으므로 가이드도 쓰지 않는다).
func (r *GuideResult) chosen(a *provisioningv1.RemediationAction, ai int, src SourceEvidenceCheck, hist history.SnapshotLookup) string {
	sel := r.Selection
	for _, i := range sel.GetSourceEvidenceIndexes() {
		rs := src.Of(ai)
		if int(i) >= len(rs) || !rs[i].Resolved {
			why := "not looked up"
			if int(i) < len(rs) {
				why = rs[i].Reason
			}
			return fmt.Sprintf("the source evidence it was chosen for ([source %d]) did not resolve: %s", i, why)
		}
		for j, l := range rs[i].Lines {
			pre := fmt.Sprintf("[source %d] ", i)
			if j > 0 {
				pre = "           "
			}
			r.Chosen = append(r.Chosen, pre+l)
		}
	}
	for _, i := range sel.GetEvidenceSourceIndexes() {
		e := a.GetEvidenceSources()[i]
		if hist == nil {
			return fmt.Sprintf("the runtime evidence it was chosen for ([runtime %d]) was not looked up — no history store was given (--dsn)", i)
		}
		res := ResolveReference(hist, e.GetSnapshot(), e.GetFindingId())
		if e.GetFindingId() == "" || res.GetResolvedSnapshotId() == "" {
			why := res.GetReason()
			if e.GetFindingId() == "" {
				why = "the evidence names no finding_id"
			}
			return fmt.Sprintf("the runtime evidence it was chosen for ([runtime %d]) did not resolve: %s", i, why)
		}
		r.Chosen = append(r.Chosen, fmt.Sprintf("[runtime %d] finding %s in snapshot %s (node %s)", i,
			view.Safe(e.GetFindingId()), view.Safe(res.GetResolvedSnapshotId()), view.Safe(e.GetSnapshot().GetSourceNodeId())))
	}
	if len(sel.GetSourceEvidenceIndexes())+len(sel.GetEvidenceSourceIndexes()) == 0 {
		r.Chosen = append(r.Chosen, "no specific location — a general guide for the confirmed purpose")
	}
	return ""
}

// Incomplete — 소스 근거만 있는 조치가 아닌 조치의 미해결 가이드(불완전으로 센다).
func (c GuideCheck) Incomplete(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	for ai, a := range p.GetActions() {
		if r := c.Of(ai); r != nil && !r.Resolved {
			out = append(out, fmt.Sprintf("action[%d] %s (node=%s): guide %s revision %s not given — %s",
				ai, a.GetId(), a.GetTargetNodeId(), view.Safe(r.Selection.GetGuideId()), r.Selection.GetRevision(), r.Reason))
		}
	}
	return out
}

// Notes — 플레이북 주석 줄(순번별). approvalsVerified가 아니면 「approved guide」라고 쓰지 않는다.
func (c GuideCheck) Notes(approvalsVerified bool) [][]string {
	out := make([][]string, len(c))
	for ai, r := range c {
		if r == nil {
			continue
		}
		sel := r.Selection
		if !r.Resolved {
			out[ai] = []string{fmt.Sprintf("guide not given: %s revision %s — %s", sel.GetGuideId(), sel.GetRevision(), r.Reason)}
			continue
		}
		g := r.Guide
		label := "approved guide"
		if !approvalsVerified {
			label = "guide named in the plan (approval signatures NOT verified in this run — not shown as approved)"
		}
		ls := []string{
			fmt.Sprintf("%s: %s, revision %s — %s", label, g.ID, g.Revision, g.Title),
			fmt.Sprintf("guide body: sha256:%s (matches the catalog: %s)", g.BodySHA256, g.BodyPath),
			fmt.Sprintf("guide manifest: sha256:%s", g.ManifestSHA256),
			"purpose confirmed by plan author: " + sel.GetPurpose(),
		}
		if len(sel.GetConditions()) > 0 {
			ls = append(ls, "conditions confirmed by plan author: "+strings.Join(sel.GetConditions(), "; "))
		}
		for _, ch := range r.Chosen {
			ls = append(ls, "chosen for: "+ch)
		}
		for _, l := range g.ObservationLimits {
			ls = append(ls, "observation limit: "+l)
		}
		ls = append(ls, "→ review the reported places and follow the guide's steps and tests. Nothing here says the change is done.")
		out[ai] = ls
	}
	return out
}

// MergeNotes — 순번별 주석 두 벌을 잇는다(소스 근거 다음 가이드).
func MergeNotes(a, b [][]string) [][]string {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([][]string, n)
	for i := range out {
		if i < len(a) {
			out[i] = append(out[i], a[i]...)
		}
		if i < len(b) {
			out[i] = append(out[i], b[i]...)
		}
	}
	return out
}
