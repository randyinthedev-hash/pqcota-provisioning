English · [한국어](README.ko.md)

# plans/: sample plans (`FinalizedPlan`)

These are the **input** of `pqcota-provision`. This repo does not create plans, it only reads them, so to use one the user writes it. Pick the closest sample here and change `targetNodeId`, the paths and the provider to your own.

```bash
./run.sh openssl-3.5-config-only          # the example runner: it walks through the signing too
```

**A plan has to be approved to get past the generator.** With no key to check against it refuses, so when you call it yourself you sign
and register the public key. The runner does these three things for you.

```bash
eval "$(pqcota-keygen | grep '^PQCOTA_')"
PQCOTA_APPROVAL_KEY="$PQCOTA_SIGN_KEY" \
  pqcota-approve --approver reviewer-1 plans/openssl-3.5-config-only.json > plan.signed.json
PQCOTA_APPROVAL_KEYS="reviewer-1=$PQCOTA_VERIFY_KEY" \
  pqcota-provision --level l2 plan.signed.json > provision.yml
```

**What gets generated** is in the [parent README](../README.md), case by case. This document covers **what the JSON itself looks like**.

## The samples at a glance

What splits them is `kind` (the remediation type) and `cryptoRuntime`. Fill in only the other fields that the combination requires.

| File | `cryptoRuntime` | `kind` | Distinctive fields |
|---|---|---|---|
| [`openssl-3.5-config-only`](openssl-3.5-config-only.json) | OPENSSL | `CONFIG_ONLY` | none. One config line is all it takes, so no provider is specified |
| [`openssl-3.0-provider-inject`](openssl-3.0-provider-inject.json) | OPENSSL | `PROVIDER_INJECT` | `providerChoice: oqsprovider` |
| [`openssl-1.1.1-fork-replace`](openssl-1.1.1-fork-replace.json) | OPENSSL | `FORK_REPLACE` | none. It cannot be done through config, so only a comment is left |
| [`jca-native-config-only`](jca-native-config-only.json) | JCA | `CONFIG_ONLY` | none |
| [`jca-provider-inject-bc`](jca-provider-inject-bc.json) | JCA | `PROVIDER_INJECT` | `providerChoice: BC` |
| [`jca-fips-bcfips`](jca-fips-bcfips.json) | JCA | `PROVIDER_INJECT` | `providerChoice: BCFIPS`: the registration class changes |
| [`jca-eol-jdk-upgrade`](jca-eol-jdk-upgrade.json) | JCA | `JDK_UPGRADE` | none. No output |
| [`custom-openssl-provider`](custom-openssl-provider.json) | OPENSSL | `PROVIDER_INJECT` | `providerChoice: acme-pqc`: an unknown name |
| [`custom-jca-provider`](custom-jca-provider.json) | JCA | `PROVIDER_INJECT` | the FQCN stated in `providerClass` |
| [`custom-jca-missing-class`](custom-jca-missing-class.json) | JCA | `PROVIDER_INJECT` | the same, when `providerClass` is **left out** |
| [`l3-activation-hooks`](l3-activation-hooks.json) | JCA | `PROVIDER_INJECT` | **`activation`**, all four hooks |
| [`l3-hooks-missing`](l3-hooks-missing.json) | JCA | `PROVIDER_INJECT` | **no** `activation`: what does not happen is announced |
| [`signature-algorithm`](signature-algorithm.json) | OPENSSL | `CONFIG_ONLY` | `targetAlgorithm` is a **signature** (ML-DSA) |
| [`00-basic-two-actions`](00-basic-two-actions.json) | both | `PROVIDER_INJECT` ×2 | two nodes: it splits into a play per node |

## Fields

### The top level of a plan

| Field | Required | What it does |
|---|---|---|
| `id` | ✅ | the plan identifier |
| `status` | ✅ | a plan whose judgement is done **arrives as `PLAN_STATUS_IN_REVIEW`.** `pqcota-approve` raises it to `FINALIZED` on the first approval, and the generator **refuses anything that is not `FINALIZED`.** It is the gate that prevents deploying an unapproved plan. `DRAFT` is refused by the approval itself |
| `scope` | | a label for the plan's scope (e.g. `ring-0`) |
| `approvalSignatures` | | **arrives empty.** It is the place for the execution approval, so the side that made the judgement does not fill it: [`pqcota-approve`](../../../cmd/README.md) signs with the approver's key and puts it in. The generator **refuses if it is empty.** If it is `IN_REVIEW` but has a value, it is refused as corruption, because an approval is "a signature over this state" |
| `derivedFromSnapshotId` | | **the compatibility path for older versions.** The id of the one snapshot the whole plan came from. If an action has `evidenceSources`, that takes priority and this value is not read. If both are empty it warns (it runs, but the history keeps no evidence) |
| `rulesetVersion` | | the rule version that made the plan. It warns if empty |
| `finalizedAt` | | **arrives empty.** The first approval stamps it. If it is `IN_REVIEW` but has a value, it is refused as corruption. If it is empty on an approved plan, the generator warns |
| `actions` | ✅ | the list of actions. **A play is split per node** |

**The samples are not approved.** They keep the shape of a plan whose judgement is done (`IN_REVIEW`, with the approval field and the finalization time empty), so feeding one straight to the generator is refused with "no approval signature". That is the correct behaviour. The samples show the shape of an action, not an approved plan. Earlier, the samples were `FINALIZED` and carried a label such as `"reviewer:alice"`, and **a label sitting in the approval field made it look as if it passed the structural gate.** A plan you really deploy is signed with `pqcota-approve`, and that command raises the state and stamps the time.

