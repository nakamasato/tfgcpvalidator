# Design

Why `tfgcpvalidator` exists, and why the obvious alternatives do not cover the
same ground. The README carries the short version; this is the long one.

## The problem

A standard Terraform pipeline assumes that `terraform plan` is an accurate
preview of `terraform apply`. On Google Cloud, `deletion_protection` breaks that
assumption.

The check is enforced by the Google Cloud API, which means it only happens
during `apply`. `terraform plan` never verifies that a resource is actually
deletable. So:

1. The pull request plan is green, and reviewers approve it.
2. `apply` fails after the merge with `Error, failed to delete instance because deletion_protection is set to true`.
3. The problem surfaces *after* review, the one place designed to catch it.

## Why a failed apply is worse than it sounds

Terraform destroys resources in dependency order. Deleting a
`google_sql_database_instance` means Terraform first deletes its children —
`google_sql_database`, `google_sql_user` — and only then reaches the instance,
where `deletion_protection` stops it.

**The databases are gone. The protected instance remains, empty.** The resource
you asked Terraform to protect survives, and the data you actually cared about
does not.

This is reported in
[hashicorp/terraform#33732](https://github.com/hashicorp/terraform/issues/33732)
and
[terraform-provider-google#7869](https://github.com/hashicorp/terraform-provider-google/issues/7869).
Both are closed with no fix. It has to be guarded from the outside.

A partial apply also leaves state and reality out of sync, and recovery is
manual.

## Why Terraform's own guards don't help

| Mechanism | Why it falls short |
|---|---|
| `lifecycle.prevent_destroy` | Deleting the resource block deletes the `lifecycle` guard with it — so it does nothing on the most common destroy path |
| `check` blocks | Failed assertions are warnings; they never stop a plan or an apply. They also disappear with the resource block |
| `precondition` / `postcondition` | Disappear with the resource block, same as above |
| A custom provider plugin | Terraform's plugin model gives a provider no view of the overall plan — only its own resource types |

**Any guard written inside HCL disappears at the exact moment the resource is
removed from HCL.** A guard only works if it inspects the plan from the outside.

## Why existing tools don't cover it

- **OPA/conftest, Checkov, Trivy, cfn-guard** — general policy engines. You can
  write a rule for this yourself, and teams do, but there is no ready-made
  answer and everyone reimplements it.
- **tflint** — closest in spirit, using deep checks against the Google Cloud API
  to predict apply failures, but it reads HCL rather than a plan, so
  destroy-related failures are out of reach by construction.
- **terrasafe, terraguard** — plan-based deletion guards built on allowlists.
  Neither looks at `deletion_protection`.
- **`gcloud beta terraform vet`** — official, but built around CFT constraints
  and not aimed at destroy detection.

No maintained tool addresses `deletion_protection` and destroy directly.

## Design decisions

**Read a plan, not HCL.** The failure only exists in the plan: it is the
combination of a delete action and a protection value already in state. HCL
alone cannot express it, which is the same reason every in-HCL guard fails.

**Match field names, not resource types.** Google Cloud adds deletion
protection to new resource types continuously — between provider `6.44.0` and
`7.41.0` the number of protection-bearing fields went from 48 to 868. A list of
resource types is stale on the next provider release; a list of field names is
not. See [architecture.md](architecture.md) for how the match runs and how the
name list is kept honest against the real provider schema.

**Report, do not mutate.** The tool never edits HCL or state. It prints what
will break and the one-line change that fixes it, and leaves the decision to the
author.

## Where this is going

`deletion_protection` is one instance of a broader class: failures that only
appear once `apply` starts. Others fit the same frame of reading a plan and
listing what will break.

- **The apply service account lacks a permission.** A plan needs read access; an
  apply needs write access. AWS ships
  [`aws_iam_principal_policy_simulation`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/iam_principal_policy_simulation)
  to check this at plan time. Google Cloud has no equivalent.
- **A required API is not enabled.**

## Scope

Google Cloud only. These failures come from cloud-specific API behaviour, and
there is little to share across providers. Depth over breadth.
