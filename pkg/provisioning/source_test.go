// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"gopkg.in/yaml.v3"
)

// 경로에 개행과 ESC가 든 위치 — 플레이북 주석을 깨지 않아야 한다.
const seCBOM = `{"bomFormat":"CycloneDX","specVersion":"1.6","components":[
 {"type":"cryptographic-asset","name":"RSA","cryptoProperties":{"assetType":"algorithm"},
  "evidence":{"occurrences":[{"location":"src/Wrap.java","line":12},{"location":"src/evil\n- hosts: all\u001b.java","line":3}]}},
 {"type":"cryptographic-asset","name":"SHA-256","cryptoProperties":{"assetType":"algorithm"}}]}`

func timeNow() time.Time { return time.Unix(1700000000, 0).UTC() }

type fakeLookup map[string]*sourceevidence.Interpretation

func (f fakeLookup) Interpretation(id string) (*sourceevidence.Interpretation, error) {
	return f[id], nil
}

func seFixture(t *testing.T) (fakeLookup, *sourceevidence.Interpretation) {
	t.Helper()
	in, err := sourceevidence.Convert([]byte(seCBOM), sourceevidence.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return fakeLookup{in.ID: in}, in
}

func ref(in *sourceevidence.Interpretation, item int, loc int) *provisioningv1.SourceEvidenceReference {
	r := &provisioningv1.SourceEvidenceReference{InterpretationId: in.ID, ItemId: in.Items[item].ID, InputSha256: in.InputSHA256}
	if loc >= 0 {
		r.LocationId = in.Items[item].Locations[loc].ID
	}
	return r
}

func finalized(actions ...*provisioningv1.RemediationAction) *provisioningv1.FinalizedPlan {
	return &provisioningv1.FinalizedPlan{Id: "p", Status: provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"x"}, RulesetVersion: "r", Actions: actions}
}

func sourceOnly(kind provisioningv1.RemediationKind, refs ...*provisioningv1.SourceEvidenceReference) *provisioningv1.RemediationAction {
	return &provisioningv1.RemediationAction{Id: "s1", TargetNodeId: "n1", Kind: kind, SourceEvidence: refs,
		AutomationLevel: provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL}
}

func runtimeAction(refs ...*provisioningv1.SourceEvidenceReference) *provisioningv1.RemediationAction {
	return &provisioningv1.RemediationAction{Id: "r1", TargetNodeId: "n1", FindingId: "f1",
		CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
		TargetAlgorithm: "ML-KEM (FIPS 203)", AutomationLevel: provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL,
		SourceEvidence: refs}
}

