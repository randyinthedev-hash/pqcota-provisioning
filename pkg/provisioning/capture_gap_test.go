// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"strings"
	"testing"

	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// before 캡처의 여섯 경우(JCA 식별 1단계, 계획 §5)와 --pid 관측 범위.

func inst(observed bool, count uint32, chain ...string) *discoveryv1.JcaInstance {
	return &discoveryv1.JcaInstance{ChainObserved: observed, Count: count, ProviderSet: chain}
}

func deployment(scope string, chain []string, insts ...*discoveryv1.JcaInstance) *discoveryv1.Finding {
	return &discoveryv1.Finding{Id: "f", AppKeys: []string{"pay.service"}, CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA,
		RuntimeAxes: &discoveryv1.Finding_Jca{Jca: &discoveryv1.JcaAxes{ProviderSet: chain, Instances: insts, AppKeyKind: "systemd-unit", ObservationScope: scope}}}
}

func TestBeforeCaptureCases(t *testing.T) {
	bc := []string{"SUN", "BC"}
	for _, c := range []struct {
		name   string
		f      *discoveryv1.Finding
		chain  string // 캡처한 체인
		reason string // 사유에 들어가야 할 말(빈 값이면 사유 없음)
	}{
		{"옛 입력, 체인 있음", &discoveryv1.Finding{RuntimeAxes: &discoveryv1.Finding_Jca{Jca: &discoveryv1.JcaAxes{ProviderSet: bc}}}, "SUN,BC", ""},
		{"옛 입력, 체인을 못 봄", &discoveryv1.Finding{RuntimeAxes: &discoveryv1.Finding_Jca{Jca: &discoveryv1.JcaAxes{}}}, "", ""},
		{"모두 관측, 체인 하나", deployment("", bc, inst(true, 3, bc...)), "SUN,BC", ""},
		{"모두 관측, 체인 여럿", deployment("", nil, inst(true, 1, bc...), inst(true, 1, "SUN")), "", "2 different provider chains"},
		{"일부 변형의 체인 미관측(셋은 같은 체인, 넷째는 못 봄)", deployment("", nil, inst(true, 3, bc...), inst(false, 1)), "", "not observed for 1 of 4 JVMs"},
		{"일부 미관측이면서 체인도 여럿", deployment("", nil, inst(true, 1, bc...), inst(true, 1, "SUN"), inst(false, 1)), "", "not observed for 1 of 3 JVMs"},
		{"--pid로 하나만 관측(같은 서비스의 JVM 둘)", deployment("selected-pid", nil, inst(true, 1, bc...), inst(false, 1)), "", "selected with --pid (2 JVM(s) found"},
		{"--pid로 하나만 관측(그 배포본에 JVM 하나)", deployment("selected-pid", bc, inst(true, 1, bc...)), "", "selected with --pid (1 JVM(s) found"},
	} {
		st := provisioning.CaptureState([]*discoveryv1.Finding{c.f})
		why := provisioning.BeforeGap([]*discoveryv1.Finding{c.f})
		if got := strings.Join(st.GetProviderChain(), ","); got != c.chain {
			t.Errorf("%s: captured chain %q, want %q", c.name, got, c.chain)
		}
		switch {
		case c.reason == "" && why != "":
			t.Errorf("%s: unexpected reason %q", c.name, why)
		case c.reason != "" && (!strings.Contains(why, c.reason) || !strings.HasPrefix(why, "before not captured: ")):
			t.Errorf("%s: reason %q, want it to say %q", c.name, why, c.reason)
		}
		if c.reason != "" && len(st.GetModules()) != 0 {
			t.Errorf("%s: modules %v were recorded although the before was not captured", c.name, st.GetModules())
		}
	}
	// 「여럿」과 「일부 미관측」이 함께면 두 사실을 다 적는다.
	why := provisioning.BeforeGap([]*discoveryv1.Finding{deployment("", nil, inst(true, 1, bc...), inst(true, 1, "SUN"), inst(false, 1))})
	if !strings.Contains(why, "not observed") || !strings.Contains(why, "different provider chains") {
		t.Errorf("both facts are not stated: %q", why)
	}
}
