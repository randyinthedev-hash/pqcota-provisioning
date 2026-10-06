// SPDX-FileCopyrightText: 2026 randyinthedev
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"strings"
	"testing"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// L2 롤백: forward가 배치한 config 조각 + 스테이지한 모듈을 제거(state: absent). 재시작은 하지 않는다 — 그것은 L3.
func TestRollbackPlaybookL2(t *testing.T) {
	pb := provisioning.GenerateRollbackPlaybook(samplePlan(), provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	for _, want := range []string{
		`hosts: ["web-01"]`, `hosts: ["app-01"]`,
		"state: absent",        // 제거(역방향)
		"/opt/pqcota/BC.jar",   // a2 주입 모듈 제거
		"java.security.pqcota", // a2 JCA config 조각 제거
		"openssl-pqc.cnf",      // a1 OpenSSL config 조각 제거
		"a3 (REMEDIATION_KIND_FORK_REPLACE): it was never delivered through config", // 비-config는 수동
	} {
		if !strings.Contains(pb, want) {
			t.Errorf("the L2 rollback playbook does not contain %q:\n%s", want, pb)
		}
	}
	if strings.Contains(pb, "④ restart") {
		t.Errorf("a rollback must not restart (restart is the L3 restart hook):\n%s", pb)
	}
	if strings.Contains(pb, "ansible.builtin.copy") {
		t.Errorf("a rollback must remove (absent), not place (copy):\n%s", pb)
	}
}

// L1 롤백: 스테이지한 모듈만 제거 — config 조각은 L1에서 배치 안 했으니 제거도 없음.
func TestRollbackPlaybookL1(t *testing.T) {
	pb := provisioning.GenerateRollbackPlaybook(samplePlan(), provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	if !strings.Contains(pb, "/opt/pqcota/BC.jar") {
		t.Error("an L1 rollback must also remove the staged module")
	}
	if strings.Contains(pb, "remove the config fragment") {
		t.Errorf("L1 (stage-only) placed no config, so it must not remove one either:\n%s", pb)
	}
}
