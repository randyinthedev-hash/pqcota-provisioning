package provisioning_test

import (
	"strings"
	"testing"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// CaptureState — 롤백 before 상태(모듈+버전)를 findings에서 요약.
func TestCaptureState(t *testing.T) {
	findings := []*discoveryv1.Finding{
		{RuntimeAxes: &discoveryv1.Finding_Openssl{Openssl: &discoveryv1.OpensslAxes{Lib: "libcrypto.so.3", Version: "3.0.13"}}},
		{RuntimeAxes: &discoveryv1.Finding_Openssl{Openssl: &discoveryv1.OpensslAxes{Lib: "libssl.so.3", Version: ""}}},
		{CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA,
			RuntimeAxes: &discoveryv1.Finding_Jca{Jca: &discoveryv1.JcaAxes{ProviderSet: []string{"SUN", "BC"}}}},
	}
	st := provisioning.CaptureState(findings)
	joined := strings.Join(st.GetModules(), ",")
	for _, want := range []string{"libcrypto.so.3@3.0.13", "libssl.so.3", "jca:BC"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the before state does not contain %q: %v", want, st.GetModules())
		}
	}
	if len(st.GetProviderChain()) != 2 {
		t.Errorf("provider chain: %v", st.GetProviderChain())
	}

	// ProvisioningRecord: before 캡처 + STAGED 상태.
	rec := provisioning.NewProvisioningRecord("r1", "web-01", []string{"payment.service"}, "plan1",
		&provisioningv1.RemediationAction{Id: "a1"}, findings)
	if rec.GetBefore() == nil || len(rec.GetBefore().GetModules()) == 0 {
		t.Error("the record did not capture the before state")
	}
	if rec.GetStatus() != provisioningv1.ProvisioningStatus_PROVISIONING_STATUS_STAGED {
		t.Errorf("initial state: %v", rec.GetStatus())
	}
}
