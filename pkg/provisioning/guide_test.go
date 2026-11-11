// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"google.golang.org/protobuf/proto"
)

func hexsum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

const testGuideBody = "# Doc key transport\n\nStep 1: replace RSA-OAEP key wrapping.\x1b[31m\nExample sha256 EXAMPLESHA\n"

// catalog — <dir>/doc-key/1/{guide.json, README.md, Example.java}. edit가 있으면 매니페스트 본문을 고친다.
func catalog(t *testing.T, edit func(m string) string) (dir string, sel *provisioningv1.GuideSelection) {
	t.Helper()
	dir = t.TempDir()
	rev := filepath.Join(dir, "doc-key", "1")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	ex := []byte("class Example {}\n")
	body := []byte(strings.Replace(testGuideBody, "EXAMPLESHA", hexsum(ex), 1))
	m := `{"id":"pqcota-guide/doc-key","revision":"1","title":"Document key transport with ML-KEM",
 "purposes":["document-key-transport"],"observationLimits":["runtime sees BC registered, not that keys travel under RSA"],
 "body":"README.md","examples":[{"path":"Example.java","sha256":"` + hexsum(ex) + `"}]}`
	if edit != nil {
		m = edit(m)
	}
	for name, b := range map[string][]byte{"README.md": body, "Example.java": ex, "guide.json": []byte(m)} {
		if err := os.WriteFile(filepath.Join(rev, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, &provisioningv1.GuideSelection{GuideId: "pqcota-guide/doc-key", Revision: "1", BodySha256: hexsum(body),
		ManifestSha256: hexsum([]byte(m)), Purpose: "document-key-transport", Conditions: []string{"JCA", "JDK 21"}, Rationale: "wraps keys with RSA-OAEP"}
}

func TestLoadGuide(t *testing.T) {
	dir, sel := catalog(t, nil)
	if g, why, _ := provisioning.LoadGuide(dir, sel); g == nil || why != "" || len(g.Examples) != 1 || g.Title == "" {
		t.Fatalf("good guide: %v %q", g, why)
	}
	clone := func(f func(*provisioningv1.GuideSelection)) *provisioningv1.GuideSelection {
		c := &provisioningv1.GuideSelection{GuideId: sel.GuideId, Revision: sel.Revision, BodySha256: sel.BodySha256, ManifestSha256: sel.ManifestSha256, Purpose: sel.Purpose}
		f(c)
		return c
	}
	cases := []struct {
		name string
		dir  string
		sel  *provisioningv1.GuideSelection
		want string
	}{
		{"no catalog", "", sel, "no guide catalog"},
		{"other revision only", dir, clone(func(c *provisioningv1.GuideSelection) { c.Revision = "2" }), "it has 1 — another revision is not used"},
		{"manifest hash", dir, clone(func(c *provisioningv1.GuideSelection) { c.ManifestSha256 = strings.Repeat("0", 64) }), "manifest hash differs"},
		{"body hash", dir, clone(func(c *provisioningv1.GuideSelection) { c.BodySha256 = strings.Repeat("0", 64) }), "body hash differs"},
		{"purpose", dir, clone(func(c *provisioningv1.GuideSelection) { c.Purpose = "tls-key-exchange" }), "is not one the guide covers"},
	}
	for _, c := range cases {
		if g, why, _ := provisioning.LoadGuide(c.dir, c.sel); g != nil || !strings.Contains(why, c.want) {
			t.Errorf("%s: %q, want %q", c.name, why, c.want)
		}
	}
	// 매니페스트를 고친 카탈로그 — 그 매니페스트에 맞춘 선택이라 매니페스트 해시는 맞고, 그 다음 검사에 걸린다.
	edits := []struct {
		name string
		edit func(string) string
		want string
	}{
		{"id differs", func(m string) string {
			return strings.Replace(m, `"id":"pqcota-guide/doc-key"`, `"id":"pqcota-guide/other"`, 1)
		}, "manifest names"},
		{"body outside", func(m string) string { return strings.Replace(m, `"body":"README.md"`, `"body":"../../x"`, 1) }, "must not contain .."},
		{"absolute example", func(m string) string { return strings.Replace(m, `"path":"Example.java"`, `"path":"/etc/passwd"`, 1) }, "relative path"},
		{"duplicate example", func(m string) string { return strings.Replace(m, `"path":"Example.java"`, `"path":"README.md"`, 1) }, "listed twice"},
		{"example hash", func(m string) string { return strings.Replace(m, `"sha256":"`, `"sha256":"0`, 1) }, "hash differs"},
		{"unknown field", func(m string) string { return strings.Replace(m, `"title"`, `"extra":1,"title"`, 1) }, "not valid"},
		// JSON 문서 하나만 — 유효한 객체 뒤에 내용이 붙으면 해시가 맞아도 받지 않는다.
		{"trailing object", func(m string) string { return m + ` {"id":"x"}` }, "content after the JSON document"},
		{"trailing garbage", func(m string) string { return m + "\n]" }, "content after the JSON document"},
	}
	for _, e := range edits {
		d, s := catalog(t, e.edit)
		if g, why, _ := provisioning.LoadGuide(d, s); g != nil || !strings.Contains(why, e.want) {
			t.Errorf("%s: %q, want %q", e.name, why, e.want)
		}
	}
	// 예제를 매니페스트에서 빼면 매니페스트 해시가 달라진다 — 승인한 선택(옛 해시)으로는 걸린다.
	d, s := catalog(t, nil)
	rev := filepath.Join(d, "doc-key", "1")
	m, _ := os.ReadFile(filepath.Join(rev, "guide.json"))
	i := strings.Index(string(m), `,"examples"`)
	os.WriteFile(filepath.Join(rev, "guide.json"), []byte(string(m[:i])+"}"), 0o644)
	if g, why, _ := provisioning.LoadGuide(d, s); g != nil || !strings.Contains(why, "manifest hash differs") {
		t.Errorf("example removed from the manifest: %q", why)
	}
	// 판 디렉터리 밖을 가리키는 심볼릭 링크.
	d, s = catalog(t, nil)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("x"), 0o644)
	rev = filepath.Join(d, "doc-key", "1")
	os.Remove(filepath.Join(rev, "Example.java"))
	if err := os.Symlink(outside, filepath.Join(rev, "Example.java")); err != nil {
		t.Skip("symlinks not available")
	}
	if g, why, _ := provisioning.LoadGuide(d, s); g != nil || !strings.Contains(why, "resolves outside") {
		t.Errorf("symlink outside: %q", why)
	}
	// 판 디렉터리 자체가 카탈로그 밖을 가리키는 심볼릭 링크.
	d, s = catalog(t, nil)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	os.Rename(filepath.Join(d, "doc-key", "1"), elsewhere)
	os.Symlink(elsewhere, filepath.Join(d, "doc-key", "1"))
	if g, why, _ := provisioning.LoadGuide(d, s); g != nil || !strings.Contains(why, "revision directory resolves outside") {
		t.Errorf("revision directory outside: %q", why)
	}
	// 판 디렉터리 안에 머무는 심볼릭 링크는 따라간다.
	d, s = catalog(t, nil)
	rev = filepath.Join(d, "doc-key", "1")
	os.Mkdir(filepath.Join(rev, "src"), 0o755)
	os.Rename(filepath.Join(rev, "Example.java"), filepath.Join(rev, "src", "Example.java"))
	os.Symlink(filepath.Join("src", "Example.java"), filepath.Join(rev, "Example.java"))
	if g, why, _ := provisioning.LoadGuide(d, s); g == nil {
		t.Errorf("a symlink that stays inside the revision directory was refused: %q", why)
	}
	// 카탈로그 안이라도 판 디렉터리 밖이면 거부한다(파일은 그 판 디렉터리 안으로 한정).
	d, s = catalog(t, nil)
	rev = filepath.Join(d, "doc-key", "1")
	os.Rename(filepath.Join(rev, "Example.java"), filepath.Join(d, "Example.java"))
	os.Symlink(filepath.Join("..", "..", "Example.java"), filepath.Join(rev, "Example.java"))
	if g, why, _ := provisioning.LoadGuide(d, s); g != nil || !strings.Contains(why, "resolves outside the guide directory") {
		t.Errorf("a symlink to elsewhere in the catalog: %q", why)
	}
	// 예제 자리가 디렉터리면 읽지 않는다.
	d, s = catalog(t, nil)
	rev = filepath.Join(d, "doc-key", "1")
	os.Remove(filepath.Join(rev, "Example.java"))
	os.Mkdir(filepath.Join(rev, "Example.java"), 0o755)
	if g, why, _ := provisioning.LoadGuide(d, s); g != nil || !strings.Contains(why, "not a regular file") {
		t.Errorf("directory as example: %q", why)
	}
}

func TestGuideShape(t *testing.T) {
	_, in := seFixture(t)
	_, sel := catalog(t, nil)
	good := func() *provisioningv1.RemediationAction {
		a := runtimeAction(ref(in, 0, 0), ref(in, 0, 1))
		a.EvidenceSources = []*provisioningv1.ActionEvidenceSource{{FindingId: "f1"}}
		a.Guide = &provisioningv1.GuideSelection{GuideId: sel.GuideId, Revision: "1", BodySha256: sel.BodySha256, ManifestSha256: sel.ManifestSha256,
			Purpose: sel.Purpose, EvidenceSourceIndexes: []uint32{0}, SourceEvidenceIndexes: []uint32{0, 1}}
		return a
	}
	if err := provisioning.Executable(finalized(good())); err != nil {
		t.Fatalf("good: %v", err)
	}
	for name, f := range map[string]func(*provisioningv1.GuideSelection){
		"bad id":               func(g *provisioningv1.GuideSelection) { g.GuideId = "Bad Id" },
		"bad revision":         func(g *provisioningv1.GuideSelection) { g.Revision = "1.0" },
		"uppercase body hash":  func(g *provisioningv1.GuideSelection) { g.BodySha256 = strings.ToUpper(g.BodySha256) },
		"no manifest hash":     func(g *provisioningv1.GuideSelection) { g.ManifestSha256 = "" },
		"no purpose":           func(g *provisioningv1.GuideSelection) { g.Purpose = "" },
		"empty condition":      func(g *provisioningv1.GuideSelection) { g.Conditions = []string{"JCA", ""} },
		"index out of range":   func(g *provisioningv1.GuideSelection) { g.SourceEvidenceIndexes = []uint32{2} },
		"indexes not ordered":  func(g *provisioningv1.GuideSelection) { g.SourceEvidenceIndexes = []uint32{1, 0} },
		"duplicate index":      func(g *provisioningv1.GuideSelection) { g.SourceEvidenceIndexes = []uint32{0, 0} },
		"runtime out of range": func(g *provisioningv1.GuideSelection) { g.EvidenceSourceIndexes = []uint32{1} },
	} {
		a := good()
		f(a.Guide)
		if err := provisioning.Executable(finalized(a)); !errors.Is(err, provisioning.ErrNotActionable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	empty := good()
	empty.Guide = &provisioningv1.GuideSelection{}
	if err := provisioning.Executable(finalized(empty)); !errors.Is(err, provisioning.ErrNotActionable) {
		t.Errorf("empty selection: %v", err)
	}
	// 소스 근거만 있는 조치는 런타임 순번을 가질 수 없다.
	so := sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 0))
	so.Guide = &provisioningv1.GuideSelection{GuideId: sel.GuideId, Revision: "1", BodySha256: sel.BodySha256, ManifestSha256: sel.ManifestSha256,
		Purpose: sel.Purpose, EvidenceSourceIndexes: []uint32{0}}
	if err := provisioning.Executable(finalized(so)); !errors.Is(err, provisioning.ErrNotActionable) {
		t.Errorf("source-only with a runtime index: %v", err)
	}
}

type fakeHist map[string]*history.Snapshot

func (f fakeHist) ByID(id string) (*history.Snapshot, error) { return f[id], nil }
func (f fakeHist) ByContentHashV1(string, string, string) (*history.Snapshot, error) {
	return nil, nil
}

func TestCheckGuides(t *testing.T) {
	lk, in := seFixture(t)
	dir, sel := catalog(t, nil)
	withGuide := func(a *provisioningv1.RemediationAction, ev, src []uint32) *provisioningv1.RemediationAction {
		g := proto.Clone(sel).(*provisioningv1.GuideSelection)
		g.EvidenceSourceIndexes, g.SourceEvidenceIndexes = ev, src
		a.Guide = g
		return a
	}
	runtimeRef := []*provisioningv1.ActionEvidenceSource{{FindingId: "f1",
		Snapshot: &provisioningv1.SnapshotReference{SourceNodeId: "n1", Reference: &provisioningv1.SnapshotReference_SnapshotId{SnapshotId: "s1"}}}}
	hist := fakeHist{"s1": {ID: "s1", NodeID: "n1", Findings: []*discoveryv1.Finding{{Id: "f1"}}}}

	mixed := withGuide(runtimeAction(ref(in, 0, 0)), []uint32{0}, []uint32{0})
	mixed.EvidenceSources = runtimeRef
	so := withGuide(sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 1)), nil, []uint32{0})
	p := finalized(mixed, so)
	src, err := provisioning.CheckSourceEvidence(lk, p)
	if err != nil {
		t.Fatal(err)
	}
	gc, err := provisioning.CheckGuides(p, dir, src, hist)
	if err != nil || !gc.Of(0).Resolved || !gc.Of(1).Resolved {
		t.Fatalf("resolved: %v %+v %+v", err, gc.Of(0), gc.Of(1))
	}
	if ch := strings.Join(gc.Of(0).Chosen, "|"); !strings.Contains(ch, "[source 0] RSA") || !strings.Contains(ch, "[runtime 0] finding f1 in snapshot s1") {
		t.Fatalf("chosen: %s", ch)
	}

	// 고른 런타임 근거를 못 찾음 / 이력 없음 → 혼합 조치는 가이드만 빠진다.
	for name, h := range map[string]history.SnapshotLookup{"missing snapshot": fakeHist{}, "no history": nil} {
		gc, err := provisioning.CheckGuides(p, dir, src, h)
		if err != nil || gc.Of(0).Resolved || !strings.Contains(gc.Of(0).Reason, "[runtime 0]") || len(gc.Incomplete(p)) != 1 {
			t.Errorf("%s: %v %+v", name, err, gc.Of(0))
		}
	}
	// 고른 소스 근거가 미해결이면 그 가이드도 쓰지 않는다.
	bad := ref(in, 0, 0)
	bad.ItemId = "gone"
	mixed2 := withGuide(runtimeAction(bad), nil, []uint32{0})
	p2 := finalized(mixed2)
	src2, _ := provisioning.CheckSourceEvidence(lk, p2)
	if gc, err := provisioning.CheckGuides(p2, dir, src2, nil); err != nil || gc.Of(0).Resolved || !strings.Contains(gc.Of(0).Reason, "[source 0]) did not resolve") {
		t.Errorf("unresolved chosen source: %v %+v", err, gc.Of(0))
	}
	// 소스 근거만 있는 조치의 가이드가 틀리면 계획 전체 거부.
	so2 := withGuide(sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 1)), nil, nil)
	so2.Guide.BodySha256 = strings.Repeat("0", 64)
	p3 := finalized(so2)
	src3, _ := provisioning.CheckSourceEvidence(lk, p3)
	if _, err := provisioning.CheckGuides(p3, dir, src3, nil); !errors.Is(err, provisioning.ErrGuideUnresolved) {
		t.Errorf("source-only bad guide: %v", err)
	}
	if _, err := provisioning.CheckGuides(finalized(withGuide(sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 1)), nil, nil)), "", src3, nil); !errors.Is(err, provisioning.ErrGuideUnresolved) {
		t.Errorf("source-only without a catalog: %v", err)
	}
}

