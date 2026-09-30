package provisioning_test

import (
	"strings"
	"testing"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"gopkg.in/yaml.v3"
)

// 오류 메시지용 표시 이름 — 검증 대상이 아니라 어느 레벨에서 깨졌는지 읽기 위한 것이다.
func levelName(l provisioningv1.DeployAutomationLevel) string {
	switch l {
	case provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY:
		return "L1"
	case provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL:
		return "L2"
	case provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO:
		return "L3"
	}
	return l.String()
}

func samplePlan() *provisioningv1.FinalizedPlan {
	return &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{
		{Id: "a1", TargetNodeId: "web-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
			Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY, TargetAlgorithm: "ML-KEM (FIPS 203)"},
		{Id: "a2", TargetNodeId: "app-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA,
			Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT, TargetAlgorithm: "ML-KEM (FIPS 203)", ProviderChoice: "BC"},
		{Id: "a3", TargetNodeId: "db-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
			Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_FORK_REPLACE},
	}}
}

// L2: 노드별 play + 모듈 스테이지(주입형) + config 조각 배치. 활성화·재시작은 하지 않는다 — 그것은 L3.
func TestProvisioningPlaybookL2(t *testing.T) {
	pb := provisioning.GenerateProvisioningPlaybook(samplePlan(), provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	for _, want := range []string{
		`hosts: ["web-01"]`, `hosts: ["app-01"]`,
		"stage the provider module (BC)", "/opt/pqcota/BC.jar", // JCA 주입 모듈
		"content: |",           // config 조각 배치
		"java.security.pqcota", // JCA config 경로
		"openssl-pqc.cnf",      // openssl config 경로
		"a3 (REMEDIATION_KIND_FORK_REPLACE): cannot be delivered through config", // 비-config 조치는 주석
	} {
		if !strings.Contains(pb, want) {
			t.Errorf("the L2 playbook is missing %q:\n%s", want, pb)
		}
	}
	// 태스크 이름으로 본다 — 머리말 주석에도 "restart"라는 낱말이 들어가므로
	// 낱말만 세면 자기 설명에 걸린다(영어로 옮기며 실제로 걸렸다).
	if strings.Contains(pb, "③ restart") {
		t.Errorf("L2 must have no restart task (restart belongs to the L3 hook):\n%s", pb)
	}
}

// JCA provider 주입이 있으면 헤더에 classpath 배선 함정을 먼저 짚는다(JAR 배치≠로드).
// openssl 전용 계획엔 뜨지 않는다 — 무관한 노트로 헤더를 어지럽히지 않는다.
func TestJCAClasspathHintInHeader(t *testing.T) {
	// samplePlan은 JCA 주입(a2)을 포함 → 헤더 노트가 떠야 한다(L1·L2 모두).
	for _, lvl := range []provisioningv1.DeployAutomationLevel{
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY,
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL,
	} {
		pb := provisioning.GenerateProvisioningPlaybook(samplePlan(), lvl)
		for _, want := range []string{"includes a JCA provider injection", "classpath or --module-path", "activation.activate"} {
			if !strings.Contains(pb, want) {
				t.Errorf("lvl=%s: the JCA pitfall header is missing %q:\n%s", levelName(lvl), want, pb)
			}
		}
	}

	// openssl 전용 계획 → 노트 없음.
	ossl := &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{
		{Id: "a1", TargetNodeId: "web-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
			Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT, ProviderChoice: "oqsprovider"},
	}}
	if pb := provisioning.GenerateProvisioningPlaybook(ossl, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL); strings.Contains(pb, "includes a JCA provider injection") {
		t.Errorf("an openssl-only plan produced a JCA note:\n%s", pb)
	}
}

