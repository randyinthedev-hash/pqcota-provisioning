// SPDX-FileCopyrightText: 2026 randyinthedev
// SPDX-License-Identifier: Apache-2.0

package provisioning

import (
	"sort"

	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
)

// CaptureState — 앱의 현재 크립토 상태(롤백 before 기준)를 findings에서 요약한다(프로비저닝 설계 §6A).
// openssl은 lib@version, JCA는 provider 목록을 모듈로. 프로비저닝 *전* 이걸 보존해 롤백 근거로 삼는다.
func CaptureState(findings []*discoveryv1.Finding) *provisioningv1.CryptoState {
	seen := map[string]bool{}
	var modules, chain []string
	for _, f := range findings {
		if o := f.GetOpenssl(); o != nil {
			m := o.GetLib()
			if v := o.GetVersion(); v != "" {
				m += "@" + v
			}
			if m != "" && !seen[m] {
				seen[m] = true
				modules = append(modules, m)
			}
		}
		if j := f.GetJca(); j != nil {
			for _, p := range j.GetProviderSet() {
				chain = append(chain, p)
				k := "jca:" + p
				if !seen[k] {
					seen[k] = true
					modules = append(modules, k)
				}
			}
		}
	}
	sort.Strings(modules)
	return &provisioningv1.CryptoState{Modules: modules, ProviderChain: chain}
}

// NewProvisioningRecord — 조치 실행 전 before 상태를 캡처해 append-only 히스토리 레코드를 만든다(프로비저닝 설계 §6A 롤백).
// appKeys — 이 조치가 영향을 주는 앱(들). 공유 라이브러리면 다중(cbom Finding.app_keys 유래).
func NewProvisioningRecord(id, nodeID string, appKeys []string, planID string, action *provisioningv1.RemediationAction, beforeFindings []*discoveryv1.Finding) *provisioningv1.ProvisioningRecord {
	return &provisioningv1.ProvisioningRecord{
		Id:       id,
		NodeId:   nodeID,
		AppKeys:  appKeys,
		PlanId:   planID,
		ActionId: action.GetId(),
		Before:   CaptureState(beforeFindings), // 롤백 기준
		Status:   provisioningv1.ProvisioningStatus_PROVISIONING_STATUS_STAGED,
	}
}
