English · [한국어](README.ko.md)

# examples/provisioning: see what gets generated for each plan

The cases below show how the output changes when you **swap only the plan** and keep the same command. Each case is one JSON file in [`plans/`](plans/README.md).

```bash
./examples/provisioning/run.sh                          # list the cases + a default run
./examples/provisioning/run.sh openssl-3.0-provider-inject
./examples/provisioning/run.sh custom-jca-provider --rollback
./examples/provisioning/run.sh --all
```

> The whole flow and the reasoning are in [Provisioning](../../README.md). This is where you look at **the real output for each case**.

> ⚠️ **The examples generate playbooks.** The **provider module binaries** such as `oqsprovider.so` and `acme-jce.jar` **are not in this repo** (they differ per arch, and a dummy would look like it works, which is harmful). To actually run a generated playbook, put your own module in [`files/`](files/README.md) or pass a path with `-e pqcota_module_src_<name>=`.
>
> **To see it through with a real provider**: fetch one with a pinned hash, and check on a real JVM that the generated fragment really registers the provider:
>
> ```bash
> ./examples/provisioning/files/fetch-example-provider.sh   # BC 1.85 (sha256 checked)
> ./examples/provisioning/files/verify-registration.sh      # compare the provider list before and after the fragment
> ```
>
> **The same check for OpenSSL** is done by an optional step of the demo: `DEMO_REAL_PROVIDER=1 ./demo/scripts/demo.sh`. It builds a real oqsprovider, places and activates it on a 3.0–3.4 node, and measures before and after with `openssl list` ([demo README](https://github.com/randyinthedev-hash/pqcota/blob/main/demo/README.md#optional-step--the-last-inch-with-a-real-provider-demo_real_provider1)).

## OpenSSL: the version decides the remediation

| Case | Observed situation | `kind` | What is generated |
|---|---|---|---|
| [`openssl-3.5-config-only`](plans/openssl-3.5-config-only.json) | 3.5+ native PQC | `CONFIG_ONLY` | **one line**, `Groups = X25519MLKEM768:x25519`. No provider module |
| [`openssl-3.0-provider-inject`](plans/openssl-3.0-provider-inject.json) | 3.0–3.4 (has the provider API) | `PROVIDER_INJECT` | the module placed at `/opt/pqcota/oqsprovider.so` + a config that **references its absolute path** |
| [`openssl-1.1.1-fork-replace`](plans/openssl-1.1.1-fork-replace.json) | 1.1.1 · 1.0.2 (no provider API) | `FORK_REPLACE` | **nothing is placed**. A comment saying it cannot be delivered through config: a manual step |

**The point**: the older the version, the less the tool can do for you. 1.1.1 does not drop out without a mark: **why it is manual stays in the playbook.**

## JVM/JCA: the provider situation decides the remediation

| Case | Observed situation | `kind` · `providerChoice` | What is generated |
|---|---|---|---|
| [`jca-native-config-only`](plans/jca-native-config-only.json) | JDK native PQC | `CONFIG_ONLY` | one line, `jdk.tls.namedGroups=…`. No provider registered |
| [`jca-provider-inject-bc`](plans/jca-provider-inject-bc.json) | a provider chain without PQC | `PROVIDER_INJECT` · `BC` | the JAR placed + `security.provider.2=org.bouncycastle.jce.provider.BouncyCastleProvider` |
| [`jca-fips-bcfips`](plans/jca-fips-bcfips.json) | **a regulated asset** | `PROVIDER_INJECT` · `BCFIPS` | the same flow, but **a different registration class** (`BouncyCastleFipsProvider`): FIPS routing |
| [`jca-eol-jdk-upgrade`](plans/jca-eol-jdk-upgrade.json) | an EOL JDK | `JDK_UPGRADE` | **nothing is placed**. A manual-step comment |

**The point**: `providerChoice` **decides the registration class name.** Whether it is BC or BC-FJA depending on regulation is a judgement made at the plan stage.

> JCA `PROVIDER_INJECT` always registers at **priority 2**. JCA lets the provider earlier in the list serve first, so putting it later means **nothing changes** even though the JAR is there.

## Custom providers

| Case | What it shows |
|---|---|
| [`custom-openssl-provider`](plans/custom-openssl-provider.json) | `acme-pqc.so`, assumed to be an in-house build: the absolute module path, the `pqcota_module_src_acme_pqc` variable and a sha256 gate are generated automatically |
| [`custom-jca-provider`](plans/custom-jca-provider.json) | `acme-jce.jar`, assumed to be an in-house build: **the FQCN is stated in `providerClass`**, so the plan alone is complete |
| [`custom-jca-missing-class`](plans/custom-jca-missing-class.json) | the same, with only `providerClass` left out: **a placeholder + a note on what to do** |

Both are **complete from the plan alone**. JCA needs one more field, though:

```json
{"providerChoice": "acme-jce", "providerClass": "com.acme.jce.AcmeProvider"}
```

```
# OpenSSL — only the path is needed (the generator decides it)
module = /opt/pqcota/acme-pqc.so

# JCA — the FQCN is needed (the plan has to say it)
security.provider.2=com.acme.jce.AcmeProvider
```

**Why one more field**: OpenSSL only needs a **path**, and the playbook decides that path. JCA needs an **FQCN** written into java.security, and package structures differ per vendor, so it cannot be derived from the provider name. Only BC and BC-FJA are known and filled in automatically. For anything else the plan has to say it.

When it is not stated, nothing is guessed and this is left instead (`custom-jca-missing-class`):

```
# ⚠ the class name below is a placeholder — replace it with the exact class from your provider build, or
#   put the FQCN in the plan's provider_class and it is filled in automatically.
security.provider.2=<acme-jce: check the provider's documentation for the exact class name>
```

> **For the JVM to find the JAR**, it has to be on the classpath. The method differs by JDK generation (the `lib/ext` extension mechanism was **removed in JDK 9**), so the generated fragment explains both.

For the module delivery procedure (controller → target, the `files/` convention, sha256), see [cmd · Applying it](../../cmd/README.md#applying-it).

## Boundary cases

| Case | What it shows |
|---|---|
| [`signature-algorithm`](plans/signature-algorithm.json) | when `targetAlgorithm` is a **signature** (ML-DSA), the group line comes out **as a comment**, not a KEM group. Nothing is filled in by guessing |
| [`00-basic-two-actions`](plans/00-basic-two-actions.json) | two nodes in one plan: it splits into **a play per node**, and each receives only its own action |

## Try the gate

Change the `"status"` of any plan to `"PLAN_STATUS_DRAFT"` and run it, and it is refused (exit 1):

```
refused: plan not finalized — refusing to provision. Only a finalized plan justifies provisioning.
```

Emptying `approvalSignatures` has the same effect.

## L3: activation and restart

L1/L2 only place files. **L3 makes what was placed actually referenced and starts the process again.** The activation
point differs per environment (a systemd drop-in, an include directory, an in-house start script), so the tool does not guess.
You write the commands in the plan's `activation`, and the generator lays them out in **their meaningful order**.

```bash
./examples/provisioning/run.sh l3-activation-hooks             # forward
./examples/provisioning/run.sh l3-activation-hooks --rollback  # reverse order
```

| Hook | When | forward | rollback |
|---|---|---|---|
| `pre` | what to bring down before the action | ① | ① |
| (none) | placing the module and config | ② | ③ (removal) |
| `activate` | make the fragment referenced | ③ | (none) |
| `deactivate` | the inverse of `activate` | (none) | ② |
| `restart` | load the new provider | ④ | ④ |

So forward is `pre → place → activate → restart`, and rollback is `pre → deactivate → remove → restart`.
Even when a node has several actions, **the same command goes out only once**. Restarting per action would shake the service
several times, and a restart wedged between activations would bring it up with only part of it applied.

`l3-activation-hooks` is a JCA case, so it shows the hooks closing the **JAR placed ≠ loaded** trap. The paths
and variable names inside it are only examples, so rewrite them for how your own app starts.

### Without hooks: nothing is invented

```bash
./examples/provisioning/run.sh l3-hooks-missing
```

The same plan with only the hooks removed. The generator **does not make an activation command**, and instead tells you on stderr what does *not* happen:
no `activate` = the fragment is only placed and not made referenced, no `restart` = the new
provider may not be loaded, no `deactivate` = rollback cannot undo the activation.

## Rollback

Add `--rollback` to any case and you get a playbook that **removes what forward placed**. The original settings were never overwritten, so removal alone returns the previous state.

```bash
./examples/provisioning/run.sh custom-openssl-provider --rollback
```

## With `--dsn`

It reads the before-findings from the history and leaves the **state before the action** and the **affected apps** as an append-only record. Discovery has to be loaded into the store first, so the end-to-end flow is shown by [demo/](https://github.com/randyinthedev-hash/pqcota/tree/main/demo).

## What is not here

**Dynamic provisioning** (injecting into a running process without a restart) is not done. **Fleet orchestration**
(drain, rolling, health-check gates) is not done. Command map: [cmd/README](../../cmd/README.md).
