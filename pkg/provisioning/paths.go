package provisioning

import (
	"path"
	"strings"
)

// 배치 경로 — **여기서만 정의한다.**
//
// 생성기가 세 곳(플레이북 dest, config의 module 참조, 롤백 제거 대상)에서 같은 경로를 가리켜야
// 하는데, 각자 문자열을 들고 있으면 표시 없이 어긋난다. 실제로 그랬다 — config는 `module =
// oqsprovider.so`(상대명)를 냈고 파일은 `/opt/pqcota/oqsprovider.so`에 놓여, OpenSSL이
// 모듈 디렉터리에서 찾다 로드에 실패하는 구성이었다. 상수 한 벌로 묶어 재발을 막는다.
const (
	// StageDir — provider 모듈이 놓이는 곳. 기존 시스템 경로를 건드리지 않는 별도 디렉터리라
	// 롤백이 "파일 제거"로 끝난다.
	StageDir = "/opt/pqcota"
	// ConfigDir — config 조각이 놓이는 곳. 원본 openssl.cnf·java.security를 덮지 않는다.
	ConfigDir = "/etc/pqcota"

	OpenSSLConfigPath = ConfigDir + "/openssl-pqc.cnf"
	JCAConfigPath     = ConfigDir + "/java.security.pqcota"
)

// ModuleFile — provider 모듈의 파일명. JCA는 JAR, OpenSSL은 공유 라이브러리.
func ModuleFile(provider string, jca bool) string {
	if provider == "" {
		provider = "provider"
	}
	if jca {
		return provider + ".jar"
	}
	return provider + ".so"
}

// ModulePath — 타깃에 놓이는 모듈의 **절대 경로**. config의 `module` 참조가 이 값을 써야
// OpenSSL이 모듈 디렉터리를 뒤지지 않고 바로 찾는다.
func ModulePath(provider string, jca bool) string {
	return path.Join(StageDir, ModuleFile(provider, jca))
}

// ConfigPath — 런타임별 config 조각의 배치 경로.
func ConfigPath(jca bool) string {
	if jca {
		return JCAConfigPath
	}
	return OpenSSLConfigPath
}

// SplitConfigPath — 한 노드·런타임에 **서로 다른 조각이 둘 이상**일 때 조치별로 나눈 경로.
// 같은 경로에 두 번 copy하면 뒤가 앞을 알리지 않고 덮어써 앞 조치가 사라진다(§2.6 — 유실을 알리지 않은 채 두지 않는다).
// 어느 조각을 살릴지는 도구가 정하지 않는다 — 둘 다 놓고, 무엇을 참조할지는 활성화 훅이 정한다(§2.1).
func SplitConfigPath(jca bool, actionID string) string {
	base := ConfigPath(jca)
	ext := path.Ext(base) // .cnf / (JCA는 확장자 없음)
	return strings.TrimSuffix(base, ext) + "." + actionID + ext
}

// varSuffix — Ansible 변수명에 쓸 수 있게 provider 이름을 정규화한다(영숫자·밑줄만).
// provider별로 소스·체크섬을 따로 줄 수 있어야 한 플레이북에 여러 provider가 섞여도 된다.
func varSuffix(provider string) string {
	if provider == "" {
		provider = "provider"
	}
	var b strings.Builder
	for _, r := range provider {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