// 승인 서명을 검증한 실행에서만 「approved guide」로 적는다.
func TestGuideNotesSayApprovedOnlyWhenVerified(t *testing.T) {
	dir, sel := catalog(t, nil)
	a := runtimeAction()
	a.Guide = proto.Clone(sel).(*provisioningv1.GuideSelection)
	p := finalized(a)
	gc, err := provisioning.CheckGuides(p, dir, nil, nil)
	if err != nil || !gc.Of(0).Resolved {
		t.Fatalf("%v %+v", err, gc.Of(0))
	}
	level := provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL
	verified := provisioning.GenerateProvisioningPlaybookWithSource(p, level, gc.Notes(true))
	unverified := provisioning.GenerateProvisioningPlaybookWithSource(p, level, gc.Notes(false))
	if !strings.Contains(verified, "# action r1 approved guide: pqcota-guide/doc-key, revision 1 — Document key transport with ML-KEM") ||
		!strings.Contains(verified, "observation limit: runtime sees BC registered") || !strings.Contains(verified, "a general guide for the confirmed purpose") {
		t.Fatalf("verified run\n%s", verified)
	}
	if strings.Contains(unverified, "approved guide") || !strings.Contains(unverified, "guide named in the plan (approval signatures NOT verified") {
		t.Fatalf("unverified run says approved\n%s", unverified)
	}
	if r := gc.Of(0).Proto(false); r.GetApprovalsVerified() || !r.GetResolved() || r.GetBodySha256Found() != sel.BodySha256 {
		t.Fatalf("record %+v", r)
	}
}

