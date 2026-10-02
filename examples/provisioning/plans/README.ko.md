[English](README.md) · 한국어

# plans/: 샘플 계획(`FinalizedPlan`)

`pqcota-provision`의 **입력**입니다. 이 리포지터리는 계획을 만들지 않고 읽기만 하므로, 쓰려면 사용자가 계획을 씁니다. 여기서 가장 가까운 샘플을 골라 `targetNodeId`, 경로, provider를 자신의 것으로 바꾸세요.

```bash
./run.sh openssl-3.5-config-only          # the example runner: it walks through the signing too
```

**계획은 승인되어야 생성기를 통과합니다.** 확인할 키가 없으면 거부하므로, 직접 호출할 때는 서명하고
공개 키를 등록합니다. 실행기는 이 세 가지를 대신 해 줍니다.

```bash
eval "$(pqcota-keygen | grep '^PQCOTA_')"
PQCOTA_APPROVAL_KEY="$PQCOTA_SIGN_KEY" \
  pqcota-approve --approver reviewer-1 plans/openssl-3.5-config-only.json > plan.signed.json
PQCOTA_APPROVAL_KEYS="reviewer-1=$PQCOTA_VERIFY_KEY" \
  pqcota-provision --level l2 plan.signed.json > provision.yml
```

**무엇이 생성되는지**는 케이스별로 [상위 README](../README.ko.md)에 있습니다. 이 문서는 **JSON 자체가 어떻게 생겼는지**를 다룹니다.

## 샘플 한눈에 보기

샘플을 가르는 것은 `kind`(조치 종류)와 `cryptoRuntime`입니다. 조합이 요구하는 다른 필드만 채우세요.

| 파일 | `cryptoRuntime` | `kind` | 눈에 띄는 필드 |
|---|---|---|---|
| [`openssl-3.5-config-only`](openssl-3.5-config-only.json) | OPENSSL | `CONFIG_ONLY` | 없음. 설정 한 줄이면 되므로 provider를 지정하지 않습니다 |
| [`openssl-3.0-provider-inject`](openssl-3.0-provider-inject.json) | OPENSSL | `PROVIDER_INJECT` | `providerChoice: oqsprovider` |
| [`openssl-1.1.1-fork-replace`](openssl-1.1.1-fork-replace.json) | OPENSSL | `FORK_REPLACE` | 없음. 설정으로 할 수 없어 주석만 남습니다 |
| [`jca-native-config-only`](jca-native-config-only.json) | JCA | `CONFIG_ONLY` | 없음 |
| [`jca-provider-inject-bc`](jca-provider-inject-bc.json) | JCA | `PROVIDER_INJECT` | `providerChoice: BC` |
| [`jca-fips-bcfips`](jca-fips-bcfips.json) | JCA | `PROVIDER_INJECT` | `providerChoice: BCFIPS`: 등록 클래스가 바뀝니다 |
| [`jca-eol-jdk-upgrade`](jca-eol-jdk-upgrade.json) | JCA | `JDK_UPGRADE` | 없음. 출력이 없습니다 |
| [`custom-openssl-provider`](custom-openssl-provider.json) | OPENSSL | `PROVIDER_INJECT` | `providerChoice: acme-pqc`: 알려지지 않은 이름 |
| [`custom-jca-provider`](custom-jca-provider.json) | JCA | `PROVIDER_INJECT` | `providerClass`에 적은 FQCN |
| [`custom-jca-missing-class`](custom-jca-missing-class.json) | JCA | `PROVIDER_INJECT` | 같은 케이스에서 `providerClass`를 **뺀 것** |
| [`l3-activation-hooks`](l3-activation-hooks.json) | JCA | `PROVIDER_INJECT` | **`activation`**, 훅 넷 모두 |
| [`l3-hooks-missing`](l3-hooks-missing.json) | JCA | `PROVIDER_INJECT` | `activation`이 **없음**: 일어나지 않는 일을 알립니다 |
| [`signature-algorithm`](signature-algorithm.json) | OPENSSL | `CONFIG_ONLY` | `targetAlgorithm`이 **서명**(ML-DSA)입니다 |
| [`00-basic-two-actions`](00-basic-two-actions.json) | 둘 다 | `PROVIDER_INJECT` ×2 | 노드 둘: 노드마다 플레이로 나뉩니다 |

## 필드

### 계획의 최상위

