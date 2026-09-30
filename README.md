# pqcota-provisioning

The provisioning stage of the pqcota platform: generate migration artifacts from a finalized plan.

It signs and checks plans, turns a finalized plan into Ansible playbooks (stage, install, activate) with matching rollback playbooks, and records the state before each change. It is one of five repositories that make up [pqcota](https://github.com/randyinthedev-hash/pqcota): `pqcota-common`, `pqcota-inventory`, `pqcota-discovery`, `pqcota-provisioning`, and the integration repository `pqcota` (demo, examples, release bundles, contributing guide).

## What is here

| Path | What |
|---|---|
| `pkg/provisioning/` | the library: the plan gate, the taxonomy-to-config generators (OpenSSL and JCA), the playbook generator, `CaptureState`, and the record stores |
| `provisioning/cmd/` | the commands: `pqcota-provision`, `pqcota-approve`, `pqcota-records` |
| `examples/` | runnable examples: one plan per case under `examples/provisioning/plans/`, the generated playbooks, and rollback |
| `provisioning/README.md` | what the stage does and how to use it |

## Depends on

`pqcota-common` and `pqcota-inventory`. It does not depend on discovery.

## Build and test

```bash
make            # every check of this repository
go test ./...   # unit tests only
```

Until the modules are tagged, `go.mod` points at the sibling repositories with `replace` directives (`../pqcota-common` and so on), so clone the repositories side by side. Remove the `replace` lines and raise the `require` versions once the tags exist.

## Contributing · security · license

Contributing and security reporting are described in the [pqcota repository](https://github.com/randyinthedev-hash/pqcota). Licensed under [Apache-2.0](https://github.com/randyinthedev-hash/pqcota/blob/main/LICENSE).
