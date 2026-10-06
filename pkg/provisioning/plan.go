// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning

import (
	"errors"
	"fmt"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/registry"
)

// ErrNotFinalized — FINALIZED 아닌 계획을 실행 근거로 쓰려 할 때(§3.7 최강 게이트).
var ErrNotFinalized = errors.New("plan not finalized — refusing to provision")

// ErrNotActionable — 절차는 끝났는데(FINALIZED·서명·조치 있음) 그 조치로 할 수 있는 것이
// 없을 때. ErrNotFinalized와 가르는 이유는 **고칠 자리가 다르기** 때문이다: 앞엣것은 승인
// 절차로 돌아가라는 말이고, 이것은 계획의 내용을 채우라는 말이다.
var ErrNotActionable = errors.New("plan is finalized but an action cannot be acted on — refusing to provision")

// Executable — 확정 계획이 프로비저닝 실행 근거로 유효한지 검증한다(§3.7 Inventory→Deploy 게이트).
// 규칙은 두 층이다. **절차**: FINALIZED 상태 + 승인 서명(§3.3③) + 조치 최소 1건.
// **내용**: 조치마다 대상 노드가 있고 무엇을 할지가 정해져 있을 것. 파생이 아니라 실행 직전 관문이다.
//
// 승인 서명은 여기서 **개수만 센다.** 값이 유효한지·누구의 것인지는 키가 있어야 알 수 있고,
// 이 함수는 계약 규칙이라 키 재료를 들지 않는다. 그 확인은 sign.VerifyApprovals가 하고
// pqcota-provision이 PQCOTA_APPROVAL_KEYS로 배선한다. 즉 **여기를 통과했다고 승인이 확인된
// 것은 아니다** — 둘은 다른 질문이고, 답하는 자리도 다르다.
//
// 되짚을 수 있는가(§1.2 — derived_from_snapshot_id·ruleset_version·finding_id)는 여기서 막지
// 않는다. 비어도 실행은 되기 때문이다. 그쪽은 TraceabilityWarnings가 표면화한다.
//
// GATE: 배선 필수
//
// ★ 경계(§5): 이 함수는 전 컴포넌트가 공유하는 "실행 근거" 계약 규칙일 뿐이다.
// 실제 단계적 실행·오케스트레이션(drain·rolling·게이트·롤백)은 하지 않는다(§4.3).
func Executable(p *provisioningv1.FinalizedPlan) error {
	if p == nil {
		return fmt.Errorf("%w: nil", ErrNotFinalized)
	}
	if p.GetStatus() != provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED {
		return fmt.Errorf("%w: status=%s", ErrNotFinalized, p.GetStatus())
	}
	if len(p.GetApprovalSignatures()) == 0 {
		return fmt.Errorf("%w: no approval signature", ErrNotFinalized)
	}
	if len(p.GetActions()) == 0 {
		return fmt.Errorf("%w: no actions", ErrNotFinalized)
	}
	// 절차만 보고 내용을 안 보면, **확정 도장은 찍혔는데 실행할 수 없는 계획**이 게이트를 지난다.
	// 상태·서명·건수는 계획이 어떻게 만들어졌는지에 대한 것이고, 아래는 그 계획으로 무엇을
	// 할 수 있는지에 대한 것이다. §3.7이 확정 계획을 유일한 실행 근거라고 했으니 후자도 봐야 한다.
	return actionable(p)
}

// actionable — 내용 층. 조치마다 대상 노드가 있고 무엇을 할지가 정해져 있나.
//
// Executable과 PrepareApproval이 함께 쓴다. 승인 전에도 이것을 묻는 이유는, 여기서 걸리는
// 계획에 서명이 붙으면 **승인은 됐는데 실행할 수 없는 계획**이 생기기 때문이다 — 그것은
// 승인자가 책임진 것이 무엇인지 말할 수 없는 상태다.
func actionable(p *provisioningv1.FinalizedPlan) error {
	for i, a := range p.GetActions() {
		where := fmt.Sprintf("action[%d] id=%q", i, a.GetId())
		if a.GetTargetNodeId() == "" {
			// 생성기는 조치의 노드를 플레이북 `hosts` 목록에 적는다. 빈 값이면 빈 항목이 들어가
			// 어디에도 닿지 않는 play가 나오는데, 그것은 실패가 아니라 **아무 일도 없음**으로 보인다.
			return fmt.Errorf("%w: %s has no target_node_id", ErrNotActionable, where)
		}
		if a.GetKind() == provisioningv1.RemediationKind_REMEDIATION_KIND_UNSPECIFIED {
			// 생성기는 분기할 수 없어 「config로는 넣을 수 없는 조치」라고 적은 조각을 낸다.
			// 그것은 사실이 아니다 — 계획이 무엇을 할지 **말하지 않은** 것이다. 도구가 계획을
			// 대신 추측하지 않으므로, 거짓을 적은 산출물을 내느니 여기서 막는다(§2.5).
			return fmt.Errorf("%w: %s (node=%s) has kind=UNSPECIFIED, so there is nothing to generate",
				ErrNotActionable, where, a.GetTargetNodeId())
		}
	}
	return nil
}