// 소스 근거만 있는 조치는 APP_RECONFIG·REBUILD만. 런타임을 바꾸는 종류는 승인도 실행도 거부한다.
func TestSourceOnlyKinds(t *testing.T) {
	_, in := seFixture(t)
	ok := []provisioningv1.RemediationKind{provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, provisioningv1.RemediationKind_REMEDIATION_KIND_REBUILD}
	for _, k := range ok {
		if err := provisioning.Executable(finalized(sourceOnly(k, ref(in, 0, -1)))); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	for _, k := range []provisioningv1.RemediationKind{
		provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY, provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT,
		provisioningv1.RemediationKind_REMEDIATION_KIND_FORK_REPLACE, provisioningv1.RemediationKind_REMEDIATION_KIND_PROXY_FRONT,
		provisioningv1.RemediationKind_REMEDIATION_KIND_JDK_UPGRADE, provisioningv1.RemediationKind_REMEDIATION_KIND_DECOMMISSION,
	} {
		p := finalized(sourceOnly(k, ref(in, 0, -1)))
		if err := provisioning.Executable(p); !errors.Is(err, provisioning.ErrNotActionable) {
			t.Errorf("%s: Executable %v", k, err)
		}
		p.Status, p.ApprovalSignatures = provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW, nil
		if err := provisioning.PrepareApproval(p, timeNow()); !errors.Is(err, provisioning.ErrNotApprovable) {
			t.Errorf("%s: PrepareApproval %v", k, err)
		}
	}
	// 런타임 근거와 함께면 어느 종류든 받는다.
	if err := provisioning.Executable(finalized(runtimeAction(ref(in, 0, 0)))); err != nil {
		t.Errorf("mixed: %v", err)
	}
}

func TestResolveSourceEvidence(t *testing.T) {
	lk, in := seFixture(t)
	upper := ref(in, 0, -1)
	upper.InputSha256 = strings.ToUpper(upper.InputSha256)
	other := ref(in, 0, -1)
	other.InputSha256 = strings.Repeat("0", 64)
	noItem := ref(in, 0, -1)
	noItem.ItemId = "nope"
	noLoc := ref(in, 1, -1)
	noLoc.LocationId = in.Items[0].Locations[0].ID // 다른 항목의 위치
	noInterp := ref(in, 0, -1)
	noInterp.InterpretationId = "nope"
	cases := []struct {
		name string
		r    *provisioningv1.SourceEvidenceReference
		ok   bool
		want string
	}{
		{"whole item", ref(in, 0, -1), true, "whole item, 2 reported location(s)"},
		{"one location", ref(in, 0, 0), true, "reported location src/Wrap.java:12"},
		{"empty ids", &provisioningv1.SourceEvidenceReference{InputSha256: in.InputSHA256}, false, "invalid source evidence reference: source_evidence[0] needs"},
		{"uppercase hash", upper, false, "64 lowercase hex"},
		{"other input", other, false, "was made from input"},
		{"no item", noItem, false, "has no item"},
		{"location of another item", noLoc, false, "has no location"},
		{"no interpretation", noInterp, false, "no interpretation nope"},
	}
	for _, c := range cases {
		got := provisioning.ResolveSourceEvidence(lk, &provisioningv1.RemediationAction{SourceEvidence: []*provisioningv1.SourceEvidenceReference{c.r}})
		text := got[0].Reason + strings.Join(got[0].Lines, "|")
		if got[0].Resolved != c.ok || !strings.Contains(text, c.want) {
			t.Errorf("%s: resolved=%v %q, want %v %q", c.name, got[0].Resolved, text, c.ok, c.want)
		}
	}
	dup := provisioning.ResolveSourceEvidence(lk, &provisioningv1.RemediationAction{SourceEvidence: []*provisioningv1.SourceEvidenceReference{ref(in, 0, 0), ref(in, 0, 0)}})
	if !dup[0].Resolved || dup[1].Resolved || !strings.Contains(dup[1].Reason, "repeats") {
		t.Errorf("duplicate: %+v", dup)
	}
	if r := provisioning.ResolveSourceEvidence(nil, &provisioningv1.RemediationAction{SourceEvidence: []*provisioningv1.SourceEvidenceReference{ref(in, 0, 0)}}); r[0].Resolved || !strings.Contains(r[0].Reason, "not looked up") {
		t.Errorf("no lookup: %+v", r)
	}
	if r := provisioning.ResolveSourceEvidence(provisioning.FailingSourceLookup{Err: errors.New("down")}, &provisioningv1.RemediationAction{SourceEvidence: []*provisioningv1.SourceEvidenceReference{ref(in, 0, 0)}}); r[0].Resolved || !strings.Contains(r[0].Reason, "down") {
		t.Errorf("failing lookup: %+v", r)
	}
}

// 소스 근거만 있는 조치가 미해결이면 계획 전체를 거부하고, 혼합 근거 조치의 미해결은 불완전으로만 남는다.
func TestCheckSourceEvidence(t *testing.T) {
	lk, in := seFixture(t)
	bad := ref(in, 0, -1)
	bad.ItemId = "gone"
	p := finalized(runtimeAction(bad), sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 0)))
	check, err := provisioning.CheckSourceEvidence(lk, p)
	if err != nil {
		t.Fatalf("mixed unresolved must not refuse: %v", err)
	}
	if inc := check.Incomplete(p); len(inc) != 1 || !strings.Contains(inc[0], "action r1") || !strings.Contains(inc[0], "No source location or guide") {
		t.Fatalf("incomplete %v", inc)
	}
	p.Actions[1].SourceEvidence = append(p.Actions[1].SourceEvidence, bad)
	if _, err := provisioning.CheckSourceEvidence(lk, p); !errors.Is(err, provisioning.ErrSourceEvidenceUnresolved) || !strings.Contains(err.Error(), "action s1") {
		t.Fatalf("source-only unresolved: %v", err)
	}
	if _, err := provisioning.CheckSourceEvidence(nil, finalized(sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 0)))); !errors.Is(err, provisioning.ErrSourceEvidenceUnresolved) {
		t.Fatalf("no store: %v", err)
	}
}

