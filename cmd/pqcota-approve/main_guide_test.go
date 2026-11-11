// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
)

func hexsum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func approveCatalog(t *testing.T) (dir, body, manifest string) {
	t.Helper()
	dir = t.TempDir()
	rev := filepath.Join(dir, "doc-key", "1")
	os.MkdirAll(rev, 0o755)
	b := []byte("# Document key transport\nStep 1: wrap the key with ML-KEM.\n")
	m := []byte(`{"id":"pqcota-guide/doc-key","revision":"1","title":"Document key transport","purposes":["document-key-transport"],"observationLimits":["limit-1"],"body":"README.md","examples":[]}`)
	os.WriteFile(filepath.Join(rev, "README.md"), b, 0o644)
	os.WriteFile(filepath.Join(rev, "guide.json"), m, 0o644)
	return dir, hexsum(b), hexsum(m)
}

func judgedWithGuide(body, manifest string) string {
	return strings.Replace(judged, `"kind":"REMEDIATION_KIND_CONFIG_ONLY"}`, `"kind":"REMEDIATION_KIND_CONFIG_ONLY","guide":{"guideId":"pqcota-guide/doc-key","revision":"1","bodySha256":"`+
		body+`","manifestSha256":"`+manifest+`","purpose":"document-key-transport","conditions":["JCA"]}}`, 1)
}

func approveArgs(t *testing.T, bin, priv string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "PQCOTA_APPROVAL_KEY="+priv)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return so.String(), se.String(), code
}

// --review는 서명하지 않고 볼 것과 검증한 본문 전문을 보인다. 서명은 같은 요약을 stderr에, 본문 전문은 내지 않는다.
func TestReviewAndApproveAGuide(t *testing.T) {
	_, priv, _ := sign.Generate()
	bin := buildCLI(t)
	dir, body, man := approveCatalog(t)
	plan := writePlan(t, judgedWithGuide(body, man))

	out, errOut, code := approveArgs(t, bin, priv, "--review", "--guide-dir", dir, plan)
	if code != 0 || !strings.Contains(out, "guide: pqcota-guide/doc-key revision 1 — Document key transport") ||
		!strings.Contains(out, "condition confirmed by plan author: JCA") || !strings.Contains(out, "observation limit: limit-1") ||
		!strings.Contains(out, "Step 1: wrap the key with ML-KEM.") || !strings.Contains(out, "status PLAN_STATUS_IN_REVIEW") ||
		!strings.Contains(errOut, "nothing was signed") || strings.Contains(out, "approvalSignatures") {
		t.Fatalf("review: exit %d\n%s\n%s", code, out, errOut)
	}
	out, errOut, code = approveArgs(t, bin, priv, "--approver", "reviewer-1", "--guide-dir", dir, plan)
	if code != 0 || !strings.Contains(out, `"PLAN_STATUS_FINALIZED"`) || !strings.Contains(errOut, "guide: pqcota-guide/doc-key revision 1") ||
		strings.Contains(errOut, "Step 1: wrap the key") {
		t.Fatalf("approve: exit %d\n%s\n%s", code, out, errOut)
	}
}

// 볼 수 없는 가이드에는 서명하지 않는다 — 카탈로그 없음, 본문 해시 다름.
func TestApproveRefusesAGuideItCannotShow(t *testing.T) {
	_, priv, _ := sign.Generate()
	bin := buildCLI(t)
	dir, body, man := approveCatalog(t)
	for name, c := range map[string]struct {
		plan string
		args []string
		want string
	}{
		"no catalog": {judgedWithGuide(body, man), nil, "no guide catalog"},
		"body hash":  {judgedWithGuide(strings.Repeat("0", 64), man), []string{"--guide-dir", dir}, "body hash differs"},
	} {
		p := writePlan(t, c.plan)
		for _, mode := range [][]string{{"--review"}, {"--approver", "reviewer-1"}} {
			args := append(append(append([]string{}, mode...), c.args...), p)
			out, errOut, code := approveArgs(t, bin, priv, args...)
			if code != 1 || !strings.Contains(errOut, c.want) || strings.Contains(out, "approvalSignatures") {
				t.Errorf("%s %v: exit %d\n%s\n%s", name, mode, code, out, errOut)
			}
		}
	}
}
