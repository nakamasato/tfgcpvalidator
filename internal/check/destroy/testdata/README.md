# schema_shaped_plan.json

Generated, never hand-edited. Every field name, type and nesting level is
resolved against a pinned provider schema before it is written, so the file
fails if this tool's idea of a Terraform plan diverges from the provider's.

Shaped for: `registry.terraform.io/hashicorp/google` **7.41.0**, recorded in the
document itself at `configuration.provider_config.google.version_constraint`.

## Regenerating

```bash
mkdir /tmp/schema && cd /tmp/schema
cat > versions.tf <<'TF'
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "7.41.0"
    }
  }
}
TF
terraform init -backend=false
terraform providers schema -json > schema.json
```

Then, from the repository root:

```bash
go run ./tools/schemaaudit -schema /tmp/schema/schema.json -provider-version 7.41.0 -fixture
```

Bump the version in `versions.tf`, in `-provider-version`, and in
`wantProviderVersion` in `schema_shaped_test.go` together — the test fails if
the fixture and the test disagree about which provider it describes.

## Why a pinned version matters

The protection fields this tool looks for are not stable across provider
releases. Between google 6.44.0 and 7.41.0 the count of protection-bearing
fields went from 48 to 868, and resource types such as `google_alloydb_cluster`,
`google_redis_instance` and the Oracle Database family gained
`deletion_protection` along the way. A fixture is a statement about one provider
version and says nothing about any other.

The runtime check is not pinned to any version: it matches field names in a
plan, never resource types against a schema, so it works on provider releases
that do not exist yet. Only this fixture and `tools/schemaaudit` are
version-scoped, and neither ships to users.
