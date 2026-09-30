#!/usr/bin/env bash
# examples/provisioning — 확정 계획(plans/*.json)에서 Ansible 플레이북을 생성한다.
#
#   ./run.sh                        케이스 목록 + 기본 케이스 실행
#   ./run.sh <케이스>                그 케이스만
#   ./run.sh <케이스> --rollback     역방향(제거) 플레이북
#   ./run.sh --all                  전 케이스 순서대로
#
# 전제: Go 툴체인만. Postgres 없으면 플레이북만(before 캡처·레코드 영속은 --dsn 필요).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
cd "$ROOT"

# 케이스 = plans/<이름>.json | 무엇을 보여주는가 | (선택)배포 수준. 수준은 케이스의 성질이라
# 읽는 사람이 플래그를 맞혀야 하지 않게 여기 적는다(기본 l2).
CASES='00-basic-two-actions|two nodes in one plan (OpenSSL + JCA) — split into a play per node
openssl-3.5-config-only|OpenSSL 3.5+ native: config only, no provider module
openssl-3.0-provider-inject|OpenSSL 3.0-3.4: keep the version, inject a provider module
openssl-1.1.1-fork-replace|OpenSSL 1.1.1: no provider API → nothing is generated, recorded as a manual step
jca-native-config-only|JDK-native PQC: namedGroups only, no provider registered
jca-provider-inject-bc|JCA: BouncyCastle injection — the class name is resolved automatically
jca-fips-bcfips|regulated asset: routed to BC-FJA (FIPS) — a different registration class
jca-eol-jdk-upgrade|end-of-life JDK: impossible through config → recorded as a manual step
custom-openssl-provider|custom OpenSSL provider: absolute path, per-provider source variable, sha256
custom-jca-provider|custom JCA provider: the FQCN is named in providerClass → the plan alone is enough
custom-jca-missing-class|the same, with providerClass removed — a placeholder plus guidance
signature-algorithm|signature algorithm (ML-DSA): not a KEM group, so the group line is commented out
l3-activation-hooks|L3: the plan hooks in meaningful order — pre → stage → activate → restart (rollback reverses it)|l3
l3-hooks-missing|L3 without hooks: no command is invented; it reports what will not happen|l3'

list() {
  echo "cases (plans/<name>.json):"
  printf '%s\n' "$CASES" | while IFS='|' read -r name desc lvl; do
    printf "  %-28s %s%s\n" "$name" "$desc" "${lvl:+  [--level $lvl]}"
  done
}

# 승인 검증은 **기본으로 닫혀 있다.** 예제도 실제로 서명해 그 경로를 그대로 보인다 — 여기서
# --allow-unverified-approvals로 문을 열어 두면 예제가 가르치는 것이 실제 기본값과 달라진다.
# 키쌍은 한 번 만들어 이 실행 안에서만 쓴다.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
# `go run`은 대상의 종료 코드를 1로 감싼다 — 빈칸이 남은 계획의 3을 그대로 보려면 바이너리를
# 직접 불러야 한다. 케이스마다 다시 컴파일하지 않는 이점도 있다.
go build -o "$TMP/keygen"   github.com/randyinthedev-hash/pqcota-common/cmd/pqcota-keygen
go build -o "$TMP/approve"  ./cmd/pqcota-approve
go build -o "$TMP/provision" ./cmd/pqcota-provision

APPROVER_KEYS="$("$TMP/keygen")"
APPROVER_PRIV="$(printf '%s\n' "$APPROVER_KEYS" | sed -n 's/^PQCOTA_SIGN_KEY=//p')"
APPROVER_PUB="$(printf '%s\n' "$APPROVER_KEYS" | sed -n 's/^PQCOTA_VERIFY_KEY=//p')"

desc_of() { printf '%s\n' "$CASES" | awk -F'|' -v n="$1" '$1==n{print $2}'; }
# 케이스가 수준을 정한다 — 안 적혀 있으면 l2.
level_of() { printf '%s\n' "$CASES" | awk -F'|' -v n="$1" '$1==n{print ($3==""?"l2":$3)}'; }

run_case() {
  local name="$1"; shift
  local file="$HERE/plans/$name.json"
  if [ ! -f "$file" ]; then
    echo "no such case: $name" >&2
    list >&2
    exit 2
  fi
  echo "════════════════════════════════════════════════════════════════"
  echo "▶ $name"
  echo "  $(desc_of "$name")"
  echo "════════════════════════════════════════════════════════════════"
  # 계획에 실제 승인 서명을 붙인다. 파일에 적힌 `reviewer:alice`는 서명이 아니라 이름표라,
  # 검증하는 쪽이 "아무것도 증명하지 않는다"고 따로 알린다 — 그 대비도 예제의 일부다.
  local signed="$TMP/$name.signed.json"
  PQCOTA_APPROVAL_KEY="$APPROVER_PRIV" \
    "$TMP/approve" --approver reviewer-alice "$file" > "$signed"

  # 빈칸이 남은 계획은 종료 상태 3으로 끝난다. 여기서는 **의도한 케이스**라 멈추지 않고 알린다.
  local st=0
  PQCOTA_APPROVAL_KEYS="reviewer-alice=$APPROVER_PUB" \
    "$TMP/provision" --level "$(level_of "$name")" "$@" "$signed" || st=$?
  case "$st" in
    0) ;;
    3) echo "  ↑ 빈칸이 남아 종료 상태가 3이다 — 이 케이스가 보이려는 것이 그것이다." ;;
    *) echo "  ↑ 예상치 못한 종료 상태 $st" >&2; return "$st" ;;
  esac
  echo
}

case "${1:-}" in
  --all)
    printf '%s\n' "$CASES" | while IFS='|' read -r name _; do run_case "$name"; done
    ;;
  "")
    list
    echo
    echo "running the default case — for others: ./run.sh <name>"
    echo
    run_case 00-basic-two-actions
    echo "✅ all cases: ./run.sh --all · reverse: ./run.sh <case> --rollback"
    echo "   • before capture + persisted rollback record: add --dsn <postgres> (discovery must have ingested first — see demo/)"
    echo "   • change status to PLAN_STATUS_DRAFT and pqcota-approve refuses to sign it; skip approve and the FINALIZED gate refuses it."
    ;;
  *)
    run_case "$@"
    ;;
esac
