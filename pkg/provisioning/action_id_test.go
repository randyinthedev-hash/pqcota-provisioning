// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// 조치 id는 비어 있지 않고 계획 전체에서 고유해야 한다(v0.11.0). 같은 id의 두 조치에서 둘째 조치의 config가 알리지 않고 빠졌다
// (pqcota-test-reports/2026-duplicate-action-id/). 노드가 달라도 기록 id(plan:action)가 겹치므로 거부한다.
func TestActionIDsMustBeUniqueAndPresent(t *testing.T) {
	act := func(id, node string) *provisioningv1.RemediationAction {
		return &provisioningv1.RemediationAction{Id: id, TargetNodeId: node, Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY}
	}
	cases := []struct {
		name    string
		actions []*provisioningv1.RemediationAction
		want    []string
	}{
		{"empty id", []*provisioningv1.RemediationAction{act("a1", "n1"), act("", "n2")}, []string{"action[1]", "has no id"}},
		{"duplicate on one node", []*provisioningv1.RemediationAction{act("dup", "n1"), act("a2", "n1"), act("dup", "n1")}, []string{"action[0] and action[2]", `"dup"`, "nodes n1, n1"}},
		{"duplicate across nodes", []*provisioningv1.RemediationAction{act("dup", "n1"), act("dup", "n2")}, []string{"action[0] and action[1]", `"dup"`, "nodes n1, n2"}},
	}
	for _, c := range cases {
		p := &provisioningv1.FinalizedPlan{Status: provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED, ApprovalSignatures: []string{"x"}, Actions: c.actions}
		err := provisioning.Executable(p)
		if !errors.Is(err, provisioning.ErrNotActionable) {
			t.Errorf("%s: Executable %v", c.name, err)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error lacks %q: %v", c.name, w, err)
			}
		}
		// 승인 전에도 거부한다.
		p.Status, p.ApprovalSignatures = provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW, nil
		if err := provisioning.PrepareApproval(p, time.Unix(1700000000, 0)); !errors.Is(err, provisioning.ErrNotApprovable) || !strings.Contains(err.Error(), c.want[0]) {
			t.Errorf("%s: PrepareApproval %v", c.name, err)
		}
		if p.GetStatus() != provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW || p.GetFinalizedAt() != nil {
			t.Errorf("%s: a refused approval changed the plan", c.name)
		}
	}
	// 고유한 id는 지금과 같이 받는다.
	ok := &provisioningv1.FinalizedPlan{Status: provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED, ApprovalSignatures: []string{"x"},
		Actions: []*provisioningv1.RemediationAction{act("a1", "n1"), act("a2", "n1"), act("b1", "n2")}}
	if err := provisioning.Executable(ok); err != nil {
		t.Fatalf("unique ids: %v", err)
	}
}
