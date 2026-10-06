#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
# SPDX-License-Identifier: Apache-2.0
# 생성한 java.security 조각이 **정말로 provider를 등록하는가**를 실제 JVM에서 확인한다.
#
# 빈 파일로 플레이북을 돌리면 "Ansible이 파일을 복사했다"까지만 확인된다. 그것은 생성물이 의도한
# 일을 하는지에 대해 아무 말도 하지 않는다 — 조각이 적용조차 되지 않는 경우가 실제로 있다
# (레거시 OpenSSL에서 그랬다). 그래서 여기서 진짜 JAR과 진짜 JVM으로 끝까지 본다.
#
#   ./verify-registration.sh                       기본: jca-provider-inject-bc 케이스
#   ./verify-registration.sh jca-fips-bcfips       다른 케이스
#
# 전제: ./fetch-example-provider.sh로 BC.jar을 먼저 받아둘 것. JDK는 있으면 쓰고, 없으면 Docker.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CASE="${1:-jca-provider-inject-bc}"
JAR="$HERE/BC.jar"

[ -f "$JAR" ] || {
	echo "✗ no $JAR — run ./fetch-example-provider.sh first" >&2
	exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# 1) 계획 → java.security 조각. 플레이북이 노드에 놓는 바로 그 내용을 꺼낸다.
cd "$ROOT"
# 예제 계획은 승인 전(IN_REVIEW)이다. 생성기는 확정된 계획만 받으므로 --allow-unverified-approvals로
# 서명 검증을 건너뛰어도 거부된다(그 플래그는 확정 상태 관문을 열지 않는다). 그래서 ../run.sh처럼
# 이 실행 안에서만 쓰는 키로 실제로 승인하고, 그 키로 검증하게 한다.
go build -o "$work/keygen" github.com/randyinthedev-hash/pqcota-common/cmd/pqcota-keygen
go build -o "$work/approve" ./cmd/pqcota-approve
go build -o "$work/provision" ./cmd/pqcota-provision
keys="$("$work/keygen")"
PQCOTA_APPROVAL_KEY="$(printf '%s\n' "$keys" | sed -n 's/^PQCOTA_SIGN_KEY=//p')" \
	"$work/approve" --approver verify-registration "examples/provisioning/plans/$CASE.json" \
	>"$work/plan.signed.json" 2>"$work/approve.err" || {
	echo "✗ approving $CASE failed:" >&2
	cat "$work/approve.err" >&2
	exit 1
}
# 빈칸이 남은 계획(종료 상태 3)도 조각은 나온다. 여기서 보려는 것은 조각이 JVM에서 먹는가이므로
# 3은 받아들이고, 거부(1)·사용법(2)은 그 이유를 보이고 멈춘다. 오류를 버리면 왜 멈췄는지 모른다.
st=0
PQCOTA_APPROVAL_KEYS="verify-registration=$(printf '%s\n' "$keys" | sed -n 's/^PQCOTA_VERIFY_KEY=//p')" \
	"$work/provision" --level l2 "$work/plan.signed.json" >"$work/playbook.yml" 2>"$work/provision.err" || st=$?
if [ "$st" != 0 ] && [ "$st" != 3 ]; then
	echo "✗ generating the playbook for $CASE failed (exit $st):" >&2
	cat "$work/provision.err" >&2
	exit 1
fi
sed -n '/content: |/,/^    - name:/p' "$work/playbook.yml" | sed -n 's/^          //p' >"$work/pqcota.security"
[ -s "$work/pqcota.security" ] || {
	echo "✗ no java.security fragment came out of $CASE (does this case stage no config?)" >&2
	exit 1
}
cp "$JAR" "$work/BC.jar"

# 2) 조각을 얹은 JVM에서 provider 체인을 실제로 읽는다.
#    -Djava.security.properties=<파일>(등호 하나)은 JDK의 java.security에 **덮어쓰기 병합**한다 —
#    노드에서 조각을 병합해 적용하는 것과 같은 효과라, 시스템 파일을 건드리지 않고 확인할 수 있다.
cat >"$work/Show.java" <<'EOF'
import java.security.*;
public class Show {
  public static void main(String[] a) throws Exception {
    System.out.println("── registered provider order (after the fragment) ──");
    int i = 1;
    for (Provider p : Security.getProviders()) System.out.printf("  %d. %s %s%n", i++, p.getName(), p.getVersionStr());
    System.out.println("── is the target algorithm actually provided ──");
    for (String alg : new String[]{"ML-KEM", "ML-KEM-768", "ML-DSA"}) {
      String where = null;
      for (Provider p : Security.getProviders())
        for (Provider.Service s : p.getServices())
          if (s.getAlgorithm().equalsIgnoreCase(alg)) { where = p.getName() + " (" + s.getType() + ")"; break; }
      System.out.printf("  %-12s %s%n", alg, where == null ? "none" : "→ " + where);
    }
    System.out.println("── TLS negotiation groups ──");
    System.out.println("  jdk.tls.namedGroups = " + Security.getProperty("jdk.tls.namedGroups"));
  }
}
EOF

# 조각 없이도 한 번 돌린다 — **무엇이 달라졌는가**는 대조해야만 보인다.
: >"$work/empty.security"

if command -v java >/dev/null 2>&1; then
	jvm() { java "$@"; }
	FRAG="$work/pqcota.security"; NONE="$work/empty.security"; JARP="$work/BC.jar"; SRC="$work/Show.java"
elif command -v docker >/dev/null 2>&1; then
	echo "(no local JDK → using eclipse-temurin:21-jdk in Docker)"
	jvm() { docker run --rm -v "$work:/w" -w /w eclipse-temurin:21-jdk java "$@"; }
	FRAG=/w/pqcota.security; NONE=/w/empty.security; JARP=/w/BC.jar; SRC=/w/Show.java
else
	echo "✗ neither java nor docker is available — one of them is required." >&2
	exit 1
fi

echo "▶ case $CASE — apply the fragment and check it in a JVM"
echo
jvm -Djava.security.properties="$FRAG" -cp "$JARP" "$SRC" | tee "$work/after.txt"

# ★ 자리 대체를 눈으로 — security.provider.N은 끼워 넣지 않고 그 자리를 차지한다.
jvm -Djava.security.properties="$NONE" -cp "$JARP" "$SRC" >"$work/before.txt" 2>/dev/null || true
echo
echo "── what the fragment did to the provider list (without → with) ──"
# diff는 차이가 있으면 1로 끝난다 — pipefail이 켜져 있어 그 값이 파이프라인 결과가 된다.
# 그래서 결과를 먼저 담고 나서 판단한다(그러지 않으면 차이가 있는데도 "차이 없음"이 함께 찍힌다).
changed=$(diff <(sed -n '/registered provider order/,/is the target algorithm/p' "$work/before.txt") \
	<(sed -n '/registered provider order/,/is the target algorithm/p' "$work/after.txt") |
	sed 's/^</  dropped:/; s/^>/  added:  /' | grep -E 'dropped|added' || true)
[ -n "$changed" ] && echo "$changed" || echo "  (no difference)"
echo
echo '※ security.provider.2 **takes over slot 2 instead of inserting** — the comparison above shows the'
echo "   provider that used to be number 2 dropping off. Its services (RSA on JDK defaults) move to the"
echo "   new provider's implementation. That is the blast radius of a global change."
