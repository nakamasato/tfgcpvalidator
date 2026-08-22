# tfgcpvalidator

Catch Terraform failures on Google Cloud at plan time, before `apply` breaks something.

## The problem

A standard Terraform pipeline assumes that `terraform plan` is an accurate preview of `terraform apply`. On Google Cloud, `deletion_protection` breaks that assumption.

The check is enforced by the Google Cloud API, which means it only happens during `apply`. `terraform plan` never verifies that a resource is actually deletable. So:

1. The pull request plan is green, and reviewers approve it.
2. `apply` fails after the merge with `Error, failed to delete instance because deletion_protection is set to true`.
3. The problem surfaces *after* review, the one place designed to catch it.

## Why a failed apply is worse than it sounds

Terraform destroys resources in dependency order. Deleting a `google_sql_database_instance` means Terraform first deletes its children — `google_sql_database`, `google_sql_user` — and only then reaches the instance, where `deletion_protection` stops it.

**The databases are gone. The protected instance remains, empty.** The resource you asked Terraform to protect survives, and the data you actually cared about does not.

This is reported in [hashicorp/terraform#33732](https://github.com/hashicorp/terraform/issues/33732) and [terraform-provider-google#7869](https://github.com/hashicorp/terraform-provider-google/issues/7869). Both are closed with no fix. It has to be guarded from the outside.

A partial apply also leaves state and reality out of sync, and recovery is manual.

## Why Terraform's own guards don't help

| Mechanism | Why it falls short |
|---|---|
| `lifecycle.prevent_destroy` | Deleting the resource block deletes the `lifecycle` guard with it — so it does nothing on the most common destroy path |
| `check` blocks | Failed assertions are warnings; they never stop a plan or an apply. They also disappear with the resource block |
| `precondition` / `postcondition` | Disappear with the resource block, same as above |
| A custom provider plugin | Terraform's plugin model gives a provider no view of the overall plan — only its own resource types |

**Any guard written inside HCL disappears at the exact moment the resource is removed from HCL.** A guard only works if it inspects the plan from the outside.

## Why existing tools don't cover it

- **OPA/conftest, Checkov, Trivy, cfn-guard** — general policy engines. You can write a rule for this yourself, and teams do, but there is no ready-made answer and everyone reimplements it.
- **tflint** — closest in spirit, using deep checks against the Google Cloud API to predict apply failures, but it reads HCL rather than a plan, so destroy-related failures are out of reach by construction.
- **terrasafe, terraguard** — plan-based deletion guards built on allowlists. Neither looks at `deletion_protection`.
- **`gcloud beta terraform vet`** — official, but built around CFT constraints and not aimed at destroy detection.

No maintained tool addresses `deletion_protection` and destroy directly.

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

| Input | Default | Description |
| --- | --- | --- |
| `plan` | *required* | Path to the output of `terraform show -json` |
| `check` | every check | Name of a single check to run |
| `format` | `github` | `text`, `markdown`, `github` or `json` |
| `fail-on` | `error` | `error`, `warn` or `never` |
| `version` | `latest` | Release tag to install |

Outputs: `findings` (JSON), `error-count`, `warn-count`.

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

## Where this is going

`deletion_protection` is one instance of a broader class: failures that only appear once `apply` starts. Others fit the same frame of reading a plan and listing what will break.

- **The apply service account lacks a permission.** A plan needs read access; an apply needs write access. AWS ships [`aws_iam_principal_policy_simulation`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/iam_principal_policy_simulation) to check this at plan time. Google Cloud has no equivalent.
- **A required API is not enabled.**

## Scope

Google Cloud only. These failures come from cloud-specific API behaviour, and there is little to share across providers. Depth over breadth.