// L1: 모듈 스테이지만 — config 조각 배치 없음.
func TestProvisioningPlaybookL1(t *testing.T) {
	pb := provisioning.GenerateProvisioningPlaybook(samplePlan(), provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	if !strings.Contains(pb, "/opt/pqcota/BC.jar") {
		t.Error("L1 must still stage the module")
	}
	if strings.Contains(pb, "content: |") {
		t.Errorf("L1 (stage-only) must not place config fragments:\n%s", pb)
	}
}

// L3 — 사용자가 준 훅을 **의미 순서로** 배치한다: pre → [배치] → activate → restart.
// 순서가 곧 안전성이다(내리고 → 바꾸고 → 참조되게 하고 → 새로 로드).
func TestL3ActivationOrder(t *testing.T) {
	a := &provisioningv1.RemediationAction{
		Id: "a1", TargetNodeId: "db-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
		Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT, ProviderChoice: "oqsprovider",
		Activation: &provisioningv1.ActivationHooks{
			Pre: "systemctl stop payment", Activate: "ln -sf /etc/pqcota/openssl-pqc.cnf /etc/ssl/inc/",
			Deactivate: "rm -f /etc/ssl/inc/openssl-pqc.cnf", Restart: "systemctl start payment",
		},
	}
	plan := &provisioningv1.FinalizedPlan{
		Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"r"}, Actions: []*provisioningv1.RemediationAction{a},
	}
	L3 := provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO

	fwd := provisioning.GenerateProvisioningPlaybook(plan, L3)
	iPre, iCfg := strings.Index(fwd, "systemctl stop payment"), strings.Index(fwd, "place the config fragment")
	iAct, iRst := strings.Index(fwd, "ln -sf"), strings.Index(fwd, "systemctl start payment")
	for _, s := range []int{iPre, iCfg, iAct, iRst} {
		if s < 0 {
			t.Fatalf("the L3 playbook is missing a step:\n%s", fwd)
		}
	}
	if !(iPre < iCfg && iCfg < iAct && iAct < iRst) {
		t.Errorf("order must be pre→stage→activate→restart: pre=%d cfg=%d act=%d restart=%d", iPre, iCfg, iAct, iRst)
	}

	// 롤백은 정확한 역순 — 내리고 → 활성화 되돌리고 → 파일 제거 → 재시작.
	back := provisioning.GenerateRollbackPlaybook(plan, L3)
	jPre, jDeact := strings.Index(back, "systemctl stop payment"), strings.Index(back, "rm -f /etc/ssl/inc")
	jRm, jRst := strings.Index(back, "state: absent"), strings.Index(back, "systemctl start payment")
	if !(jPre < jDeact && jDeact < jRm && jRm < jRst) {
		t.Errorf("rollback order must be pre→deactivate→remove→restart: %d %d %d %d", jPre, jDeact, jRm, jRst)
	}

	// L2에는 훅이 들어가지 않는다 — 활성화는 L3에서만.
	l2 := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	if strings.Contains(l2, "systemctl") || strings.Contains(l2, "ln -sf") {
		t.Errorf("an activation hook leaked into L2:\n%s", l2)
	}
}

// ★ 추측 금지(§2.5) — 훅이 비면 그 단계를 **만들지 않고**, 무엇이 안 일어나는지 고지한다.
func TestL3MissingHooksWarnButDoNotGuess(t *testing.T) {
	bare := &provisioningv1.RemediationAction{
		Id: "a1", TargetNodeId: "db-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
		Kind: provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
	}
	plan := &provisioningv1.FinalizedPlan{
		Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"r"}, Actions: []*provisioningv1.RemediationAction{bare},
	}
	L3 := provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO

	// 활성화 명령을 지어내지 않는다 — systemctl 같은 걸 추측하면 남의 운영을 망가뜨린다.
	out := provisioning.GenerateProvisioningPlaybook(plan, L3)
	if strings.Contains(out, "systemctl") || strings.Contains(out, "ansible.builtin.shell") {
		t.Errorf("a command was invented for an empty hook:\n%s", out)
	}
	// 대신 무엇이 일어나지 않는지 경고한다.
	w := provisioning.ActivationWarnings(plan, L3)
	if len(w) < 2 {
		t.Fatalf("the missing activate and restart must each be reported: %v", w)
	}
	joined := strings.Join(w, " ")
	for _, want := range []string{"activate", "restart"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the warning is missing %q: %v", want, w)
		}
	}
	// L1/L2에서는 훅 경고가 없다(활성화를 약속하지 않으므로).
	if len(provisioning.ActivationWarnings(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)) != 0 {
		t.Error("L2 must not emit an activation warning")
	}
	// deactivate 경고는 activate가 있을 때만 의미 있다(되돌릴 게 있을 때).
	bare.Activation = &provisioningv1.ActivationHooks{Activate: "x", Restart: "y"}
	if got := strings.Join(provisioning.ActivationWarnings(plan, L3), " "); !strings.Contains(got, "deactivate") {
		t.Errorf("with activate but no deactivate there must be a reversibility warning: %q", got)
	}
}