// 플레이북 주석: 찾은 위치는 보이고, 못 찾은 근거는 사유만. 입력의 개행·제어 문자가 YAML을 깨지 않는다.
func TestPlaybookSourceNotes(t *testing.T) {
	lk, in := seFixture(t)
	bad := ref(in, 1, -1)
	bad.InterpretationId = "gone"
	p := finalized(runtimeAction(ref(in, 0, -1), bad), sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, 1)))
	plain := provisioning.GenerateProvisioningPlaybook(p, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	if got := provisioning.GenerateProvisioningPlaybookWithSource(p, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL, nil); got != plain {
		t.Fatal("nil notes changed the playbook")
	}
	check, err := provisioning.CheckSourceEvidence(lk, p)
	if err != nil {
		t.Fatal(err)
	}
	out := provisioning.GenerateProvisioningPlaybookWithSource(p, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL, check.Notes())
	for _, want := range []string{
		"# action r1 source evidence (reported locations, for review",
		"# action r1   [0] RSA (/components/0) — whole item, 2 reported location(s)",
		"# action r1       reported location src/Wrap.java:12 (line base unknown)",
		"# action r1   [1] not resolved: no interpretation gone",
		`# action s1   [0] RSA (/components/0)`,
		`reported location src/evil\u000a- hosts: all\u001b.java:3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("playbook lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("raw ESC in the playbook")
	}
	var doc []map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("playbook is not valid YAML: %v", err)
	}
	if len(doc) != 1 {
		t.Fatalf("a source path added plays: %d", len(doc))
	}
}

// 소스 근거만 있는 조치는 finding이 없는 것이 맞다 — 「근거 없음」·「finding 없음」으로 경고하지 않는다.
func TestTraceabilityOfSourceOnlyAction(t *testing.T) {
	_, in := seFixture(t)
	p := finalized(sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, -1)))
	p.FinalizedAt = nil
	for _, w := range provisioning.TraceabilityWarnings(p) {
		if strings.Contains(w, "action s1") {
			t.Errorf("source-only action warned: %s", w)
		}
	}
	// 소스 근거도 런타임 근거도 없는 조치는 그대로 경고한다.
	p.Actions[0].SourceEvidence = nil
	n := 0
	for _, w := range provisioning.TraceabilityWarnings(p) {
		if strings.Contains(w, "action s1") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("an action with no evidence at all: %d warnings", n)
	}
}

// 소스 근거만 있는 조치는 계획 단위 스냅샷(호환 경로)으로 되짚지 않는다 — 런타임 근거가 없다.
func TestSourceOnlyActionHasNoSnapshotResolution(t *testing.T) {
	_, in := seFixture(t)
	p := finalized(sourceOnly(provisioningv1.RemediationKind_REMEDIATION_KIND_APP_RECONFIG, ref(in, 0, -1)))
	p.DerivedFromSnapshotId = "snap-1"
	if rs := provisioning.ResolveAction(nil, p, p.Actions[0]); len(rs) != 0 {
		t.Fatalf("a source-only action was resolved against the plan-level snapshot: %+v", rs)
	}
}
