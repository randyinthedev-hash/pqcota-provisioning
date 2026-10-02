[English](README.md) · 한국어

# examples/provisioning: 계획마다 무엇이 생성되는지 보기

아래 케이스는 같은 명령을 두고 **계획만 바꿀** 때 출력이 어떻게 달라지는지 보여 줍니다. 케이스는 [`plans/`](plans/README.ko.md)의 JSON 파일 하나씩입니다.

```bash
./examples/provisioning/run.sh                          # list the cases + a default run
./examples/provisioning/run.sh openssl-3.0-provider-inject
./examples/provisioning/run.sh custom-jca-provider --rollback
./examples/provisioning/run.sh --all
```

> 전체 흐름과 근거는 [전환물 생성](../../README.ko.md)에 있습니다. 여기는 **케이스별 실제 출력**을 보는 곳입니다.

> ⚠️ **예제는 플레이북을 생성합니다.** `oqsprovider.so`, `acme-jce.jar` 같은 **provider 모듈 바이너리는 이 리포지터리에 없습니다**(아키텍처마다 다르고, 더미는 동작하는 것처럼 보이므로 해롭습니다). 생성된 플레이북을 실제로 실행하려면 자신의 모듈을 [`files/`](files/README.ko.md)에 두거나 `-e pqcota_module_src_<name>=`로 경로를 넘기세요.
>
> **실제 provider로 끝까지 보려면**: 해시를 고정해 하나를 받아 오고, 생성된 조각이 정말 provider를 등록하는지 실제 JVM에서 확인하세요.
>
> ```bash
> ./examples/provisioning/files/fetch-example-provider.sh   # BC 1.85 (sha256 checked)
> ./examples/provisioning/files/verify-registration.sh      # compare the provider list before and after the fragment
> ```
>
> **OpenSSL에서 같은 확인**은 데모의 선택 단계가 합니다. `DEMO_REAL_PROVIDER=1 ./demo/scripts/demo.sh`입니다. 실제 oqsprovider를 빌드해 3.0–3.4 노드에 배치하고 활성화한 뒤 `openssl list`로 전후를 측정합니다([데모 README](https://github.com/randyinthedev-hash/pqcota/blob/main/demo/README.ko.md#선택-단계-실물-provider로-마지막-한-걸음demo_real_provider1)).

## OpenSSL: 버전이 조치를 정합니다

| 케이스 | 관측된 상황 | `kind` | 생성되는 것 |
|---|---|---|---|
| [`openssl-3.5-config-only`](plans/openssl-3.5-config-only.json) | 3.5+ 네이티브 PQC | `CONFIG_ONLY` | `Groups = X25519MLKEM768:x25519` **한 줄.** provider 모듈은 없습니다 |
| [`openssl-3.0-provider-inject`](plans/openssl-3.0-provider-inject.json) | 3.0–3.4(provider API가 있음) | `PROVIDER_INJECT` | `/opt/pqcota/oqsprovider.so`에 놓은 모듈 + 그 **절대 경로를 참조하는** 설정 |
| [`openssl-1.1.1-fork-replace`](plans/openssl-1.1.1-fork-replace.json) | 1.1.1 · 1.0.2(provider API 없음) | `FORK_REPLACE` | **아무것도 배치하지 않습니다.** 설정으로 전달할 수 없다는 주석이 남습니다. 수동 단계입니다 |

**요점**: 버전이 오래될수록 도구가 해 줄 수 있는 일이 줄어듭니다. 1.1.1은 표시 없이 빠지지 않습니다. **수동인 까닭이 플레이북에 남습니다.**

## JVM/JCA: provider 상황이 조치를 정합니다

| 케이스 | 관측된 상황 | `kind` · `providerChoice` | 생성되는 것 |
|---|---|---|---|
| [`jca-native-config-only`](plans/jca-native-config-only.json) | JDK 네이티브 PQC | `CONFIG_ONLY` | `jdk.tls.namedGroups=…` 한 줄. 등록되는 provider는 없습니다 |
| [`jca-provider-inject-bc`](plans/jca-provider-inject-bc.json) | PQC가 없는 provider 체인 | `PROVIDER_INJECT` · `BC` | 배치한 JAR + `security.provider.2=org.bouncycastle.jce.provider.BouncyCastleProvider` |
| [`jca-fips-bcfips`](plans/jca-fips-bcfips.json) | **규제 대상 자산** | `PROVIDER_INJECT` · `BCFIPS` | 같은 흐름이지만 **등록 클래스가 다릅니다**(`BouncyCastleFipsProvider`). FIPS용 등록 경로입니다 |
| [`jca-eol-jdk-upgrade`](plans/jca-eol-jdk-upgrade.json) | EOL JDK | `JDK_UPGRADE` | **아무것도 배치하지 않습니다.** 수동 단계 주석이 남습니다 |

**요점**: `providerChoice`가 **등록 클래스 이름을 정합니다.** 규제에 따라 BC로 할지 BC-FJA로 할지는 계획 단계에서 내리는 판단입니다.

> JCA `PROVIDER_INJECT`는 항상 **우선순위 2**로 등록합니다. JCA는 목록에서 앞선 provider가 먼저 처리하므로, 더 뒤에 두면 JAR이 있어도 **아무것도 달라지지 않습니다.**

## 사용자 정의 provider

| 케이스 | 보여 주는 것 |
|---|---|
| [`custom-openssl-provider`](plans/custom-openssl-provider.json) | 자체 빌드라고 가정한 `acme-pqc.so`입니다. 모듈의 절대 경로, `pqcota_module_src_acme_pqc` 변수, sha256 게이트가 자동으로 생성됩니다 |
| [`custom-jca-provider`](plans/custom-jca-provider.json) | 자체 빌드라고 가정한 `acme-jce.jar`입니다. **`providerClass`에 FQCN을 적었으므로** 계획만으로 완전합니다 |
| [`custom-jca-missing-class`](plans/custom-jca-missing-class.json) | 같은 케이스에서 `providerClass`만 뺀 것입니다. **자리표시자 + 무엇을 할지 적은 메모**가 나옵니다 |

둘 다 **계획만으로 완전합니다.** 다만 JCA에는 필드가 하나 더 필요합니다.

```json
{"providerChoice": "acme-jce", "providerClass": "com.acme.jce.AcmeProvider"}
```

```
# OpenSSL — only the path is needed (the generator decides it)
module = /opt/pqcota/acme-pqc.so

# JCA — the FQCN is needed (the plan has to say it)
security.provider.2=com.acme.jce.AcmeProvider
```

**필드가 하나 더 필요한 이유**: OpenSSL은 **경로**만 있으면 되고, 그 경로는 플레이북이 정합니다. JCA는 java.security에 적을 **FQCN**이 필요한데, 패키지 구조는 벤더마다 달라 provider 이름에서 유도할 수 없습니다. BC와 BC-FJA만 알려져 있어 자동으로 채웁니다. 그 밖의 것은 계획이 말해야 합니다.

말하지 않으면 추측하지 않고 이것을 남깁니다(`custom-jca-missing-class`).

```
# ⚠ the class name below is a placeholder — replace it with the exact class from your provider build, or
#   put the FQCN in the plan's provider_class and it is filled in automatically.
security.provider.2=<acme-jce: check the provider's documentation for the exact class name>
```

> **JVM이 JAR을 찾으려면** 클래스패스에 있어야 합니다. 방법은 JDK 세대마다 다르므로(`lib/ext` 확장 메커니즘은 **JDK 9에서 제거되었습니다**), 생성된 조각이 두 방법을 모두 설명합니다.

모듈 전달 절차(컨트롤러 → 대상, `files/` 규칙, sha256)는 [cmd · 적용하기](../../cmd/README.ko.md#적용하기)를 보세요.

## 경계 케이스

| 케이스 | 보여 주는 것 |
|---|---|
| [`signature-algorithm`](plans/signature-algorithm.json) | `targetAlgorithm`이 **서명**(ML-DSA)이면 그룹 줄이 KEM 그룹이 아니라 **주석으로** 나옵니다. 추측으로 채우는 것은 없습니다 |
| [`00-basic-two-actions`](plans/00-basic-two-actions.json) | 계획 하나에 노드 둘: **노드마다 플레이**로 나뉘고, 각 플레이는 자기 액션만 받습니다 |

## 게이트 시험하기

아무 계획이나 `"status"`를 `"PLAN_STATUS_DRAFT"`로 바꿔 실행하면 거부됩니다(종료 1).

```
refused: plan not finalized — refusing to provision. Only a finalized plan justifies provisioning.
```

`approvalSignatures`를 비워도 같은 결과입니다.

## L3: 활성화와 재시작

L1/L2는 파일을 둘 뿐입니다. **L3은 둔 것이 실제로 참조되게 하고 프로세스를 다시 시작합니다.** 활성화 지점은 환경마다
다르므로(systemd drop-in, include 디렉터리, 자체 시작 스크립트) 도구는 추측하지 않습니다.
계획의 `activation`에 명령을 쓰면 생성기가 그것을 **의미 있는 순서로** 펼칩니다.

```bash
./examples/provisioning/run.sh l3-activation-hooks             # forward
./examples/provisioning/run.sh l3-activation-hooks --rollback  # reverse order
```

| 훅 | 언제 | 정방향 | 되돌림 |
|---|---|---|---|
| `pre` | 액션 전에 내려 둘 것 | ① | ① |
| (없음) | 모듈과 설정 배치 | ② | ③(제거) |
| `activate` | 조각이 참조되게 함 | ③ | (없음) |
| `deactivate` | `activate`의 역 | (없음) | ② |
| `restart` | 새 provider를 로드 | ④ | ④ |

따라서 정방향은 `pre → 배치 → activate → restart`이고, 되돌림은 `pre → deactivate → 제거 → restart`입니다.
한 노드에 액션이 여럿이어도 **같은 명령은 한 번만 나갑니다.** 액션마다 재시작하면 서비스가 여러 번 흔들리고, 활성화
사이에 낀 재시작은 일부만 적용된 채로 서비스를 올리게 됩니다.

`l3-activation-hooks`는 JCA 케이스이므로, 훅이 **JAR을 둠 ≠ 로드됨**이라는 함정을 막는 모습을 보여 줍니다. 그 안의 경로와
변수 이름은 예시일 뿐이므로 자신의 앱이 시작되는 방식에 맞게 고쳐 쓰세요.

### 훅이 없으면: 지어내지 않습니다

```bash
./examples/provisioning/run.sh l3-hooks-missing
```

훅만 뺀 같은 계획입니다. 생성기는 **활성화 명령을 만들지 않고**, 대신 무엇이 일어나지 *않는지* stderr로 알립니다.
`activate`가 없으면 조각이 놓이기만 하고 참조되게 하지는 않으며, `restart`가 없으면 새
provider가 로드되지 않을 수 있고, `deactivate`가 없으면 되돌림이 활성화를 되돌리지 못합니다.

## 되돌림

아무 케이스에나 `--rollback`을 붙이면 **정방향이 둔 것을 제거하는** 플레이북이 나옵니다. 원래 설정은 덮어쓴 적이 없으므로 제거만으로 이전 상태로 돌아갑니다.

```bash
./examples/provisioning/run.sh custom-openssl-provider --rollback
```

## `--dsn`과 함께

이력에서 변경 전 발견 항목을 읽어 **액션 전의 상태**와 **영향받는 앱**을 추가 전용 기록으로 남깁니다. 관측이 먼저 저장소에 적재되어 있어야 하므로, 처음부터 끝까지의 흐름은 [demo/](https://github.com/randyinthedev-hash/pqcota/tree/main/demo)가 보여 줍니다.

## 여기에 없는 것

**동적 적용**(재시작 없이 실행 중인 프로세스에 주입)은 하지 않습니다. **플릿 오케스트레이션**
(드레인, 롤링, 헬스 체크 게이트)은 하지 않습니다. 명령 지도: [cmd/README](../../cmd/README.ko.md).