// 같은 노드에 조치가 여러 개고 재시작 명령이 같으면 — 재시작은 **한 번**이어야 한다.
// 조치별로 훅을 내면 서비스를 n번 흔들고, 활성화 사이에 재시작이 끼어 일부만 반영된 채 뜬다.
func TestL3HooksGroupedAndDeduped(t *testing.T) {
	h := func() *provisioningv1.ActivationHooks {
		return &provisioningv1.ActivationHooks{
			Pre: "svc stop pay", Activate: "", Deactivate: "", Restart: "svc start pay",
		}
	}
	a1 := &provisioningv1.RemediationAction{Id: "a1", TargetNodeId: "db-01",
		CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
		Kind:          provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY, Activation: h()}
	a2 := &provisioningv1.RemediationAction{Id: "a2", TargetNodeId: "db-01",
		CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
		Kind:          provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY, Activation: h()}
	a2.Activation.Activate = "touch /etc/ssl/inc/on" // a2만 활성화 명령이 다름
	plan := &provisioningv1.FinalizedPlan{
		Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"r"}, Actions: []*provisioningv1.RemediationAction{a1, a2},
	}
	L3 := provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO

	for name, pb := range map[string]string{
		"forward":  provisioning.GenerateProvisioningPlaybook(plan, L3),
		"rollback": provisioning.GenerateRollbackPlaybook(plan, L3),
	} {
		if n := strings.Count(pb, "svc start pay"); n != 1 {
			t.Errorf("%s: the same restart command appears %d times (must be 1):\n%s", name, n, pb)
		}
		if n := strings.Count(pb, "svc stop pay"); n != 1 {
			t.Errorf("%s: the same pre command appears %d times (must be 1)", name, n)
		}
		// 재시작은 활성화보다 **뒤**에 한 번 — 사이에 끼지 않는다.
		if i, j := strings.Index(pb, "touch /etc/ssl/inc/on"), strings.Index(pb, "svc start pay"); name == "forward" && !(i > 0 && i < j) {
			t.Errorf("forward: activation (%d) must come before restart (%d)", i, j)
		}
	}
}

// 같은 노드·같은 런타임에 **서로 다른** 조각이 둘이면, 같은 경로에 두 번 copy해선 안 된다 —
// 뒤가 앞을 알리지 않고 덮어써 앞 조치가 사라진다(모듈만 놓이고 참조는 안 되는 상태로 배포됨, §2.6).
func TestConfigFragmentsNeverOverwriteEachOther(t *testing.T) {
	mk := func(id, kind string) *provisioningv1.RemediationAction {
		k := provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY
		prov := ""
		if kind == "inject" {
			k, prov = provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT, "oqsprovider"
		}
		return &provisioningv1.RemediationAction{Id: id, TargetNodeId: "db-01",
			CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, Kind: k,
			ProviderChoice: prov, TargetAlgorithm: "ML-KEM (FIPS 203)"}
	}
	// a1=주입(provider_sect 있는 조각), a2=config-only(다른 조각) → 내용이 다르다.
	plan := &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{mk("a1", "inject"), mk("a2", "cfg")}}
	L2 := provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL

	pb := provisioning.GenerateProvisioningPlaybook(plan, L2)
	if n := strings.Count(pb, `dest: "`+provisioning.OpenSSLConfigPath+`"`); n > 1 {
		t.Errorf("placed at the same path %d times — the later one overwrites the earlier:\n%s", n, pb)
	}
	// 두 조각이 모두 살아 있어야 한다(하나가 사라지면 안 된다).
	for _, want := range []string{"provider_sect", "config-only"} {
		if !strings.Contains(pb, want) {
			t.Errorf("fragment %q was lost:\n%s", want, pb)
		}
	}
	// 경로를 나눈 사실을 알린다 — 나눈 채 두면 어느 것도 참조되지 않는다.
	if w := provisioning.ConfigConflictWarnings(plan); len(w) != 1 || !strings.Contains(w[0], "activation.activate") {
		t.Errorf("the path split must be reported: %v", w)
	}
	// 롤백은 나눈 경로를 **그대로** 지운다(대칭) — 안 지우면 잔재가 남는다.
	back := provisioning.GenerateRollbackPlaybook(plan, L2)
	for id, d := range provisioning.ConfigDests(plan.GetActions()) {
		if !strings.Contains(back, `path: "`+d+`",`) {
			t.Errorf("rollback does not remove %s (action %s):\n%s", d, id, back)
		}
	}

	// 내용이 같은 조각 둘 → 경로를 나누지 않고, 배치도 한 번(중복 파일·중복 태스크 없음).
	same := &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{mk("b1", "cfg"), mk("b2", "cfg")}}
	sp := provisioning.GenerateProvisioningPlaybook(same, L2)
	if n := strings.Count(sp, "place the config fragment"); n != 1 {
		t.Errorf("identical fragments produced %d placement tasks (must be 1):\n%s", n, sp)
	}
	if len(provisioning.ConfigConflictWarnings(same)) != 0 {
		t.Error("identical fragments must not raise a collision warning")
	}
}

