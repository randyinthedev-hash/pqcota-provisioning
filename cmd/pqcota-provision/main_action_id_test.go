// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 같은 id의 두 조치(노드가 달라도) — 생성·되돌림 모두 출력 없이 거부한다. --dsn이면 기록도 남지 않는다.
func planDupIDs(id string) string {
	return `{"id":"` + id + `","status":"PLAN_STATUS_FINALIZED","scope":"s","approvalSignatures":["reviewer:test"],
 "rulesetVersion":"r","finalizedAt":"2026-09-10T00:00:00Z","derivedFromSnapshotId":"s1",
 "actions":[
  {"id":"a1","targetNodeId":"n1","findingId":"f1","cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY","targetAlgorithm":"ML-KEM (FIPS 203)","automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL"},
  {"id":"a1","targetNodeId":"n2","findingId":"f2","cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY","targetAlgorithm":"ML-DSA (FIPS 204)","automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL"}]}`
}

func TestDuplicateActionIDsAreRefusedBeforeOutput(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, planDupIDs("dup"))
	for _, extra := range [][]string{nil, {"--rollback"}} {
		args := append([]string{"--level", "l2", unverified}, extra...)
		out, errOut, code := runProvision(t, bin, envWith(), append(args, plan)...)
		if code != 1 || out != "" || !strings.Contains(errOut, `action[0] and action[1] share the id "a1" (nodes n1, n2)`) {
			t.Errorf("%v: exit %d, stdout %d bytes\n%s", extra, code, len(out), errOut)
		}
	}
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — the record half of this test needs Postgres")
	}
	orgName := "dupid-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if out, _, code := runProvision(t, bin, envWith("PQCOTA_ORG="+orgName), "--level", "l2", unverified, "--dsn", dsn, plan); code != 1 || out != "" {
		t.Fatalf("with --dsn: exit %d", code)
	}
	if n := countRecords(t, dsn, orgName); n != 0 {
		t.Fatalf("a refused plan left %d records", n)
	}
}