| 필드 | 필수 | 하는 일 |
|---|---|---|
| `id` | ✅ | 계획 식별자입니다 |
| `status` | ✅ | 판정이 끝난 계획은 **`PLAN_STATUS_IN_REVIEW`로 도착합니다.** `pqcota-approve`가 첫 승인에서 이를 `FINALIZED`로 올리고, 생성기는 **`FINALIZED`가 아니면 거부합니다.** 승인되지 않은 계획의 배포를 막는 게이트입니다. `DRAFT`는 승인 단계에서 거부합니다 |
| `scope` | | 계획의 범위를 나타내는 라벨입니다(예: `ring-0`) |
| `approvalSignatures` | | **빈 채로 도착합니다.** 실행 승인이 놓이는 자리이므로 판정한 쪽이 채우지 않습니다. [`pqcota-approve`](../../../cmd/README.ko.md)가 승인자의 키로 서명해 넣습니다. 생성기는 **비어 있으면 거부합니다.** `IN_REVIEW`인데 값이 있으면 손상으로 보고 거부합니다. 승인은 「이 상태에 대한 서명」이기 때문입니다 |
| `derivedFromSnapshotId` | | **이전 버전을 위한 호환 경로입니다.** 계획 전체가 나온 스냅샷 하나의 id입니다. 액션에 `evidenceSources`가 있으면 그쪽이 우선하고 이 값은 읽지 않습니다. 둘 다 비어 있으면 경고합니다(실행은 되지만 이력에 근거가 남지 않습니다) |
| `rulesetVersion` | | 계획을 만든 규칙의 버전입니다. 비어 있으면 경고합니다 |
| `finalizedAt` | | **빈 채로 도착합니다.** 첫 승인이 시각을 찍습니다. `IN_REVIEW`인데 값이 있으면 손상으로 보고 거부합니다. 승인된 계획에서 비어 있으면 생성기가 경고합니다 |
| `actions` | ✅ | 액션 목록입니다. **플레이는 노드마다 나뉩니다** |

**샘플은 승인되지 않았습니다.** 판정이 끝난 계획의 모양(`IN_REVIEW`, 승인 필드와 확정 시각이 빈 상태)을 유지하므로, 샘플을 그대로 생성기에 넣으면 「승인 서명 없음」으로 거부됩니다. 올바른 동작입니다. 샘플은 승인된 계획이 아니라 액션의 모양을 보여 줍니다. 예전에는 샘플이 `FINALIZED`였고 `"reviewer:alice"` 같은 라벨을 달고 있었는데, **승인 필드에 라벨이 있어서 구조 게이트를 통과한 것처럼 보였습니다.** 실제로 배포하는 계획은 `pqcota-approve`로 서명하며, 그 명령이 상태를 올리고 시각을 찍습니다.

### 액션(`actions[]`)

| 필드 | 필수 | 하는 일 |
|---|---|---|
| `id` | ✅ | 액션 식별자입니다. 경고 메시지가 이 값으로 어느 액션인지 가리킵니다 |
| `targetNodeId` | ✅ | 이 액션이 가는 노드입니다. 플레이북의 `hosts:`가 됩니다. **비어 있으면 거부합니다.** 빈 항목은 아무 데도 닿지 않는 플레이를 만들기 때문입니다 |
| `findingId` | | 근거가 되는 관측입니다. 인벤토리의 자산과 이어집니다. 비어 있으면 경고합니다 |
| `evidenceSources[]` | | **이 액션의 근거**: 어느 발견 항목이 어느 스냅샷 상태에서 왔는지입니다. `{findingId, snapshot: {sourceNodeId, snapshotId \| content: {formatVersion, digest, rulesetVersion}}}`. `sourceNodeId`는 이력이 그 스냅샷을 저장한 이름(봉투의 노드)이므로 `targetNodeId`와 다를 수 있습니다. 보통 하나이며 주된 근거가 먼저 옵니다. **모양이 잘못되면 `--dsn` 없이도 불완전(종료 3)이고**, `--dsn`을 주면 생성기가 이력에서 실제로 찾아 기록에 남깁니다. 찾지 못하거나 찾은 스냅샷에 그 발견 항목이 없어도 불완전입니다. 내용 지문 형식은 [`openssl-3.5-config-only`](openssl-3.5-config-only.json)가 보여 줍니다 |
| `cryptoRuntime` | ✅ | `CRYPTO_RUNTIME_OPENSSL` \| `CRYPTO_RUNTIME_JCA`: 설정 조각의 문법을 정합니다 |
| `kind` | ✅ | 조치 종류입니다(아래). **`UNSPECIFIED`는 거부합니다.** 생성기가 분기할 수 없고, 계획이 말하지 않은 것에 「설정으로 넣을 수 없다」고 하는 조각이 나가게 되기 때문입니다 |
| `targetAlgorithm` | | 목표 알고리즘입니다. KEM이면 하이브리드 그룹 줄이 나가고, **서명이면 그룹 줄 대신 주석이 나갑니다** |
| `providerChoice` | `PROVIDER_INJECT`이면 | 주입할 provider의 이름입니다. **이 값이 파일 이름이 됩니다**(아래) |
| `providerClass` | JCA 사용자 정의 provider이면 | `java.security`에 적을 FQCN입니다. **없으면** 알려진 이름(BC, BCFIPS)만 정해지고, 그 밖의 것은 자리표시자 + 경고가 나옵니다 |
| `rollbackNote` | | 되돌림에 관한 메모입니다 |
| `activation` | `--level l3`이면 | 활성화 훅입니다(아래) |

