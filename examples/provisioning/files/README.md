# files/: put your own provider modules here

**This repo has no provider module binaries.** A `.so` differs per arch and libc, and a JAR is around 10 MB, so committing them would leave the repo holding **copies that never get updated**. Leaving dummies here is worse: they never load, yet look like they work.

Instead there is **a place to fetch from and a pinned hash**. Being able to check that what you fetched is what you expected beats committing it.

```bash
./examples/provisioning/files/fetch-example-provider.sh          # puts BC.jar here (sha256 checked)
./examples/provisioning/files/fetch-example-provider.sh --check  # only the pinned value, without fetching
```

BouncyCastle **1.85** is pinned. From 1.80 ML-KEM lives in `BouncyCastleProvider` (the very class the generated fragment registers), while 1.78.x and below have zero KEMs in that class and keep Kyber separately in `BouncyCastlePQCProvider`. With the same fragment, the target algorithm would not appear.

The OpenSSL provider (`oqsprovider.so`) cannot be fetched. There is no official binary per distribution and arch, so you have to build liboqs + oqs-provider. Nothing is made to look present when it is not.

## Does the fragment really register the provider?

Having copied a file is different from **the intended thing having happened** (there really are cases where the fragment is not even applied). This checks it end to end with a real JAR and a real JVM:

```bash
./examples/provisioning/files/verify-registration.sh                    # jca-provider-inject-bc
./examples/provisioning/files/verify-registration.sh jca-native-config-only
```

The output shows three things: the **provider order** after the fragment is applied, **which provider the target algorithms (ML-KEM, ML-DSA) actually come from**, and a **before and after comparison** of what the fragment did to the list.

That comparison reveals one important fact: `security.provider.2=` does **not insert, it replaces that slot.** On JDK 21 the `SunRsaSign` that was originally number 2 drops out of the list, and the RSA services move to the new provider's implementation. To avoid pushing it out, you have to shift the later numbers down by one in the target node's `java.security` before inserting. That requires knowing that node's original, so the tool does not do it for you.

Run with `jca-native-config-only` and all three algorithms come out as **absent**. It is a case that registers no provider, and JDK 21 has no native ML-KEM. That is an honest result too.

To **actually run** a generated playbook, the module has to be on the controller. Ansible `copy` also looks for `src` in the `files/` next to the playbook, so putting it here works without arguments:

```
examples/provisioning/files/
  acme-pqc.so      ← the name the custom-openssl-provider case looks for
  acme-jce.jar     ← the name the custom-jca-provider case looks for
  oqsprovider.so   ← the openssl-3.0-provider-inject case
  BC.jar           ← the jca-provider-inject-bc case
```

The file name comes from the plan's `providerChoice`. `"providerChoice": "acme-pqc"` → `acme-pqc.so`.

To keep it elsewhere, pass the path:

```bash
ansible-playbook provision.yml \
  -e pqcota_module_src_acme_pqc=/srv/pqcota/modules/acme-pqc.so \
  -e pqcota_module_sha256_acme_pqc=$(sha256sum /srv/pqcota/modules/acme-pqc.so | cut -d' ' -f1)
```

## If you only want to try the playbook itself

Even an empty file lets **the placement and checksum tasks run normally** (naturally there is no real crypto capability). To avoid touching your host, run it over a local connection inside a container:

> `--allow-unverified-approvals` is written here because what you want to see is **placement and checksum**, not the approval path. The generator refuses by default when there is no key to check against, so that door is opened explicitly. [The example runner](../README.md) shows the approval being walked through as well.

```bash
mkdir -p /tmp/try/files && : > /tmp/try/files/acme-pqc.so
go run ./cmd/pqcota-provision --level l2 --allow-unverified-approvals \
  examples/provisioning/plans/custom-openssl-provider.json \
  | sed 's/^  hosts: .*/  hosts: all/' > /tmp/try/provision.yml

docker run --rm -v /tmp/try:/work -w /work alpine/ansible:latest \
  ansible-playbook -i 'localhost,' -c local provision.yml
```

To see the integrity gate as well, pass a hash. If it matches it passes, and if not it **stops**:

```bash
... ansible-playbook ... -e "pqcota_module_sha256_acme_pqc=$(sha256sum /tmp/try/files/acme-pqc.so | cut -d' ' -f1)"
... ansible-playbook ... -e "pqcota_module_sha256_acme_pqc=deadbeef"   # → stops with fail_msg
```

To see the undo as well, you have to continue **inside the same container**. `docker run` is a new container each time, so running it separately leaves nothing to delete (you get `changed=0` and it looks as if nothing happened):

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

To see activation and restart (L3) as well, use `--level l3` with a plan that has `activation` hooks → [`l3-activation-hooks`](../plans/l3-activation-hooks.json), [the L3 section of the example README](../README.md#l3-activation-and-restart).

> The `.so` and `.jar` files in this folder are gitignored, so that a vendor binary, whether fetched or built yourself, is never committed by accident.