// 생성물이 **정말 YAML인가** — 문자열 검사로는 못 잡는다. 사용자가 적은 훅에는 줄바꿈·`:`·`#`·
// 인용부호가 들어올 수 있고, 그대로 한 줄 스칼라에 붙이면 ansible-playbook이 파일을 읽지도 못한다.
// 여러 줄 명령·따옴표·주석 기호가 훅에 들어오면 특히 위험하다. 그래서 실제로 파싱한다.
func TestGeneratedPlaybooksAreValidYAML(t *testing.T) {
	nasty := "printf 'OPENSSL_CONF=%s\\n' /etc/pqcota/x.cnf > /etc/pqcota/service.env\nsystemctl daemon-reload  # comment: it has a colon too"
	a := &provisioningv1.RemediationAction{
		Id: "a1: odd id", TargetNodeId: "db-01",
		CryptoRuntime:  commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
		Kind:           provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT,
		ProviderChoice: `oqs "prov": #1`, TargetAlgorithm: "ML-KEM (FIPS 203)",
		Activation: &provisioningv1.ActivationHooks{
			Pre: "svc stop", Activate: nasty, Deactivate: "rm -f /etc/pqcota/service.env", Restart: "svc start",
		},
	}
	plan := &provisioningv1.FinalizedPlan{
		Status:             provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"r"}, Actions: []*provisioningv1.RemediationAction{a},
	}

	for _, lvl := range []provisioningv1.DeployAutomationLevel{
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY,
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL,
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO,
	} {
		for name, pb := range map[string]string{
			"forward":  provisioning.GenerateProvisioningPlaybook(plan, lvl),
			"rollback": provisioning.GenerateRollbackPlaybook(plan, lvl),
		} {
			var plays []map[string]any
			if err := yaml.Unmarshal([]byte(pb), &plays); err != nil {
				t.Fatalf("%s/%s: the output is not YAML: %v\n%s", levelName(lvl), name, err, pb)
			}
			if len(plays) != 1 {
				t.Fatalf("%s/%s: expected 1 play, got %d", levelName(lvl), name, len(plays))
			}
			if got := plays[0]["hosts"]; !strings2Contains(got, "db-01") {
				t.Errorf("%s/%s: hosts is malformed: %#v", levelName(lvl), name, got)
			}
		}
	}

	// L3 forward에는 훅 명령이 **원문 그대로** 살아 있어야 한다(이스케이프로 변형되면 다른 명령이 된다).
	fwd := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO)
	var plays []struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(fwd), &plays); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tk := range plays[0].Tasks {
		if s, ok := tk["ansible.builtin.shell"].(string); ok && strings.TrimSpace(s) == strings.TrimSpace(nasty) {
			found = true
		}
	}
	if !found {
		t.Errorf("the hook command was not carried through verbatim:\n%s", fwd)
	}
}