### `providerChoice`: 이름이 파일 이름이 됩니다

| | OpenSSL | JCA |
|---|---|---|
| 이 값이 되는 것 | `<name>.so`라는 파일 이름이며 `/opt/pqcota/<name>.so`에 놓입니다 | `<name>.jar`라는 파일 이름 **+ 등록 클래스를 정합니다** |
| 아무 이름이나 되는가 | **됩니다.** 설정은 경로만 참조하므로 이름은 자유입니다 | **안 됩니다.** 알려진 이름이 아니면 `providerClass`(FQCN)도 주어야 합니다 |
| 이름이 뜻하는 클래스 | (해당 없음) | `BC` · `BCFIPS`(= `BC-FJA`) |
| 비어 있으면 | `provider.so` | `BC`로 취급합니다 |

JCA에서 이름이 알려진 것이 아니고 `providerClass`도 없으면, 등록 줄이 자리표시자 `<name: check the provider's documentation for the exact class name>`로 나가고 경고가 함께 나옵니다. 아무것도 지어내지 않는다는 뜻입니다. [`custom-jca-missing-class`](custom-jca-missing-class.json)가 그 케이스입니다.

#### 어떤 provider를 주입할 수 있는가

이름은 자유지만 **도구가 내보낼 수 있는 설정 조각의 모양은 하나뿐입니다.** `activate = 1` + `module = path`입니다.
그 모양으로 충분한 provider는 그대로 동작하고, 다른 모양이 필요한 provider는 아직 내보낼 수 없습니다.

| 후보 | 동작하는가 |
|---|---|
| [oqsprovider](https://github.com/open-quantum-safe/oqs-provider) | ✅ 지금의 모양으로 충분합니다. 실물로 확인했습니다(2026-08-06, OpenSSL 3.0.13) |
| [wolfProvider](https://github.com/wolfSSL/wolfProvider) | ◐ 충분해 보이지만 실물로는 확인하지 못했습니다 |
| OpenSSL 자체의 `fips` 모듈 | ❌ 모양이 다릅니다. `fipsinstall`이 만드는 `fipsmodule.cnf`를 끌어와야 합니다 |
| [pkcs11-provider](https://github.com/openssl-projects/pkcs11-provider) | ❌ 모양이 다릅니다. 드라이버 경로 같은 키가 더 필요합니다 |
| JCA 사용자 정의 | ✅ `providerClass`(FQCN)가 이미 일반 경로입니다 |

❌인 것을 받으려면 도구가 그 모양을 알아야 합니다.
모듈 파일 자체는 어느 경우에나 사용자가 구해 [`files/`](../files/README.ko.md)에 두는 것입니다.

**이름은 Ansible 변수 이름으로도 쓰입니다.** `pqcota_module_src_<name>`과 `pqcota_module_sha256_<name>`에서 `<name>`은 이름에서 **영숫자 외의 모든 문자를 `_`로 바꾼 것**입니다(`acme-pqc` → `acme_pqc`). Ansible 변수 이름에는 하이픈을 쓸 수 없기 때문입니다. 하이픈을 그대로 둔 변수는 **인식되지 않고 아무 오류 없이 무시됩니다**(무결성 점검 전체가 건너뛰어집니다).

### `kind`: 무엇이 생성되는지를 정합니다

| 값 | 출력 |
|---|---|
| `REMEDIATION_KIND_CONFIG_ONLY` | 설정 조각만입니다. provider는 배치하지 않습니다 |
| `REMEDIATION_KIND_PROVIDER_INJECT` | 배치한 provider 모듈 + sha256 게이트 + 그 모듈을 참조하는 설정 조각입니다 |
| `REMEDIATION_KIND_FORK_REPLACE` · `JDK_UPGRADE` · `APP_RECONFIG` · `REBUILD` · `DECOMMISSION` | **그 액션에는 아무것도 배치하지 않습니다.** 설정으로 할 수 없는 액션이므로 주석만 남습니다. `# action a1(…): cannot be delivered through config — manual step`. 플레이의 공통 뼈대(디렉터리 생성)는 여전히 나옵니다 |

### `activation`: L3 훅

명령은 사용자가 쓰고, **펼치는 순서는 생성기가 정합니다**(적용: `pre` → 배치 → `activate` → `restart`, 되돌림: `pre` → 비활성화 → 제거 → `restart`).

```json
"activation": {
  "pre":        "systemctl stop payments.service",
  "activate":   "…a command that makes the app see the new provider…",
  "deactivate": "…a command that undoes activate…",
  "restart":    "systemctl start payments.service"
}
```

**비어 있는 훅은 지어내지 않습니다.** 없으면 그 작업을 만들지 않고 일어나지 않는 일을 stderr로 알립니다. `l3-hooks-missing`이 그 케이스입니다.