// TraceabilityWarnings — 이 계획을 무엇에서 뽑았는지 나중에 되짚을 수 있는가(§1.2 재현).
//
// ★ Executable이 아니다 — 이 값들이 비어도 계획은 그대로 실행된다. 다만 실행하고 나면
// append-only 이력에 **무엇을 근거로 한 조치였는지**가 남지 않는다. 되짚을 수 없다는 사실은
// 되짚어야 할 때가 되어서야 드러나므로, 그전에 알린다(§2.6 — 빠진 것을 알리지 않은 채 두지 않는다).
func TraceabilityWarnings(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	if p.GetId() == "" {
		out = append(out, "the plan has no id — provisioning records point back with plan_id, so this run cannot be tied to the plan that caused it.")
	}
	// 스냅샷 참조는 **조치마다** 본다. 조치에 근거(evidence_sources)가 있으면 그것이 참조이고, 없으면
	// 계획 단위 derived_from_snapshot_id가 호환 경로다. 둘 다 없는 조치만 알린다. 참조의 모양이
	// 맞는지·찾히는지는 ResolveAction이 따로 알린다 — 여기는 「있는가」만 본다.
	if p.GetDerivedFromSnapshotId() == "" {
		for _, a := range p.GetActions() {
			if len(a.GetEvidenceSources()) == 0 {
				out = append(out, fmt.Sprintf("action %s (node=%s): no evidence_sources and the plan has no derived_from_snapshot_id — nothing says which snapshot state it was derived from (§1.2).",
					a.GetId(), a.GetTargetNodeId()))
			}
		}
	}
	if p.GetRulesetVersion() == "" {
		out = append(out, "the plan has no ruleset_version — it cannot be regenerated and compared against what was run (§1.2).")
	}
	if p.GetFinalizedAt() == nil {
		out = append(out, "the plan has no finalized_at — it claims to be FINALIZED but not when.")
	}
	for _, a := range p.GetActions() {
		if a.GetFindingId() == "" {
			out = append(out, fmt.Sprintf("action %s (node=%s): no finding_id — the action names no observed asset as its basis (§2.4).",
				a.GetId(), a.GetTargetNodeId()))
		}
	}
	return out
}

// AutomationLevelWarnings — 위임 수준을 계획이 말하지 않은 조치.
//
// 말하지 않으면 실행 수준이 `--level`에서 온다. 그 플래그는 **승인 서명 밖에 있다**(서명은 계획을
// 덮지 계획을 부르는 명령줄을 덮지 않는다). 그래서 승인자가 서명한 것과 실제 실행 수준이 갈릴 수
// 있는데, 계획이 값을 적으면 그 자리가 닫힌다. LevelFor가 조치의 값을 먼저 보는 이유가 여기 있다.
//
// 하드 블록은 하지 않는다: 수준을 명령줄에서 정하는 것이 정당한 경로인 자리가 있다(한 노드를 그
// 자리에서 시험하는 경우). 다만 그 계획은 **위임 수준을 스스로 말하지 않은 계획**이므로 빈칸으로 센다.
func AutomationLevelWarnings(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	for _, a := range p.GetActions() {
		if a.GetAutomationLevel() != provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_UNSPECIFIED {
			continue
		}
		out = append(out, fmt.Sprintf(
			"action %s (node=%s): automation_level is unset, so the level comes from --level, which the approval signature does not cover. Name the level in the plan (§4.3 per-asset).",
			a.GetId(), a.GetTargetNodeId()))
	}
	return out
}