### An action (`actions[]`)

| Field | Required | What it does |
|---|---|---|
| `id` | ✅ | the action identifier. A warning message uses this value to point at which action |
| `targetNodeId` | ✅ | the node this action goes to. It becomes the playbook's `hosts:`. **It is refused if empty**, since an empty entry would produce a play that reaches nowhere |
| `findingId` | | the observation it is based on. It links to an asset in the inventory. It warns if empty |
| `evidenceSources[]` | | **the evidence for this action**: which finding came from which snapshot state. `{findingId, snapshot: {sourceNodeId, snapshotId \| content: {formatVersion, digest, rulesetVersion}}}`. `sourceNodeId` is the name under which the history stored that snapshot (the envelope's node), so it may differ from `targetNodeId`. There is usually one, with the primary evidence first. **If the shape is wrong it is incomplete (exit 3) even without `--dsn`**, and with `--dsn` the generator actually looks it up in the history and leaves it on the record. It is also incomplete if it cannot be found, or if the snapshot found does not contain that finding. [`openssl-3.5-config-only`](openssl-3.5-config-only.json) shows the content-fingerprint form |
| `cryptoRuntime` | ✅ | `CRYPTO_RUNTIME_OPENSSL` \| `CRYPTO_RUNTIME_JCA`: decides the syntax of the config fragment |
| `kind` | ✅ | the remediation type (below). **`UNSPECIFIED` is refused**: the generator cannot branch, and a fragment that says "cannot be put in through config" for something the plan never said would go out |
| `targetAlgorithm` | | the target algorithm. For a KEM a hybrid group line goes out, and **for a signature a comment goes out instead of the group line** |
| `providerChoice` | if `PROVIDER_INJECT` | the name of the provider to inject: **this value becomes the file name** (below) |
| `providerClass` | for a JCA custom provider | the FQCN to write in `java.security`. **If missing**, only the known names (BC, BCFIPS) are settled, and anything else gets a placeholder + a warning |
| `rollbackNote` | | a note on undoing |
| `activation` | if `--level l3` | the activation hooks (below) |

### `providerChoice`: the name becomes the file name

| | OpenSSL | JCA |
|---|---|---|
| What this value becomes | the file name `<name>.so`, placed at `/opt/pqcota/<name>.so` | the file name `<name>.jar` **+ it decides the registration class** |
| Is any name allowed | **yes**: config references only the path, so the name is free | **no**: unless it is a known name you also have to give `providerClass` (the FQCN) |
| What class the name implies | (not applicable) | `BC` · `BCFIPS` (= `BC-FJA`) |
| If empty | `provider.so` | treated as `BC` |

In JCA, when the name is not a known one and there is no `providerClass` either, the registration line goes out as the placeholder `<name: check the provider's documentation for the exact class name>` and a warning appears with it. That means nothing is invented. [`custom-jca-missing-class`](custom-jca-missing-class.json) is that case.

#### Which providers can you inject

The name is free, but **the tool can emit only one shape of settings fragment**: `activate = 1` + `module = path`.
A provider for which that shape is enough works as it is, and one that requires another shape cannot be emitted yet.

| Candidate | Does it work |
|---|---|
| [oqsprovider](https://github.com/open-quantum-safe/oqs-provider) | ✅ the current shape is enough: confirmed on the real thing (2026-08-06, OpenSSL 3.0.13) |
| [wolfProvider](https://github.com/wolfSSL/wolfProvider) | ◐ looks sufficient but not confirmed on the real thing |
| OpenSSL's own `fips` module | ❌ a different shape: it has to pull in the `fipsmodule.cnf` that `fipsinstall` creates |
| [pkcs11-provider](https://github.com/openssl-projects/pkcs11-provider) | ❌ a different shape: it needs more keys, such as the driver path |
| JCA custom | ✅ `providerClass` (the FQCN) is already the general path |

To accept a ❌ one, the tool would have to know its shape.
The module file itself, in every case, is something the user obtains and puts in [`files/`](../files/README.md).

**The name is also used as an Ansible variable name.** In `pqcota_module_src_<name>` and `pqcota_module_sha256_<name>`, `<name>` is the name **with everything except alphanumerics replaced by `_`** (`acme-pqc` → `acme_pqc`), because a hyphen cannot be used in an Ansible variable name. A variable given with the hyphen kept is **not recognized and is ignored without any error** (the whole integrity check is skipped).

### `kind`: decides what gets generated

| Value | Output |
|---|---|
| `REMEDIATION_KIND_CONFIG_ONLY` | only a config fragment. No provider is placed |
| `REMEDIATION_KIND_PROVIDER_INJECT` | the provider module placed + a sha256 gate + a config fragment that references that module |
| `REMEDIATION_KIND_FORK_REPLACE` · `JDK_UPGRADE` · `APP_RECONFIG` · `REBUILD` · `DECOMMISSION` | **nothing is placed for that action.** It is an action that cannot be done through config, so only a comment is left: `# action a1(…): cannot be delivered through config — manual step`. The common skeleton of the play (creating directories) still comes out |

### `activation`: the L3 hooks

The user writes the commands, and **the generator decides the order they are laid out in** (apply: `pre` → place → `activate` → `restart`; rollback: `pre` → deactivate → remove → `restart`).

```json
"activation": {
  "pre":        "systemctl stop payments.service",
  "activate":   "…a command that makes the app see the new provider…",
  "deactivate": "…a command that undoes activate…",
  "restart":    "systemctl start payments.service"
}
```

**An empty hook is not invented.** If it is missing, that task is not made and what does not happen is announced on stderr. `l3-hooks-missing` is that case.
