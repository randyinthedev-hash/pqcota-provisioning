// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning

import (
	"fmt"
	"sort"
	"strings"

	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
)

// CaptureState — 앱의 현재 크립토 상태(롤백 before 기준)를 findings에서 요약한다(프로비저닝 설계 §6A).
// openssl은 lib@version, JCA는 provider 목록을 모듈로. 프로비저닝 *전* 이걸 보존해 롤백 근거로 삼는다.
//
// 넘기는 것은 **조치의 대상 finding**이다. 받은 finding을 모두 한 상태로 합치므로, 노드의 finding
// 전부를 넘기면 다른 앱의 모듈과 provider 체인이 섞인다(v0.10.6까지 pqcota-provision이 그랬다).
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
			if jcaBeforeGap(f) != "" {
				continue // 배포본 전체의 체인으로 기록할 수 없다. 사유는 BeforeGap이 낸다
			}
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

// BeforeGap — 이 finding들로 before를 채울 수 없는 사유. 채울 수 있으면 빈 문자열이다.
//
// JCA 배포본(v0.12.0)은 JVM 여럿을 변형으로 싣는다. 그 가운데 하나의 체인을 배포본 전체의 before로 적으면,
// 롤백 근거가 다른 JVM의 실제 상태와 다를 수 있다. 그래서 다음이면 체인을 기록하지 않고 사유를 남긴다.
//   - 수집기가 지정한 JVM만 관측했다(observation_scope selected-pid, pqcota-jvmscan --pid).
//   - 일부 변형의 체인을 보지 못했다(chain_observed false).
//   - 관측한 체인이 여럿이다.
//
// 체인을 아예 보지 못한 finding(provider_set도 instances도 없음)은 지금처럼 빈 before이고 사유를 달지 않는다.
// 변형 정보가 없는 옛 입력은 지금 규칙 그대로다.
func BeforeGap(findings []*discoveryv1.Finding) string {
	var out []string
	for _, f := range findings {
		if r := jcaBeforeGap(f); r != "" {
			out = append(out, r)
		}
	}
	return strings.Join(out, "; ")
}

func jcaBeforeGap(f *discoveryv1.Finding) string {
	j := f.GetJca()
	if j == nil || (len(j.GetInstances()) == 0 && j.GetObservationScope() == "") {
		return ""
	}
	name := f.GetId()
	if ak := f.GetAppKeys(); len(ak) > 0 {
		name = ak[0]
	}
	var jvms, unobservedJVMs uint32
	chains := map[string]bool{}
	for _, in := range j.GetInstances() {
		jvms += in.GetCount()
		if !in.GetChainObserved() {
			unobservedJVMs += in.GetCount()
			continue
		}
		chains[strings.Join(in.GetProviderSet(), "\x00")] = true
	}
	var why []string
	if j.GetObservationScope() == "selected-pid" {
		why = append(why, fmt.Sprintf("deployment %s was observed only through the JVM selected with --pid (%d JVM(s) found in it)", name, jvms))
	}
	if unobservedJVMs > 0 && j.GetObservationScope() != "selected-pid" {
		why = append(why, fmt.Sprintf("the provider chain was not observed for %d of %d JVMs of deployment %s; the observed chain cannot stand for the whole deployment", unobservedJVMs, jvms, name))
	}
	if len(chains) > 1 {
		why = append(why, fmt.Sprintf("deployment %s was observed with %d different provider chains (%d JVMs); a single provider chain cannot be recorded", name, len(chains), jvms))
	}
	if len(why) == 0 {
		return ""
	}
	return "before not captured: " + strings.Join(why, "; ")
}
