// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"google.golang.org/protobuf/encoding/protojson"
)

func hexsum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// guideCatalog — <dir>/doc-key/1/{guide.json, README.md}. 돌려주는 JSON 조각을 조치의 "guide"에 넣는다.
func guideCatalog(t *testing.T) (dir, body, manifest string) {
	t.Helper()
	dir = t.TempDir()
	rev := filepath.Join(dir, "doc-key", "1")
	os.MkdirAll(rev, 0o755)
	b := []byte("# Document key transport\nStep 1.\n")
	m := []byte(`{"id":"pqcota-guide/doc-key","revision":"1","title":"Document key transport","purposes":["document-key-transport"],"observationLimits":["limit-1"],"body":"README.md","examples":[]}`)
	os.WriteFile(filepath.Join(rev, "README.md"), b, 0o644)
	os.WriteFile(filepath.Join(rev, "guide.json"), m, 0o644)
	return dir, hexsum(b), hexsum(m)
}

func guideJSON(body, manifest, extra string) string {
	return `"guide":{"guideId":"pqcota-guide/doc-key","revision":"1","bodySha256":"` + body + `","manifestSha256":"` + manifest +
		`","purpose":"document-key-transport"` + extra + `}`
}

// planWithGuide — planOK의 a1에 가이드를 붙인다(일반 가이드 또는 extra로 순번).
func planWithGuide(body, manifest, extra string) string {
	return strings.Replace(planOK, `"targetAlgorithm":"ML-KEM (FIPS 203)",`, `"targetAlgorithm":"ML-KEM (FIPS 203)",`+guideJSON(body, manifest, extra)+`,`, 1)
}

func signPlanJSON(t *testing.T, body, priv string) string {
	t.Helper()
	p := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal([]byte(body), p); err != nil {
		t.Fatal(err)
	}
	p.ApprovalSignatures = nil
	sig, err := sign.SignApproval(priv, "reviewer-1", p)
	if err != nil {
		t.Fatal(err)
	}
	p.ApprovalSignatures = []string{sig}
	out, _ := protojson.Marshal(p)
	return writePlan(t, string(out))
}

// 승인 서명을 검증한 실행에서만 「approved guide」. --allow-unverified-approvals로는 매니페스트·본문이 맞아도 그렇게 쓰지 않는다.
func TestGuideIsApprovedOnlyWhenApprovalsAreVerified(t *testing.T) {
	bin := buildCLI(t)
	dir, body, man := guideCatalog(t)
	pub, priv, _ := sign.Generate()
	signed := signPlanJSON(t, planWithGuide(body, man, ""), priv)

	out, errOut, code := runProvision(t, bin, envWith("PQCOTA_APPROVAL_KEYS=reviewer-1="+pub), "--level", "l2", "--guide-dir", dir, signed)
	if code != 0 || !strings.Contains(out, "# action a1 approved guide: pqcota-guide/doc-key, revision 1 — Document key transport") ||
		!strings.Contains(out, "observation limit: limit-1") {
		t.Fatalf("verified: exit %d\n%s\n%s", code, out, errOut)
	}
	out, errOut, code = runProvision(t, bin, envWith(), "--level", "l2", unverified, "--guide-dir", dir, signed)
	if code != 0 || strings.Contains(out, "approved guide") || !strings.Contains(out, "guide named in the plan (approval signatures NOT verified") {
		t.Fatalf("unverified: exit %d\n%s\n%s", code, out, errOut)
	}
}

// 소스 근거만 있는 조치가 아닌 조치의 가이드가 틀리면 가이드만 빠지고 불완전(종료 3). config 조각은 그대로다.
func TestUnresolvedGuideOnARuntimeActionIsLeftOut(t *testing.T) {
	bin := buildCLI(t)
	dir, body, man := guideCatalog(t)
	runtimeRef := `,"evidenceSourceIndexes":[0]`
	withRef := strings.Replace(planWithGuide(body, man, runtimeRef), `"automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL"}`,
		`"automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL","evidenceSources":[{"findingId":"f1","snapshot":{"sourceNodeId":"n1","snapshotId":"s1"}}]}`, 1)
	for _, c := range []struct {
		name, plan, dir, want string
	}{
		{"body hash", planWithGuide(strings.Repeat("0", 64), man, ""), dir, "body hash differs"},
		{"no catalog", planWithGuide(body, man, ""), "", "no guide catalog"},
		{"chosen runtime evidence without --dsn", withRef, dir, "[runtime 0]) was not looked up"},
	} {
		args := []string{"--level", "l2", unverified}
		if c.dir != "" {
			args = append(args, "--guide-dir", c.dir)
		}
		out, errOut, code := runProvision(t, bin, envWith(), append(args, writePlan(t, c.plan))...)
		if code != 3 || !strings.Contains(out, "place the config fragment") || !strings.Contains(out, "guide not given") ||
			strings.Contains(out, "guide named in the plan") || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d\n%s\n%s", c.name, code, out, errOut)
		}
	}
}

// 모양이 틀린 가이드는 실행 근거가 아니다(카탈로그를 보기 전).
func TestMalformedGuideIsRefused(t *testing.T) {
	bin := buildCLI(t)
	out, errOut, code := runProvision(t, bin, envWith(), "--level", "l2", unverified, writePlan(t, planWithGuide("abc", strings.Repeat("0", 64), "")))
	if code != 1 || out != "" || !strings.Contains(errOut, "body_sha256 must be 64 lowercase hex") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
}
