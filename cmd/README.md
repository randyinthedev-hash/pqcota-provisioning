English · [한국어](README.ko.md)

# cmd/: the provisioning entry points

The CLIs (Go binaries) of the provisioning stage. They **attach an approval signature** to a finalized plan, **generate an Ansible playbook** from that plan, and **persist the rollback basis**. They are sorted into four categories.

**The generator decides all of the playbook's content and order.** What the user does is run it with their own Ansible. There is no remote execution engine of our own.

## ① Generate: build a playbook from a finalized plan

### `pqcota-provision`

```
pqcota-provision [--level l1|l2|l3] [--rollback] [--dsn <postgres>] <plan.json>
```

| Argument · option | What it does |
|---|---|
| `<plan.json>` | a finalized plan (`FinalizedPlan`). **It is refused unless it is `PLAN_STATUS_FINALIZED`** |
| `--level l1` | **stage only**: as far as placing the module on the target |
| `--level l2` (default) | **through installation**. Module placement + config fragment placement |
| `--level l3` | **through activation and restart**. Lays out the plan's `activation` hooks (`pre`, `activate`, `deactivate`, `restart`) in their meaningful order |
| `--rollback` | the reverse playbook: removes the files forward placed |
| `--allow-incomplete` | **exits 0** even if the plan has blanks. The warnings still appear |
| `--allow-unverified-approvals` | **carries on** even with no key to check against. The default is to refuse |
| `--dsn <postgres>` | reads the before-findings from the history, **captures the before state** and persists it as an append-only record. For the format see [DSN](https://github.com/randyinthedev-hash/pqcota-discovery/blob/main/cmd/README.md#pqcota-hosts) |

| Environment variable | What it does |
|---|---|
| `PQCOTA_APPROVAL_KEYS` | comma-separated `<approver>=<base64 public key>`. Verifies each approval signature **with that approver's key**. It refuses if even one is off, and refuses if not a single approval was verified. **It also refuses if empty** |
| `PQCOTA_REQUIRE_APPROVAL` | `1` is accepted as it is. It now means the same as the default, so it changes nothing |

**It refuses when there is no key to check against.** An approval is where responsibility sits, and if any string can fill that place, the place is as good as empty. Before, it warned and let it through, and closing it happened only in deployments that had separately set `PQCOTA_REQUIRE_APPROVAL=1`, which left approval integrity as "a means you could close" while the default path stayed open.

To skip approval verification, you have to **write `--allow-unverified-approvals` on the command line, and there is no other means.** If an environment variable could open it too, what was verified would be hidden in shell settings, and from the log alone you could not tell whether this output stood on verified approvals. Even when it is opened, "not verified" still comes out. It is passing through, not having verified.

The signature is attached by [`pqcota-approve`](#pqcota-approve), and the key pair comes from [`pqcota-keygen`](https://github.com/randyinthedev-hash/pqcota-common/blob/main/cmd/README.md#pqcota-keygen) in `pqcota-common`. [The example runner](../examples/provisioning/README.md) walks exactly that path.

**`--level` is the default for actions the plan does not speak to.** The delegation level is a **per-asset attribute** the contract defines ("payment server = L2, stateless worker = L3"), so when an action states `automation_level` it is followed. That value is covered by the approval signature, so overriding it with a global flag would put the delegation level the approver signed and the level actually run out of step.

The playbook goes to stdout. Take it with `> provision.yml`.

**A plan with blanks does not end in success.** The output is still produced, but the exit status is **3**. The product goes out to stdout first and the warnings go out later to stderr, so if the exit status were 0 too, in automation that does not collect stderr **an incomplete playbook would remain as a normal output.** The reason it is not blocked is that filling it in by hand is a legitimate path. When you are passing over it knowingly, write `--allow-incomplete`.

| Exit status | Meaning |
|---|---|
| `0` | the output came out and there are no blanks. With `--dsn`, it looks up each action's snapshot reference in the history and leaves it on the record (`pqcota-records` shows it as a `snapshot:` line) |
| `1` | **refused**: this plan is not grounds for execution (not finalized, no approval, no action, approval cannot be verified, approval verification failed). Not a single line of playbook comes out |
| `3` | **incomplete**: the output came out but has blanks. The target algorithm, an activation hook or the traceability evidence is empty, or **the shape of a snapshot reference is wrong** (caught even without `--dsn`), or **it was looked up with `--dsn` and is not in the history, or the snapshot found does not contain that finding.** Which one it is, is written by name on stderr |
| `2` | the usage is wrong |

`--rollback` goes through the same checks. Deleting files has nothing to do with the target algorithm, so that warning is not given, but not being able to undo the activation for lack of `deactivate` and `restart`, and having no basis to retrace what is being undone, are weighed the same as in the forward direction.

**An empty hook at `--level l3` is not invented.** If the plan has no `activate`, that task is not made and **what does not happen is announced on stderr** (for example, with no restart hook: "the new provider may never be loaded"). How to activate depends on how the app starts, which the tool cannot know.

**What `--dsn` does is record, not apply.** It captures the state *before* the action (module@version, config, provider chain), which becomes the basis for saying what to return to when you undo it later.

### Applying it

```bash
pqcota-provision --level l2 plan.json > provision.yml
ansible-playbook -i targets.ini -e pqcota_module_sha256_oqsprovider=<sha256> provision.yml
```

Use the same `targets.ini` you used for discovery ([`pqcota-hosts`](https://github.com/randyinthedev-hash/pqcota-discovery/blob/main/cmd/README.md#pqcota-hosts)).

**The tool does not supply the provider module.** The playbook copies `files/<module file>` on the controller to the target, so the user has to put that file there.

| Variable | What it does |
|---|---|
| `pqcota_module_src_<provider>` | the controller-local path of that module. If absent, `pqcota_module_src`, and if that is absent too, `files/<module file>` |
| `pqcota_module_sha256_<provider>` | **the integrity gate**: after placement it measures the sha256 on the target and stops if it differs. If absent, `pqcota_module_sha256`, and if both are absent the check is skipped |

`<provider>` is the plan's `providerChoice` with **everything except alphanumerics replaced by `_`** (`acme-pqc` → `acme_pqc`), because that is Ansible's variable naming rule. If you give it with the hyphen kept, **the variable is not recognized and the check is skipped without any error.**

The hash is measured **on the target after the copy.** It measures the file actually placed on the node, not the original on the controller, so transfer corruption and path mistakes are caught as well. On a mismatch it stops on that node.

We recommend giving the sha256. This plants native code that will run cryptographic operations on the target, so you need **a way to pin what you planted.** If you do not give it, it is not an error: **the check task is skipped entirely.**

### Undoing it

```bash
pqcota-provision --level l2 --rollback plan.json > provision-rollback.yml
ansible-playbook -i targets.ini provision-rollback.yml
```

Applying does not overwrite the original and *adds* files, so removing what was added is the restore. At L3 the `deactivate` hook also undoes the activation.

## ② Approve: attach a signature to a plan

### `pqcota-approve`

```
pqcota-approve --approver <id> <plan.json>
```

| Argument · option | What it does |
|---|---|
| `--approver <id>` | the approver id. **`:` cannot be used**: it is the separator in the signature string |
| `env PQCOTA_APPROVAL_KEY` | a base64 ed25519 **private key**, as produced by [`pqcota-keygen`](https://github.com/randyinthedev-hash/pqcota-common/blob/main/cmd/README.md#pqcota-keygen) |

The signed plan goes to stdout. The signature string has the form `<approver>:ed25519:<base64>`, and it covers **the whole plan except the approval signatures themselves**.

**The first approval finalizes the plan.** A plan whose judgement is done arrives with `status=IN_REVIEW` and the approval field and finalization time empty. This command raises the status to `FINALIZED`, stamps `finalized_at`, and signs **after** that. The signature covers both, so if the order were changed the signature just made would break. From the second approval on, it changes nothing and only adds a signature.

| State it arrives in | What it does |
|---|---|
| `IN_REVIEW` | approvals and the finalization time **must be absent.** If present it is refused as corruption. There must be actions, and each action needs a target node and a kind. If it passes, it raises to `FINALIZED`, stamps the time and signs |
| `FINALIZED` | approvals and the finalization time **must both be present.** If either is missing it is refused as corruption. The action structure is checked the same way as for `IN_REVIEW`. If complete, it only adds a signature |
| `DRAFT` · `UNSPECIFIED` | refused |

When it refuses, nothing goes to stdout. The exit status is `1` (refused) or `2` (usage).

**The approver id goes inside the signature** so that verification checks only with that person's key. If the keys were taken as a list, a signature that passed with any key could arrive under any name, and the signature would answer only as far as "someone approved".

**If you edit the plan after signing, the approval becomes invalid.** That is the point. The approver answers for the content of the actions, not for the name of the plan.

```bash
pqcota-keygen                                     # the approver's key pair
PQCOTA_APPROVAL_KEY=<priv> pqcota-approve --approver reviewer-1 plan.json > plan.signed.json
PQCOTA_APPROVAL_KEYS=reviewer-1=<pub> pqcota-provision --level l2 plan.signed.json > provision.yml
```

## ③ Query: read the rollback basis

### `pqcota-records`

```
pqcota-records [node]
```

| Argument | What it does |
|---|---|
| `[node]` | only that node. If omitted, all of them |

`env PQCOTA_DSN` is required: it reads the store that `pqcota-provision --dsn` wrote. It lists the id, status, affected apps and before/after modules. **It is read-only and changes no state.**

## ④ Where the input comes from: a finalized plan

**This repo does not create plans. It only reads them.** `FinalizedPlan` is a public contract (`plan.proto`), so you write it directly as JSON. Samples per action kind and runtime, with a description of the fields, are in [`examples/provisioning/plans/`](../examples/provisioning/plans/README.md). Pick the closest one and change `targetNodeId`, the paths and the provider to your own.

**`status` has to be `PLAN_STATUS_FINALIZED`**, or it is refused. It is the gate that prevents deploying a plan that has not been finalized.

The before-findings and `app_keys` it reads when you give `--dsn` come from the history accumulated in the inventory (the same store `pqcota-inventory` reads). → [the pqcota-inventory cmd command map](https://github.com/randyinthedev-hash/pqcota-inventory/blob/main/cmd/README.md)

---

**When to use what**
- Take a plan and make the artifacts to apply → **①**. `--level` decides how far to go.
- Undo after an action → **①**, the same plan with `--rollback`.
- Attach an approval signature to a plan → **②**. Do it **after** the plan is completely fixed.
- What was staged, and with which before → **③**.

> The logic lives in `pkg/provisioning/` (the plan gate, the taxonomy → config generator, `GenerateProvisioningPlaybook`, `CaptureState`, and `RecordStore` Mem/Pg), and these commands are thin entry points that assemble it.
