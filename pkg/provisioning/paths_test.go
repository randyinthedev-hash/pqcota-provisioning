package provisioning_test

import (
	"strings"
	"testing"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

func injectPlan(runtime commonv1.CryptoRuntime, provider string) *provisioningv1.FinalizedPlan {
	return &provisioningv1.FinalizedPlan{
		Id: "p", Status: provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED,
		ApprovalSignatures: []string{"r"},
		Actions: []*provisioningv1.RemediationAction{{
			Id: "a1", TargetNodeId: "n1", FindingId: "f1",
			CryptoRuntime:   runtime,
			Kind:            provisioningv1.RemediationKind_REMEDIATION_KIND_PROVIDER_INJECT,
			TargetAlgorithm: "ML-KEM (FIPS 203)", ProviderChoice: provider,
		}},
	}
}

// config가 참조하는 모듈 경로와 플레이북이 배치하는 경로가 어긋나면 OpenSSL이 모듈을 못 찾고
// 오류 없이 실패한다. 셋(생성 config·배치·롤백)이 같은 경로를 가리키는지 여기서 고정한다.
func TestModulePathAgreesAcrossGenerators(t *testing.T) {
	plan := injectPlan(commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, "myprovider")
	want := provisioning.ModulePath("myprovider", false) // /opt/pqcota/myprovider.so

	cfg := provisioning.Render(plan.GetActions()[0])
	if !strings.Contains(cfg, "module = "+want) {
		t.Errorf("config must reference an absolute path (%s):\n%s", want, cfg)
	}
	fwd := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	if !strings.Contains(fwd, `dest: "`+want+`"`) {
		t.Errorf("the playbook must place it at the same path (%s):\n%s", want, fwd)
	}
	back := provisioning.GenerateRollbackPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	if !strings.Contains(back, `path: "`+want+`", state: absent`) {
		t.Errorf("the rollback must remove the same path (%s):\n%s", want, back)
	}
}

// 상대 경로가 다시 새어나오지 않게 못 박는다.
func TestConfigNeverUsesRelativeModule(t *testing.T) {
	for _, prov := range []string{"", "oqsprovider", "myprovider"} {
		plan := injectPlan(commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, prov)
		cfg := provisioning.Render(plan.GetActions()[0])
		for _, line := range strings.Split(cfg, "\n") {
			if strings.HasPrefix(line, "module = ") && !strings.HasPrefix(line, "module = /") {
				t.Errorf("provider=%q: module is a relative path — OpenSSL looks in the module directory and fails: %q", prov, line)
			}
		}
	}
}

// 커스텀 모듈 소스는 provider별로 지정할 수 있어야 한다(한 플레이북에 여러 provider가 섞임).
func TestPerProviderModuleSourceVariable(t *testing.T) {
	plan := injectPlan(commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, "my-prov.1")
	out := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	// 이름의 비영숫자는 변수명에서 밑줄로 정규화된다.
	if !strings.Contains(out, "pqcota_module_src_my_prov_1") {
		t.Errorf("no per-provider source variable:\n%s", out)
	}
	if !strings.Contains(out, "default(pqcota_module_src") {
		t.Errorf("no global variable fallback:\n%s", out)
	}
	if !strings.Contains(out, "default('my-prov.1.so')") {
		t.Errorf("no filename fallback for the files/ convention:\n%s", out)
	}
}

// 무결성 확인은 sha256을 준 경우에만 돈다(안 주면 기존처럼 동작 — 하위호환).
func TestChecksumGate(t *testing.T) {
	plan := injectPlan(commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, "myprovider")
	out := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	for _, want := range []string{
		"checksum_algorithm: sha256",
		"ansible.builtin.assert",
		"pqcota_module_sha256_myprovider is defined",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the integrity check does not mention %q:\n%s", want, out)
		}
	}
}

// JCA는 JAR로 배치된다(확장자·경로 일관).
func TestJCAModuleIsJar(t *testing.T) {
	plan := injectPlan(commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA, "BC")
	want := provisioning.ModulePath("BC", true)
	if !strings.HasSuffix(want, ".jar") {
		t.Fatalf("a JCA module must be a .jar: %s", want)
	}
	out := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	if !strings.Contains(out, `dest: "`+want+`"`) {
		t.Errorf("JCA JAR placement path mismatch (%s):\n%s", want, out)
	}
}

// ★ 실제 ansible 실행에서 잡힌 회귀 — copy는 대상 디렉터리가 없으면 실패한다.
// L2는 config 조각을 놓으므로 그 디렉터리를 먼저 만들어야 한다(깨끗한 노드엔 없다).
func TestL2CreatesConfigDirectory(t *testing.T) {
	plan := injectPlan(commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL, "myprovider")

	l2 := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL)
	for _, want := range []string{
		"path: " + provisioning.StageDir + ", state: directory",
		"path: " + provisioning.ConfigDir + ", state: directory",
	} {
		if !strings.Contains(l2, want) {
			t.Errorf("L2 must create the directory (%s):\n%s", want, l2)
		}
	}
	// 디렉터리 생성이 config 배치보다 **앞서야** 한다.
	if strings.Index(l2, provisioning.ConfigDir+", state: directory") > strings.Index(l2, `dest: "`+provisioning.OpenSSLConfigPath+`"`) {
		t.Error("the config directory is created after the placement — the copy fails")
	}

	// L1은 config를 놓지 않으므로 그 디렉터리도 필요 없다.
	l1 := provisioning.GenerateProvisioningPlaybook(plan, provisioningv1.DeployAutomationLevel_DEPLOY_AUTOMATION_LEVEL_L1_STAGE_ONLY)
	if strings.Contains(l1, provisioning.ConfigDir+", state: directory") {
		t.Errorf("L1 does not need to create the config directory:\n%s", l1)
	}
}
