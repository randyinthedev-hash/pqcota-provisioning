#!/usr/bin/env bash
# 예제용 **진짜** provider를 가져온다 — 리포에 바이너리를 넣지 않으면서 예제를 끝까지 돌리기 위해.
#
# 왜 커밋하지 않나: provider 바이너리는 arch·libc별로 다르고(.so), JAR은 10MB급이라 리포가
# **늙는 사본**을 떠안는다. 대신 받을 곳과 **고정한 해시**를 둔다 — 받은 것이 기대한 것인지
# 확인할 수 있으면 그게 커밋한 것보다 낫다.
#
#   ./fetch-example-provider.sh          BC.jar을 여기(files/)에 놓는다
#   ./fetch-example-provider.sh --check  받지 않고 고정값만 보여준다
#
# OpenSSL provider(oqsprovider.so)는 여기서 받을 수 없다 — 배포판·arch별 공식 바이너리가 없어
# liboqs + oqs-provider를 빌드해야 한다. 없는 것을 있는 척 만들지 않는다.
set -euo pipefail
cd "$(dirname "$0")"

# ── 고정값 ────────────────────────────────────────────────────────────────────
# 버전을 고정하는 이유: "최신"을 받으면 실행할 때마다 다른 것이 오고 해시로 확인할 수 없다.
# 고정한 핀은 틀린 게 아니라 오래된 것이다 — 올릴 때 해시도 함께 올린다.
BC_VERSION=1.85
BC_SHA256=20af26bf6060bb8005cc2389916812c1e0e998dc48d2ced7131b89461b54cff7
BC_URL="https://repo1.maven.org/maven2/org/bouncycastle/bcprov-jdk18on/${BC_VERSION}/bcprov-jdk18on-${BC_VERSION}.jar"
DEST=BC.jar # jca-provider-inject-bc 케이스가 찾는 이름(providerChoice=BC)

# ★ 1.80+를 고정한 이유 — 이 세대부터 ML-KEM이 **BouncyCastleProvider**(생성되는 조각이 등록하는
# 그 클래스)에 있다. 1.78.x 이하는 그 클래스에 KEM이 0개이고 Kyber가 BouncyCastlePQCProvider에
# 따로 있어, 같은 조각을 써도 목표 알고리즘이 생기지 않는다.

if [ "${1:-}" = "--check" ]; then
	echo "BouncyCastle ${BC_VERSION}"
	echo "  URL    : $BC_URL"
	echo "  sha256 : $BC_SHA256"
	exit 0
fi

echo "▶ downloading BouncyCastle ${BC_VERSION}…"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL -o "$tmp" "$BC_URL"

got=$(sha256sum "$tmp" | cut -d' ' -f1)
if [ "$got" != "$BC_SHA256" ]; then
	echo "✗ sha256 mismatch — what arrived is not the expected artifact. Not staging it." >&2
	echo "   expected: $BC_SHA256" >&2
	echo "   got:      $got" >&2
	exit 1
fi
mv "$tmp" "$DEST"
trap - EXIT
echo "✓ $DEST ($(du -h "$DEST" | cut -f1)) — sha256 verified"
echo
echo "pass this hash straight to the playbook's integrity gate:"
echo "  -e pqcota_module_sha256_BC=$BC_SHA256"
echo
echo "check that the fragment really registers this JAR: ./verify-registration.sh"