func TestReviewShowsTheVerifiedBody(t *testing.T) {
	dir, sel := catalog(t, nil)
	a := runtimeAction()
	a.Guide = proto.Clone(sel).(*provisioningv1.GuideSelection)
	p := finalized(a)
	gc, _ := provisioning.CheckGuides(p, dir, nil, nil)
	sum, bodies := provisioning.ReviewSummary(p, nil, gc), provisioning.ReviewBodies(gc)
	for _, want := range []string{
		"guide: pqcota-guide/doc-key revision 1 — Document key transport with ML-KEM",
		"sha256 " + sel.BodySha256 + " — matches the plan",
		"manifest sha256 " + sel.ManifestSha256,
		"example: Example.java sha256",
		"purpose confirmed by plan author: document-key-transport",
		"condition confirmed by plan author: JDK 21",
		"rationale: wraps keys with RSA-OAEP",
		"observation limit: runtime sees BC registered",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q\n%s", want, sum)
		}
	}
	if !strings.Contains(bodies, "Step 1: replace RSA-OAEP key wrapping.\\u001b[31m") || strings.Contains(bodies, "\x1b") ||
		!strings.Contains(bodies, "===== guide body: pqcota-guide/doc-key revision 1") {
		t.Fatalf("bodies\n%s", bodies)
	}
}