// TargetAlgorithmWarnings — 목표 알고리즘이 하이브리드 그룹으로 풀리지 않는 config 조치.
//
// 그런 조치의 조각은 `Groups` 줄이 주석 처리된 채 나간다(groupsLine). 즉 **배치해도 아무것도
// 켜지지 않는다.** 조각 안 주석은 열어봐야 보이므로 여기서 크게 알린다 — ProviderClassWarnings와
// 같은 이유다. 서명 알고리즘(ML-DSA)이 목표일 때도 걸리지만 **문구가 다르다**: 서명은 그룹으로
// 협상하지 않아 적을 그룹이 아예 없으므로, 손으로 적으라고 하면 틀린 말이 된다.
// 하드 블록은 하지 않는다: 사람이 그룹을 손으로 적는 것이 정당한 경로다.
func TargetAlgorithmWarnings(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	for _, a := range p.GetActions() {
		switch a.GetKind() {
		case provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
			provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT:
		default:
			continue // config로 내지 않는 조치는 Groups 줄 자체가 없다
		}
		if hybridGroup(a.GetTargetAlgorithm()) != "" {
			continue
		}
		target := a.GetTargetAlgorithm()
		var why string
		switch alg, ok := registry.MatchPQC(target); {
		case target == "":
			why = "target_algorithm is unset, so the fragment ships with its Groups line commented out and **deploying it enables nothing**. Name the target, or put the group in by hand."
		case ok && alg.Kind == registry.KindSignature:
			// ★ 여기서 "그룹을 손으로 적으라"고 하면 **틀린 말**이다. 서명은 그룹으로 협상하지
			// 않아서 적을 그룹이 아예 없다. config 조각으로 서명을 바꿀 수 없다는 것이 사실이다.
			why = fmt.Sprintf("target_algorithm %q is a signature algorithm, which is not negotiated as a TLS group — this config fragment cannot deliver it. The Groups line stays commented out on purpose; the signature change belongs to the certificate and its issuance, not here.", target)
		default:
			why = fmt.Sprintf("target_algorithm %q does not resolve to a hybrid KEM group, so the fragment ships with its Groups line commented out and **deploying it enables nothing**. Put the group in by hand before deploying.", target)
		}
		out = append(out, fmt.Sprintf("action %s (node=%s): %s", a.GetId(), a.GetTargetNodeId(), why))
	}
	return out
}

// ProviderClassWarnings — 계획을 훑어, JCA provider 주입인데 provider_class를 확정할 수 없어
// java.security 조각에 **placeholder가 들어가는** 조치를 찾아 경고로 돌린다.
//
// ★ 이것은 Executable(거버넌스 게이트)이 **아니다** — 계획은 유효하고 플레이북도 정상 생성된다.
// placeholder는 의도된 정직 경로다(FQCN을 추측하지 않고 사람이 채운다 — jca.go). 다만 산출물이
// 그대로는 불완전하므로, 도구가 알리지 않고 통과시키지 않도록 호출부(pqcota-provision)가 이걸 stderr에
// 크게 알린다(§2.5 — 불명을 삼키지 않는다). 하드 블록은 "생성→사람이 FQCN 기입→적용"이라는
// 정당한 워크플로를 막으므로 하지 않는다.
// ProviderSlotWarnings — provider 주입은 java.security의 **한 자리를 대체**한다. 조각 안 주석은
// 열어봐야 보이므로, 무엇이 밀려나는지 여기서 크게 알린다(§2.6 — 유실을 알리지 않은 채 두지 않는다).
func ProviderSlotWarnings(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	for _, a := range p.GetActions() {
		if a.GetKind() != provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT ||
			a.GetCryptoRuntime() != commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA {
			continue
		}
		out = append(out, fmt.Sprintf("action %s (node=%s): `security.provider.2` **takes over slot 2** — whatever was provider 2 (usually SunRsaSign on JDK defaults) drops off the list and its services move to the new provider. To avoid displacing it, renumber the entries below in the target's java.security first, then add this.",
			a.GetId(), a.GetTargetNodeId()))
	}
	return out
}

func ProviderClassWarnings(p *provisioningv1.FinalizedPlan) []string {
	var out []string
	for _, a := range p.GetActions() {
		if a.GetKind() != provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT {
			continue
		}
		if a.GetCryptoRuntime() != commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA {
			continue // OpenSSL은 모듈 경로만 있으면 됨 — FQCN 불필요.
		}
		if _, exact := providerClass(a.GetProviderChoice(), a.GetProviderClass()); !exact {
			out = append(out, fmt.Sprintf(
				"action %s (node=%s, provider=%q): provider_class is unset — the java.security fragment will contain a placeholder. Put the FQCN in the plan's provider_class, or replace the class name in the fragment before deploying.",
				a.GetId(), a.GetTargetNodeId(), a.GetProviderChoice()))
		}
	}
	return out
}
