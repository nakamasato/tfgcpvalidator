# tfgcpvalidator

Catch Terraform failures on Google Cloud at plan time, before `apply` breaks something.

> Status: preview. Until `v1`, any release may contain breaking changes — to the
> flags, the output format, or the action inputs.

## Why

`terraform plan` does not verify that a resource is actually deletable. Google
Cloud's `deletion_protection` is enforced by the API, so the check only happens
during `apply`: the pull request plan is green, and the apply fails after the
merge with `Error, failed to delete instance because deletion_protection is set
to true`.

That is worse than a plain failed apply. Terraform destroys in dependency order,
so deleting a `google_sql_database_instance` removes its databases and users
first and only then hits the protected instance. **The databases are gone, and
the protected instance remains, empty.**

No guard written inside HCL prevents this — `lifecycle.prevent_destroy`, `check`
blocks and `precondition` all disappear at the moment the resource block is
removed, which is the most common destroy path. A guard only works if it
inspects the plan from the outside.

The long version, including why existing tools don't cover it and where this is
going: [docs/design.md](docs/design.md).

## What it does

`tfgcpvalidator` reads a Terraform plan in JSON form and reports what will make `apply` fail.

v1 covers destroys:

- A resource is being destroyed while a deletion-protection field is still set — **error**, because the apply is guaranteed to fail.
- A resource is being replaced, since a replace deletes before it creates and fails the same way — **error**.

The fields it matches, and the value that blocks a delete:

| Field | Blocking value |
| --- | --- |
| `deletion_protection` | `true`, or `"PROTECTED"` where the provider spells it as an enum |
| `deletion_protection_enabled` | `true` |
| `settings.deletion_protection_enabled` | `true` |
| `deletion_policy` | `"PREVENT"` |
| `delete_protection_state` | `"DELETE_PROTECTION_ENABLED"` |
| `delete_protection` | `true` |
| `enable_deletion_protection` | `true` |

These are field names, not a list of resource types, so a resource type that
gains deletion protection in a future provider release is covered without a
change here. Resource types outside Google Cloud are skipped: the same field
names appear on other providers, and this tool does not claim to cover them.

How the check locates those fields in a plan and decides whether the apply will
fail: [docs/architecture.md](docs/architecture.md).

## Usage

### GitHub Actions

```yaml
- run: terraform plan -out tfplan.binary
- run: terraform show -json tfplan.binary > tfplan.json

- uses: nakamasato/tfgcpvalidator@v0
  with:
    plan: tfplan.json
```

The action fails the job when a finding reaches the `fail-on` severity, which
defaults to `error`, and annotates the run with every finding.

On a pull request it also posts the findings as a comment. The same comment is
reused on every run, and once the findings are gone it is hidden rather than
left behind. A clean run never posts anything.

| Input | Default | Description |
| --- | --- | --- |
| `plan` | *required* | Path to the output of `terraform show -json` |
| `check` | every check | Name of a single check to run |
| `format` | `github` | `text`, `markdown`, `github` or `json` |
| `fail-on` | `error` | `error`, `warn` or `never` |
| `comment` | `auto` | Post the comment: `auto` for pull request events only, or `true` or `false` |
| `target` | none | Name separating this call's comment from another's on the same pull request |
| `github-token` | `${{ github.token }}` | Token used for the comment |

Outputs: `findings` (JSON), `error-count`, `warn-count`.

The action installs the `tfgcpvalidator` release that matches the ref it is
pinned to, so `@v0` tracks the latest `v0.x` and `@v0.1.2` stays on that
release. A `tfgcpvalidator` already on `PATH` is used as-is instead.

The comment needs `pull-requests: write`:

```yaml
permissions:
  contents: read
  pull-requests: write
```

Without it the action leaves a warning annotation and the job's success or
failure is unaffected, which is what a pull request from a fork gets.

Calling the action more than once on the same pull request — one plan per
environment, say — needs a `target` on each call. It names the comment, so each
call updates its own instead of overwriting the others':

```yaml
strategy:
  matrix:
    env: [dev, staging, prod]
steps:
  - uses: nakamasato/tfgcpvalidator@v0
    with:
      plan: ${{ matrix.env }}/tfplan.json
      target: ${{ matrix.env }}
```

### CLI

```bash
go install github.com/nakamasato/tfgcpvalidator/cmd/tfgcpvalidator@latest

terraform plan -out tfplan.binary
terraform show -json tfplan.binary > tfplan.json

tfgcpvalidator validate --plan tfplan.json          # every check
tfgcpvalidator validate destroy --plan tfplan.json  # one check
```

| Exit code | Meaning |
| --- | --- |
| `0` | No finding reached the `--fail-on` severity |
| `1` | A finding reached it |
| `2` | The tool itself failed, for example an unreadable plan |
