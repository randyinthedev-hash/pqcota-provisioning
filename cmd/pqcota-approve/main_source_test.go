// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence"
	sestore "github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/store"
)

const judgedSource = `{"id":"p-src","status":"PLAN_STATUS_IN_REVIEW","scope":"s",
 "actions":[{"id":"a1","targetNodeId":"web-01","kind":"REMEDIATION_KIND_APP_RECONFIG",
  "sourceEvidence":[{"interpretationId":"INTERP","itemId":"ITEM","inputSha256":"SHA"}]}]}`

func runApprove(t *testing.T, bin, priv, plan string, extra ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, append(append([]string{"--approver", "reviewer-1"}, extra...), plan)...)
	cmd.Env = append(os.Environ(), "PQCOTA_APPROVAL_KEY="+priv, "PQCOTA_ORG="+os.Getenv("PQCOTA_ORG_FOR_TEST"))
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	return so.String(), se.String(), err
}

// 소스 근거만 있는 조치는 서명 전에 해석해야 한다. 저장소가 없으면 해석할 수 없으므로 서명하지 않는다.
func TestApproveRefusesSourceOnlyWithoutStore(t *testing.T) {
	_, priv, _ := sign.Generate()
	bin := buildCLI(t)
	plan := writePlan(t, strings.NewReplacer("INTERP", "i", "ITEM", "t", "SHA", strings.Repeat("a", 64)).Replace(judgedSource))
	out, errOut, err := runApprove(t, bin, priv, plan)
	if err == nil || out != "" || !strings.Contains(errOut, "give --dsn") {
		t.Fatalf("err=%v stdout %d bytes\n%s", err, len(out), errOut)
	}
	// 런타임을 바꾸는 종류면 저장소와 상관없이 승인하지 않는다.
	bad := writePlan(t, strings.Replace(strings.NewReplacer("INTERP", "i", "ITEM", "t", "SHA", strings.Repeat("a", 64)).Replace(judgedSource),
		"REMEDIATION_KIND_APP_RECONFIG", "REMEDIATION_KIND_CONFIG_ONLY", 1))
	if out, errOut, err := runApprove(t, bin, priv, bad); err == nil || out != "" || !strings.Contains(errOut, "needs runtime evidence") {
		t.Fatalf("runtime kind: err=%v\n%s", err, errOut)
	}
}

func TestApproveResolvesSourceEvidenceWithStore(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	orgName := "apv-src-" + stamp
	t.Setenv("PQCOTA_ORG_FOR_TEST", orgName)
	raw := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","serialNumber":"urn:uuid:` + stamp + `","components":[
	 {"type":"cryptographic-asset","name":"RSA","evidence":{"occurrences":[{"location":"a.java","line":1}]}}]}`)
	in, err := sourceevidence.Convert(raw, sourceevidence.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sestore.NewPgStoreIn(context.Background(), dsn, orgName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(sestore.Input{Raw: raw}, in); err != nil {
		t.Fatal(err)
	}
	st.Close()

	pub, priv, _ := sign.Generate()
	bin := buildCLI(t)
	good := writePlan(t, strings.NewReplacer("INTERP", in.ID, "ITEM", in.Items[0].ID, "SHA", in.InputSHA256).Replace(judgedSource))
	out, errOut, err := runApprove(t, bin, priv, good, "--dsn", dsn)
	if err != nil || !strings.Contains(out, `"PLAN_STATUS_FINALIZED"`) {
		t.Fatalf("resolvable: err=%v\n%s", err, errOut)
	}
	_ = pub
	missing := writePlan(t, strings.NewReplacer("INTERP", in.ID, "ITEM", "gone", "SHA", in.InputSHA256).Replace(judgedSource))
	if out, errOut, err := runApprove(t, bin, priv, missing, "--dsn", dsn); err == nil || out != "" || !strings.Contains(errOut, "has no item gone") {
		t.Fatalf("unresolvable: err=%v\n%s", err, errOut)
	}
}
