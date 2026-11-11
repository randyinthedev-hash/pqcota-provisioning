// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"strings"
	"testing"
)

// 소스 근거만 있는 조치(APP_RECONFIG). 런타임 조치 a1 뒤에 있다 — 앞 조치의 산출물도 나오면 안 된다.
const planSourceOnly = `{
  "id": "t-src", "status": "PLAN_STATUS_FINALIZED", "scope": "ring-0",
  "approvalSignatures": ["reviewer:test"],
  "derivedFromSnapshotId": "snap-1", "rulesetVersion": "rs-1",
  "finalizedAt": "2026-09-10T00:00:00Z",
  "actions": [{"id":"a1","targetNodeId":"n1","findingId":"f1",
    "cryptoRuntime":"CRYPTO_RUNTIME_OPENSSL","kind":"REMEDIATION_KIND_CONFIG_ONLY",
    "targetAlgorithm":"ML-KEM (FIPS 203)",
    "automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL"},
   {"id":"a2","targetNodeId":"n1","kind":"REMEDIATION_KIND_APP_RECONFIG",
    "automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL",
    "sourceEvidence":[{"interpretationId":"INTERP","itemId":"ITEM","inputSha256":"SHA"}]}]
}`

func srcPlan(interp, item, sha string) string {
	return strings.NewReplacer("INTERP", interp, "ITEM", item, "SHA", sha).Replace(planSourceOnly)
}

// --dsn 없이 소스 근거만 있는 조치를 생성하면 해석할 수 없으므로 거부한다 — stdout은 비어 있다. 롤백도 같다.
func TestSourceOnlyWithoutDSNIsRefusedBeforeOutput(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, srcPlan("i", "t", strings.Repeat("a", 64)))
	for _, extra := range [][]string{nil, {"--rollback"}} {
		args := append([]string{"--level", "l2", unverified}, extra...)
		out, errOut, code := runProvision(t, bin, envWith(), append(args, plan)...)
		if code != 1 || out != "" || !strings.Contains(errOut, "based only on source evidence has a reference that does not resolve") ||
			!strings.Contains(errOut, "not looked up") {
			t.Errorf("%v: exit %d, stdout %d bytes\n%s", extra, code, len(out), errOut)
		}
	}
}

// 소스 근거만 있는 조치가 런타임을 바꾸는 종류면 Executable이 거부한다(저장소를 열기 전).
func TestSourceOnlyRuntimeKindIsRefused(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, strings.Replace(srcPlan("i", "t", strings.Repeat("a", 64)), `"kind":"REMEDIATION_KIND_APP_RECONFIG"`, `"kind":"REMEDIATION_KIND_PROVIDER_INJECT"`, 1))
	out, errOut, code := runProvision(t, bin, envWith(), "--level", "l2", unverified, plan)
	if code != 1 || out != "" || !strings.Contains(errOut, "needs runtime evidence") {
		t.Errorf("exit %d, stdout %d bytes\n%s", code, len(out), errOut)
	}
}

// 승인 검사가 먼저다 — 승인에서 거부될 계획으로는 저장소를 보지 않는다(소스 근거 사유가 나오지 않는다).
func TestApprovalCheckComesBeforeSourceEvidence(t *testing.T) {
	bin := buildCLI(t)
	plan := writePlan(t, srcPlan("i", "t", strings.Repeat("a", 64)))
	out, errOut, code := runProvision(t, bin, envWith(), "--level", "l2", plan) // 승인 확인 경로를 연다(키 없음)
	if code != 1 || out != "" || strings.Contains(errOut, "source evidence") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
}
