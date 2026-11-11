// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
)

// 같은 id의 두 조치는 승인하지 않는다(--review도 서명할 수 없다고 한다).
func TestApproveRefusesDuplicateActionIDs(t *testing.T) {
	_, priv, _ := sign.Generate()
	bin := buildCLI(t)
	plan := writePlan(t, `{"id":"p","status":"PLAN_STATUS_IN_REVIEW","scope":"s","actions":[
	 {"id":"a1","targetNodeId":"n1","kind":"REMEDIATION_KIND_CONFIG_ONLY"},{"id":"a1","targetNodeId":"n2","kind":"REMEDIATION_KIND_CONFIG_ONLY"}]}`)
	for _, mode := range [][]string{{"--approver", "reviewer-1"}, {"--review"}} {
		out, errOut, code := approveArgs(t, bin, priv, append(mode, plan)...)
		if code != 1 || out != "" || !strings.Contains(errOut, `share the id "a1"`) {
			t.Errorf("%v: exit %d\n%s\n%s", mode, code, out, errOut)
		}
	}
}
