[English](README.md) · 한국어

# files/: 자신의 provider 모듈을 여기에 두세요

**이 리포지터리에는 provider 모듈 바이너리가 없습니다.** `.so`는 아키텍처와 libc마다 다르고 JAR은 10MB쯤 되므로, 커밋하면 리포지터리가 **갱신되지 않는 사본**을 들고 있게 됩니다. 더미를 두는 것은 더 나쁩니다. 로드되지 않는데도 동작하는 것처럼 보입니다.

대신 **받아 올 곳과 고정된 해시**가 있습니다. 받은 것이 기대한 것인지 확인할 수 있는 편이 커밋해 두는 것보다 낫습니다.

```bash
./examples/provisioning/files/fetch-example-provider.sh          # puts BC.jar here (sha256 checked)
./examples/provisioning/files/fetch-example-provider.sh --check  # only the pinned value, without fetching
```

BouncyCastle **1.85**를 고정했습니다. 1.80부터 ML-KEM은 `BouncyCastleProvider`(생성된 조각이 등록하는 바로 그 클래스)에 있고, 1.78.x 이하에서는 그 클래스에 KEM이 하나도 없으며 Kyber를 `BouncyCastlePQCProvider`에 따로 둡니다. 그 버전에서는 같은 조각으로 목표 알고리즘이 나타나지 않을 것입니다.

OpenSSL provider(`oqsprovider.so`)는 받아 올 수 없습니다. 배포판과 아키텍처마다 공식 바이너리가 없으므로 liboqs + oqs-provider를 빌드해야 합니다. 없는 것을 있는 것처럼 꾸미지 않습니다.

## 조각이 정말 provider를 등록하는가?

파일을 복사한 것과 **의도한 일이 일어난 것**은 다릅니다(조각이 아예 적용되지 않는 경우가 실제로 있습니다). 이것은 실제 JAR과 실제 JVM으로 처음부터 끝까지 확인합니다.

```bash
./examples/provisioning/files/verify-registration.sh                    # jca-provider-inject-bc
./examples/provisioning/files/verify-registration.sh jca-native-config-only
```

출력은 세 가지를 보여 줍니다. 조각을 적용한 뒤의 **provider 순서**, **목표 알고리즘(ML-KEM, ML-DSA)이 실제로 어느 provider에서 오는지**, 그리고 조각이 목록에 한 일의 **전후 비교**입니다.

그 비교는 중요한 사실 하나를 드러냅니다. `security.provider.2=`는 **끼워 넣는 것이 아니라 그 자리를 대체합니다.** JDK 21에서는 원래 2번이던 `SunRsaSign`이 목록에서 빠지고 RSA 서비스가 새 provider의 구현으로 옮겨 갑니다. 밀어내지 않으려면 끼워 넣기 전에 대상 노드의 `java.security`에서 뒤쪽 번호를 하나씩 내려야 합니다. 그러려면 그 노드의 원본을 알아야 하므로 도구가 대신 하지 않습니다.

`jca-native-config-only`로 실행하면 점검하는 알고리즘 셋(`ML-KEM`, `ML-KEM-768`, `ML-DSA`)이 모두 **없음**으로 나옵니다. provider를 등록하지 않는 케이스이고 JDK 21에는 네이티브 ML-KEM이 없습니다. 이것도 정직한 결과입니다.

생성된 플레이북을 **실제로 실행하려면** 모듈이 컨트롤러에 있어야 합니다. Ansible `copy`는 `src`를 플레이북 옆의 `files/`에서도 찾으므로, 여기에 두면 인자 없이 동작합니다.

```
examples/provisioning/files/
  acme-pqc.so      ← the name the custom-openssl-provider case looks for
  acme-jce.jar     ← the name the custom-jca-provider case looks for
  oqsprovider.so   ← the openssl-3.0-provider-inject case
  BC.jar           ← the jca-provider-inject-bc case
```

파일 이름은 계획의 `providerChoice`에서 옵니다. `"providerChoice": "acme-pqc"` → `acme-pqc.so`입니다.

다른 곳에 두려면 경로를 넘기세요.

```bash
ansible-playbook provision.yml \
  -e pqcota_module_src_acme_pqc=/srv/pqcota/modules/acme-pqc.so \
  -e pqcota_module_sha256_acme_pqc=$(sha256sum /srv/pqcota/modules/acme-pqc.so | cut -d' ' -f1)
```

## 플레이북만 해 보고 싶다면

빈 파일이어도 **배치와 체크섬 작업은 정상적으로 실행됩니다**(당연히 실제 암호 능력은 없습니다). 호스트를 건드리지 않으려면 컨테이너 안에서 로컬 연결로 실행하세요.

> 여기서 `--allow-unverified-approvals`를 쓰는 것은 보고 싶은 것이 승인 경로가 아니라 **배치와 체크섬**이기 때문입니다. 생성기는 확인할 키가 없으면 기본적으로 거부하므로 그 문을 명시적으로 엽니다. 승인이 진행되는 모습까지 보려면 [예제 실행기](../README.ko.md)를 보세요.

```bash
mkdir -p /tmp/try/files && : > /tmp/try/files/acme-pqc.so
go run ./cmd/pqcota-provision --level l2 --allow-unverified-approvals \
  examples/provisioning/plans/custom-openssl-provider.json \
  | sed 's/^  hosts: .*/  hosts: all/' > /tmp/try/provision.yml

docker run --rm -v /tmp/try:/work -w /work alpine/ansible:latest \
  ansible-playbook -i 'localhost,' -c local provision.yml
```

무결성 게이트까지 보려면 해시를 넘기세요. 일치하면 통과하고, 아니면 **멈춥니다.**

```bash
... ansible-playbook ... -e "pqcota_module_sha256_acme_pqc=$(sha256sum /tmp/try/files/acme-pqc.so | cut -d' ' -f1)"
... ansible-playbook ... -e "pqcota_module_sha256_acme_pqc=deadbeef"   # → stops with fail_msg
```

되돌림까지 보려면 **같은 컨테이너 안에서** 이어 가야 합니다. `docker run`은 매번 새 컨테이너이므로 따로 실행하면 지울 것이 남아 있지 않습니다(`changed=0`이 나와 아무 일도 없었던 것처럼 보입니다).

```bash
go run ./cmd/pqcota-provision --level l2 --allow-unverified-approvals --rollback \
  examples/provisioning/plans/custom-openssl-provider.json \
  | sed 's/^  hosts: .*/  hosts: all/' > /tmp/try/provision-rollback.yml

docker run --rm -v /tmp/try:/work -w /work alpine/ansible:latest sh -c '
  ansible-playbook -i localhost, -c local provision.yml
  ls /opt/pqcota /etc/pqcota                    # it was placed
  ansible-playbook -i localhost, -c local provision-rollback.yml
  ls /opt/pqcota /etc/pqcota'                   # it is empty (changed=2)
```

활성화와 재시작(L3)까지 보려면 `activation` 훅이 있는 계획에 `--level l3`을 쓰세요 → [`l3-activation-hooks`](../plans/l3-activation-hooks.json), [예제 README의 L3 절](../README.ko.md#l3-활성화와-재시작).

> 이 폴더의 `.so`와 `.jar` 파일은 gitignore 대상입니다. 받아 온 것이든 직접 빌드한 것이든 벤더 바이너리가 실수로 커밋되지 않게 하기 위해서입니다.
