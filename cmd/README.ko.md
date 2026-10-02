[English](README.md) · 한국어

# cmd/: 전환물 생성의 진입점

전환물 생성 단계의 CLI(Go 바이너리)입니다. 확정된 계획에 **승인 서명을 붙이고**, 그 계획에서 **Ansible 플레이북을 생성하고**, **되돌림의 근거를 보존합니다.** 네 범주로 나뉩니다.

**플레이북의 내용과 순서는 모두 생성기가 정합니다.** 사용자가 하는 일은 그것을 자신의 Ansible로 실행하는 것입니다. 자체 원격 실행 엔진은 없습니다.

## ① 생성: 확정된 계획에서 플레이북 만들기

### `pqcota-provision`

```
pqcota-provision [--level l1|l2|l3] [--rollback] [--dsn <postgres>] <plan.json>
```

| 인자 · 옵션 | 하는 일 |
|---|---|
| `<plan.json>` | 확정된 계획(`FinalizedPlan`)입니다. **`PLAN_STATUS_FINALIZED`가 아니면 거부합니다** |
| `--level l1` | **스테이징만**: 모듈을 대상에 두는 데까지입니다 |
| `--level l2`(기본값) | **설치까지.** 모듈 배치 + 설정 조각 배치입니다 |
| `--level l3` | **활성화와 재시작까지.** 계획의 `activation` 훅(`pre`, `activate`, `deactivate`, `restart`)을 의미 있는 순서로 펼칩니다 |
| `--rollback` | 역방향 플레이북입니다. 정방향이 둔 파일을 제거합니다 |
| `--allow-incomplete` | 계획에 빈 곳이 있어도 **0으로 종료합니다.** 경고는 여전히 나옵니다 |
| `--allow-unverified-approvals` | 확인할 키가 없어도 **그대로 진행합니다.** 기본값은 거부입니다 |
| `--dsn <postgres>` | 이력에서 변경 전 발견 항목을 읽고, **변경 전 상태를 기록해** 추가 전용 기록으로 보존합니다. 형식은 [DSN](https://github.com/randyinthedev-hash/pqcota-discovery/blob/main/cmd/README.ko.md#pqcota-hosts)을 보세요 |

| 환경변수 | 하는 일 |
|---|---|
| `PQCOTA_APPROVAL_KEYS` | 쉼표로 구분한 `<approver>=<base64 public key>`입니다. 승인 서명마다 **그 승인자의 키로** 검증합니다. 하나라도 어긋나면 거부하고, 검증된 승인이 하나도 없어도 거부합니다. **비어 있어도 거부합니다** |
| `PQCOTA_REQUIRE_APPROVAL` | `1`은 그대로 받아들입니다. 이제 기본값과 같은 뜻이므로 바뀌는 것이 없습니다 |

**확인할 키가 없으면 거부합니다.** 승인은 책임이 놓이는 자리인데, 아무 문자열이나 그 자리를 채울 수 있다면 자리는 비어 있는 것과 같습니다. 예전에는 경고하고 통과시켰고, 이를 닫는 것은 `PQCOTA_REQUIRE_APPROVAL=1`을 따로 설정한 배포에서만 일어났습니다. 그래서 승인 무결성은 「닫을 수 있는 수단」으로 남고 기본 경로는 열려 있었습니다.

승인 검증을 건너뛰려면 **명령줄에 `--allow-unverified-approvals`를 써야 하며, 다른 수단은 없습니다.** 환경변수로도 열 수 있다면 무엇을 검증했는지가 셸 설정에 가려지고, 로그만으로는 이 출력이 검증된 승인 위에 서 있는지 알 수 없습니다. 열었을 때도 「검증하지 않음」은 그대로 출력됩니다. 검증한 것이 아니라 지나친 것입니다.

서명은 [`pqcota-approve`](#pqcota-approve)가 붙이고, 키 쌍은 `pqcota-common`의 [`pqcota-keygen`](https://github.com/randyinthedev-hash/pqcota-common/blob/main/cmd/README.ko.md#pqcota-keygen)이 만듭니다. [예제 실행기](../examples/provisioning/README.ko.md)가 바로 그 경로를 따라갑니다.

**`--level`은 계획이 말하지 않은 액션의 기본값입니다.** 위임 수준은 계약이 정의하는 **자산별 속성**(「결제 서버 = L2, 상태 없는 워커 = L3」)이므로, 액션이 `automation_level`을 밝히면 그것을 따릅니다. 그 값은 승인 서명이 덮으므로, 전역 플래그로 덮어쓰면 승인자가 서명한 위임 수준과 실제로 실행되는 수준이 어긋납니다.

플레이북은 stdout으로 나옵니다. `> provision.yml`로 받으세요.

**빈 곳이 있는 계획은 성공으로 끝나지 않습니다.** 출력은 여전히 나오지만 종료 상태는 **3**입니다. 산출물이 먼저 stdout으로 나가고 경고는 나중에 stderr로 나가므로, 종료 상태까지 0이라면 stderr를 모으지 않는 자동화에서 **불완전한 플레이북이 정상 출력으로 남습니다.** 막지 않는 이유는 손으로 채우는 것이 정당한 경로이기 때문입니다. 알고 넘어갈 때는 `--allow-incomplete`를 쓰세요.

| 종료 상태 | 뜻 |
|---|---|
| `0` | 출력이 나왔고 빈 곳이 없습니다. `--dsn`을 주면 액션마다 스냅샷 참조를 이력에서 찾아 기록에 남깁니다(`pqcota-records`가 `snapshot:` 줄로 보여 줍니다) |
| `1` | **거부**: 이 계획은 실행의 근거가 아닙니다(확정되지 않음, 승인 없음, 액션 없음, 승인을 검증할 수 없음, 승인 검증 실패). 플레이북은 한 줄도 나오지 않습니다 |
| `3` | **불완전**: 출력은 나왔지만 빈 곳이 있습니다. 목표 알고리즘, 활성화 훅, 추적 근거가 비어 있거나, **스냅샷 참조의 모양이 잘못되었거나**(`--dsn` 없이도 잡힙니다), **`--dsn`으로 찾았는데 이력에 없거나, 찾은 스냅샷에 그 발견 항목이 없습니다.** 어느 쪽인지는 stderr에 이름으로 적힙니다 |
| `2` | 사용법이 틀렸습니다 |

`--rollback`도 같은 점검을 거칩니다. 파일을 지우는 일은 목표 알고리즘과 관계없으므로 그 경고는 하지 않지만, `deactivate`와 `restart`가 없어 활성화를 되돌릴 수 없는 것과 무엇을 되돌리는지 짚어 볼 근거가 없는 것은 정방향과 똑같이 따집니다.

**`--level l3`에서 비어 있는 훅은 지어내지 않습니다.** 계획에 `activate`가 없으면 그 작업을 만들지 않고 **일어나지 않는 일을 stderr로 알립니다**(예를 들어 재시작 훅이 없으면 「새 provider가 로드되지 않을 수 있음」). 활성화하는 방법은 앱이 시작되는 방식에 달려 있고, 도구는 그것을 알 수 없습니다.

**`--dsn`이 하는 일은 기록이지 적용이 아닙니다.** 액션 *전*의 상태(module@version, 설정, provider 체인)를 기록하며, 나중에 되돌릴 때 무엇으로 돌아갈지 말하는 근거가 됩니다.

### 적용하기

```bash
pqcota-provision --level l2 plan.json > provision.yml
ansible-playbook -i targets.ini -e pqcota_module_sha256_oqsprovider=<sha256> provision.yml
```

관측에서 쓴 것과 같은 `targets.ini`를 쓰세요([`pqcota-hosts`](https://github.com/randyinthedev-hash/pqcota-discovery/blob/main/cmd/README.ko.md#pqcota-hosts)).

**도구는 provider 모듈을 제공하지 않습니다.** 플레이북은 컨트롤러의 `files/<module file>`을 대상에 복사하므로, 사용자가 그 파일을 거기에 두어야 합니다.

| 변수 | 하는 일 |
|---|---|
| `pqcota_module_src_<provider>` | 그 모듈의 컨트롤러 로컬 경로입니다. 없으면 `pqcota_module_src`, 그것도 없으면 `files/<module file>`입니다 |
| `pqcota_module_sha256_<provider>` | **무결성 게이트**: 배치한 뒤 대상에서 sha256을 측정해 다르면 멈춥니다. 없으면 `pqcota_module_sha256`, 둘 다 없으면 점검을 건너뜁니다 |

`<provider>`는 계획의 `providerChoice`에서 **영숫자 외의 모든 문자를 `_`로 바꾼 것**입니다(`acme-pqc` → `acme_pqc`). Ansible의 변수 이름 규칙이기 때문입니다. 하이픈을 그대로 두고 주면 **변수가 인식되지 않고 점검은 아무 오류 없이 건너뜁니다.**

해시는 **복사한 뒤 대상에서** 측정합니다. 컨트롤러의 원본이 아니라 노드에 실제로 놓인 파일을 측정하므로 전송 중 손상과 경로 실수도 잡힙니다. 어긋나면 그 노드에서 멈춥니다.

sha256을 주기를 권합니다. 이것은 대상에서 암호 연산을 수행할 네이티브 코드를 심는 일이므로 **심은 것을 고정할 방법**이 필요합니다. 주지 않아도 오류는 아닙니다. **점검 작업 전체를 건너뜁니다.**

### 되돌리기

```bash
pqcota-provision --level l2 --rollback plan.json > provision-rollback.yml
ansible-playbook -i targets.ini provision-rollback.yml
```

적용은 원본을 덮어쓰지 않고 파일을 *더하므로*, 더한 것을 제거하면 복원입니다. L3에서는 `deactivate` 훅이 활성화도 되돌립니다.

## ② 승인: 계획에 서명 붙이기

### `pqcota-approve`

```
pqcota-approve --approver <id> <plan.json>
```

| 인자 · 옵션 | 하는 일 |
|---|---|
| `--approver <id>` | 승인자 id입니다. **`:`는 쓸 수 없습니다.** 서명 문자열의 구분자입니다 |
| `env PQCOTA_APPROVAL_KEY` | base64 ed25519 **개인 키**이며, [`pqcota-keygen`](https://github.com/randyinthedev-hash/pqcota-common/blob/main/cmd/README.ko.md#pqcota-keygen)이 만든 것입니다 |

서명된 계획은 stdout으로 나옵니다. 서명 문자열의 형식은 `<approver>:ed25519:<base64>`이며, **승인 서명 자체를 뺀 계획 전체**를 덮습니다.

**첫 승인이 계획을 확정합니다.** 판정이 끝난 계획은 `status=IN_REVIEW`에 승인 필드와 확정 시각이 빈 채로 도착합니다. 이 명령은 상태를 `FINALIZED`로 올리고 `finalized_at`을 찍은 뒤 **그 다음에** 서명합니다. 서명이 둘 다 덮으므로, 순서를 바꾸면 방금 한 서명이 깨집니다. 두 번째 승인부터는 아무것도 바꾸지 않고 서명만 더합니다.

| 도착하는 상태 | 하는 일 |
|---|---|
| `IN_REVIEW` | 승인과 확정 시각이 **없어야 합니다.** 있으면 손상으로 보고 거부합니다. 액션이 있어야 하고, 액션마다 대상 노드와 종류가 있어야 합니다. 통과하면 `FINALIZED`로 올리고 시각을 찍고 서명합니다 |
| `FINALIZED` | 승인과 확정 시각이 **모두 있어야 합니다.** 하나라도 없으면 손상으로 보고 거부합니다. 액션 구조는 `IN_REVIEW`와 똑같이 점검합니다. 온전하면 서명만 더합니다 |
| `DRAFT` · `UNSPECIFIED` | 거부합니다 |

거부하면 stdout으로는 아무것도 나가지 않습니다. 종료 상태는 `1`(거부) 또는 `2`(사용법)입니다.

**승인자 id는 서명 안에 들어갑니다.** 검증할 때 그 사람의 키로만 확인하기 위해서입니다. 키를 목록으로 받으면 아무 키로나 통과한 서명이 아무 이름으로나 도착할 수 있고, 서명은 「누군가 승인했다」까지만 답하게 됩니다.

**서명한 뒤에 계획을 고치면 승인은 무효가 됩니다.** 그것이 요점입니다. 승인자는 계획의 이름이 아니라 액션의 내용에 책임을 집니다.

```bash
pqcota-keygen                                     # the approver's key pair
PQCOTA_APPROVAL_KEY=<priv> pqcota-approve --approver reviewer-1 plan.json > plan.signed.json
PQCOTA_APPROVAL_KEYS=reviewer-1=<pub> pqcota-provision --level l2 plan.signed.json > provision.yml
```

## ③ 조회: 되돌림 근거 읽기

### `pqcota-records`

```
pqcota-records [node]
```

| 인자 | 하는 일 |
|---|---|
| `[node]` | 그 노드만 봅니다. 생략하면 전부입니다 |

`env PQCOTA_DSN`이 필요합니다. `pqcota-provision --dsn`이 쓴 저장소를 읽습니다. id, 상태, 영향받는 앱, 변경 전후 모듈을 나열합니다. **읽기 전용이며 상태를 바꾸지 않습니다.**

## ④ 입력의 출처: 확정된 계획

**이 리포지터리는 계획을 만들지 않습니다. 읽기만 합니다.** `FinalizedPlan`은 공개 계약(`plan.proto`)이므로 JSON으로 직접 씁니다. 액션 종류와 런타임별 샘플과 필드 설명은 [`examples/provisioning/plans/`](../examples/provisioning/plans/README.ko.md)에 있습니다. 가장 가까운 것을 골라 `targetNodeId`, 경로, provider를 자신의 것으로 바꾸세요.

**`status`는 `PLAN_STATUS_FINALIZED`여야 하며**, 아니면 거부합니다. 확정되지 않은 계획의 배포를 막는 게이트입니다.

`--dsn`을 주었을 때 읽는 변경 전 발견 항목과 `app_keys`는 인벤토리에 쌓인 이력에서 옵니다(`pqcota-inventory`가 읽는 것과 같은 저장소). → [pqcota-inventory cmd 명령 지도](https://github.com/randyinthedev-hash/pqcota-inventory/blob/main/cmd/README.ko.md)

---

**무엇을 언제 쓰나**
- 계획을 받아 적용할 산출물을 만든다 → **①**. `--level`이 어디까지 갈지 정합니다.
- 액션 뒤에 되돌린다 → **①**, 같은 계획에 `--rollback`을 붙입니다.
- 계획에 승인 서명을 붙인다 → **②**. 계획이 완전히 고정된 **뒤에** 합니다.
- 무엇이 스테이징되었는지, 변경 전은 무엇이었는지 → **③**.

> 로직은 `pkg/provisioning/`에 있고(계획 게이트, 분류 체계 → 설정 생성기, `GenerateProvisioningPlaybook`, `CaptureState`, `RecordStore` Mem/Pg), 이 명령들은 그것을 조립하는 얇은 진입점입니다.
