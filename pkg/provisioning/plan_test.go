package provisioning_test

import (
	"errors"
	"strings"
	"testing"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Executable — §3.7 최강 게이트. 절차(FINALIZED + 승인 서명 + 조치 ≥1)와
// 내용(조치마다 대상 노드·조치 종류)을 함께 본다.
func TestExecutable(t *testing.T) {
	base := func() *provisioningv1.FinalizedPlan {
		return &provisioningv1.FinalizedPlan{
			Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
			ApprovalSignatures: []string{"ed25519:abc"},
			Actions: []*provisioningv1.RemediationAction{{
				TargetNodeId: "web-01",
				Kind:         provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
			}},
		}
	}

	if err := provisioning.Executable(base()); err != nil {
		t.Errorf("a finalized plan must be executable: %v", err)
	}

	// draft/in-review는 거부(§3.7).
	for _, s := range []provisioningv1.PlanStatus{
		provisioningv1.PlanStatus_PLAN_STATUS_DRAFT,
		provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW,
		provisioningv1.PlanStatus_PLAN_STATUS_UNSPECIFIED,
	} {
		p := base()
		p.Status = s
		if err := provisioning.Executable(p); err == nil {
			t.Errorf("a status=%s plan must be refused", s)
		}
	}

	// 승인 서명 없으면 거부(§3.3③ finalize 전제).
	p := base()
	p.ApprovalSignatures = nil
	if err := provisioning.Executable(p); err == nil {
		t.Error("an unsigned plan must be refused")
	}

	// 조치 없으면 거부.
	p = base()
	p.Actions = nil
	if err := provisioning.Executable(p); err == nil {
		t.Error("an empty plan must be refused")
	}

	// nil 거부.
	if err := provisioning.Executable(nil); err == nil {
		t.Error("a nil plan must be refused")
	}

	// ★ 절차는 끝났는데 조치가 아무 데도 닿지 않는 계획. 대상이 없으면 플레이북의 hosts에
	// 빈 항목이 들어가, 실패가 아니라 "아무 일도 없음"으로 보인다.
	p = base()
	p.Actions[0].TargetNodeId = ""
	if err := provisioning.Executable(p); err == nil {
		t.Error("an action with no target_node_id must be refused")
	} else if !errors.Is(err, provisioning.ErrNotActionable) {
		t.Errorf("the reason must say the plan is finalized but not actionable: %v", err)
	}

	// ★ 무엇을 할지 정하지 않은 조치. 생성기는 「config로는 넣을 수 없다」고 적은 조각을 내는데,
	// 그것은 사실이 아니라 계획이 말하지 않은 것이다.
	p = base()
	p.Actions[0].Kind = provisioningv1.RemediationKind_REMEDIATION_KIND_UNSPECIFIED
	if err := provisioning.Executable(p); err == nil {
		t.Error("an action with kind=UNSPECIFIED must be refused")
	} else if !errors.Is(err, provisioning.ErrNotActionable) {
		t.Errorf("the reason must say the plan is finalized but not actionable: %v", err)
	}
}

// TraceabilityWarnings — 되짚을 수 있는가는 **경고**다. 비어도 실행은 된다(§1.2 재현).
func TestTraceabilityWarnings(t *testing.T) {
	bare := &provisioningv1.FinalizedPlan{
		Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"ed25519:abc"},
		Actions: []*provisioningv1.RemediationAction{{
			Id: "a1", TargetNodeId: "web-01",
			Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
		}},
	}
	// ★ 게이트는 통과해야 한다 — 경고와 차단은 별개다.
	if err := provisioning.Executable(bare); err != nil {
		t.Fatalf("traceability must not block execution: %v", err)
	}
	got := strings.Join(provisioning.TraceabilityWarnings(bare), "\n")
	for _, want := range []string{"no id", "derived_from_snapshot_id", "ruleset_version", "finalized_at", "no finding_id"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q must be warned about:\n%s", want, got)
		}
	}

	full := &provisioningv1.FinalizedPlan{
		Id: "plan-1", Status: provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures:    []string{"ed25519:abc"},
		DerivedFromSnapshotId: "snap-1", RulesetVersion: "ruleset-1",
		FinalizedAt: timestamppb.Now(),
		Actions: []*provisioningv1.RemediationAction{{
			Id: "a1", TargetNodeId: "web-01", FindingId: "f-1",
			Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
		}},
	}
	if w := provisioning.TraceabilityWarnings(full); len(w) != 0 {
		t.Errorf("a fully traceable plan must warn about nothing: %v", w)
	}
}