// hosts는 [문자열] 형태 — 파싱 결과에서 노드 id를 찾는다.
func strings2Contains(v any, want string) bool {
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// ★ 자산별 위임 수준이 전역 플래그에 평탄화되지 않는다.
//
// 계약은 automation_level을 **조치별** 속성으로 정하고(§4.3 "레벨은 자산별 속성이며 전사
// 일괄이 아니다"), 승인 서명이 그 값을 덮는다(sign.CanonicalPlan). 그런데 생성기가 전역
// `--level` 하나로 모든 조치를 내던 동안, 「결제 서버=L1 · 무상태 워커=L3」으로 확정한 계획이
// `--level l3` 한 번에 평탄화됐다. **승인자가 서명한 위임 수준과 실제 실행 수준이 어긋나는**
// 자리라, 위험도에 따라 위임을 나눈 판정이 실행에서 사라졌다.
func TestPerAssetAutomationLevelSurvivesTheGlobalFlag(t *testing.T) {
	plan := &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{
		// 고위험 자산 — 계획이 L1로 확정했다. 스테이지만, config도 활성화도 없다.
		{Id: "pay", TargetNodeId: "pay-db", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
			Kind:            provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT,
			TargetAlgorithm: "ML-KEM (FIPS 203)", ProviderChoice: "oqsprovider",
			AutomationLevel: provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY,
			Activation:      &provisioningv1.ActivationHooks{Activate: "pay-activate", Restart: "pay-restart"}},
		// 무상태 워커 — 계획이 L3로 확정했다. 활성화·재시작까지 간다.
		{Id: "worker", TargetNodeId: "worker-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
			Kind:            provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
			TargetAlgorithm: "ML-KEM (FIPS 203)",
			AutomationLevel: provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO,
			Activation:      &provisioningv1.ActivationHooks{Activate: "worker-activate", Restart: "worker-restart"}},
	}}

	// 전역 기본값을 어느 쪽으로 주든 조치별 판정이 우선한다. 그래서 두 방향을 다 돌린다 —
	// 한 방향만 보면 "기본값이 우연히 맞았다"와 구별되지 않는다.
	for _, fallback := range []provisioningv1.DeployAutomationLevel{
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO,
		provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY,
	} {
		pb := provisioning.GenerateProvisioningPlaybook(plan, fallback)
		if strings.Contains(pb, "pay-activate") || strings.Contains(pb, "pay-restart") {
			t.Errorf("default=%s: the L1 asset was activated — the plan finalized it as stage-only:\n%s", levelName(fallback), pb)
		}
		if !strings.Contains(pb, "worker-activate") || !strings.Contains(pb, "worker-restart") {
			t.Errorf("default=%s: the L3 asset was not activated — the plan finalized it as full-auto:\n%s", levelName(fallback), pb)
		}
		// L1은 config 조각을 놓지 않는다. 놓으면 L2를 한 것이다.
		if strings.Contains(pb, "/etc/pqcota/openssl-pqc.cnf") && !strings.Contains(pb, `hosts: ["worker-01"]`) {
			t.Errorf("default=%s: a config fragment was staged for the L1 asset:\n%s", levelName(fallback), pb)
		}
	}

	// 롤백도 같은 규칙으로 갈려야 대칭이다 — 놓지 않은 것을 지우려 들면 안 된다.
	rb := provisioning.GenerateRollbackPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	if strings.Contains(rb, "pay-activate") {
		t.Errorf("the rollback ran the L1 asset's activation hook:\n%s", rb)
	}
	if !strings.Contains(rb, "worker-restart") {
		t.Errorf("the rollback skipped the L3 asset's restart:\n%s", rb)
	}
}

// 전역이 L2여도 계획이 L3로 확정한 조치의 훅 누락은 경고로 나와야 한다.
// 전에는 전역만 보아 그런 조치가 경고조차 되지 않았다.
func TestActivationWarningsFollowThePlansLevel(t *testing.T) {
	plan := &provisioningv1.FinalizedPlan{Actions: []*provisioningv1.RemediationAction{
		{Id: "w", TargetNodeId: "worker-01", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
			Kind:            provisioningv1.RemediationKind_REMEDIATION_KIND_CONFIG_ONLY,
			AutomationLevel: provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L3_FULL_AUTO},
	}}
	ws := provisioning.ActivationWarnings(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	if len(ws) == 0 {
		t.Error("an L3 action with no hooks drew no warning while the global default was L2")
	}
}