// TargetAlgorithmWarnings — 그룹으로 안 풀리는 목표는 조각의 Groups 줄이 주석으로 나간다.
// 배치해도 아무것도 켜지지 않으므로 조각 밖에서 알려야 한다.
func TestTargetAlgorithmWarnings(t *testing.T) {
	plan := func(kind provisioningv1.RemediationKind, target string) *provisioningv1.FinalizedPlan {
		return &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{{
			Id: "a1", TargetNodeId: "web-01", Kind: kind, TargetAlgorithm: target,
		}}}
	}
	const configOnly = provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY

	if w := provisioning.TargetAlgorithmWarnings(plan(configOnly, "ML-KEM (FIPS 203)")); len(w) != 0 {
		t.Errorf("a hybrid KEM target must warn about nothing: %v", w)
	}
	if w := provisioning.TargetAlgorithmWarnings(plan(configOnly, "")); len(w) != 1 {
		t.Errorf("an unset target must warn once: %v", w)
	} else if !strings.Contains(w[0], "unset") {
		t.Errorf("the warning must say it is unset: %s", w[0])
	}
	// ★ 서명 알고리즘도 걸리되 **문구가 달라야 한다.** 서명은 그룹으로 협상하지 않으므로
	// "그룹을 손으로 적으라"고 하면 있지도 않은 일을 시키는 것이 된다.
	if w := provisioning.TargetAlgorithmWarnings(plan(configOnly, "ML-DSA (FIPS 204)")); len(w) != 1 {
		t.Errorf("a signature target must warn once: %v", w)
	} else if !strings.Contains(w[0], "signature algorithm") {
		t.Errorf("the warning must say it is a signature, not a group to fill in: %s", w[0])
	} else if strings.Contains(w[0], "by hand") {
		t.Errorf("a signature target must not be told to put a group in by hand: %s", w[0])
	}
	// config로 내지 않는 조치는 Groups 줄 자체가 없다.
	fork := provisioningv1.RemediationKind_REMEDIATION_KIND_FORK_REPLACE
	if w := provisioning.TargetAlgorithmWarnings(plan(fork, "")); len(w) != 0 {
		t.Errorf("a non-config remediation must warn about nothing: %v", w)
	}
}

// ProviderClassWarnings — placeholder를 낳는 조치는 경고로 표면화되어야 한다(표시 없는 통과 금지).
// 단, Executable(거버넌스 게이트)은 여전히 통과시킨다 — 둘은 별개다.
func TestProviderClassWarnings(t *testing.T) {
	jca := func(kind provisioningv1.RemediationKind, choice, class string) *provisioningv1.RemediationAction {
		return &provisioningv1.RemediationAction{
			Id: "a1", TargetNodeId: "app-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA,
			Kind: kind, ProviderChoice: choice, ProviderClass: class,
		}
	}
	plan := func(a *provisioningv1.RemediationAction) *provisioningv1.FinalizedPlan {
		return &provisioningv1.FinalizedPlan{
			Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
			ApprovalSignatures: []string{"r"}, Actions: []*provisioningv1.RemediationAction{a},
		}
	}
	inject := provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT

	// 알 수 없는 provider + provider_class 미지정 → placeholder → 경고 1건.
	if w := provisioning.ProviderClassWarnings(plan(jca(inject, "acme-jce", ""))); len(w) != 1 {
		t.Errorf("an undecided provider must give exactly one warning: %v", w)
	} else if !strings.Contains(w[0], "acme-jce") || !strings.Contains(w[0], "provider_class") {
		t.Errorf("the warning must name the provider and the fix: %q", w[0])
	}

	// FQCN을 계획에 넣으면 경고 없음(커스텀 provider가 계획만으로 완결).
	if w := provisioning.ProviderClassWarnings(plan(jca(inject, "acme-jce", "com.acme.AcmeProvider"))); len(w) != 0 {
		t.Errorf("with provider_class given there must be no warning: %v", w)
	}
	// 알려진 이름(BC)도 경고 없음.
	if w := provisioning.ProviderClassWarnings(plan(jca(inject, "BC", ""))); len(w) != 0 {
		t.Errorf("BC can be resolved — there must be no warning: %v", w)
	}
	// PROVIDER_INJECT가 아니면(config-only 등) 무관.
	cfg := provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY
	if w := provisioning.ProviderClassWarnings(plan(jca(cfg, "acme-jce", ""))); len(w) != 0 {
		t.Errorf("without a provider injection there must be no warning: %v", w)
	}
	// OpenSSL은 FQCN이 필요 없다 — provider 미확정이어도 경고 없음.
	ossl := jca(inject, "myprov", "")
	ossl.CryptoRuntime = commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL
	if w := provisioning.ProviderClassWarnings(plan(ossl)); len(w) != 0 {
		t.Errorf("OpenSSL needs no FQCN — there must be no warning: %v", w)
	}

	// 경고가 있어도 Executable(거버넌스)은 통과한다 — 둘은 별개 관심사.
	if err := provisioning.Executable(plan(jca(inject, "acme-jce", ""))); err != nil {
		t.Errorf("a placeholder step must still pass Executable when FINALIZED (not a hard block): %v", err)
	}
}
