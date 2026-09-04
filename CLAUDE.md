# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
go build ./cmd/tfgcpvalidator            # build the CLI
go test -race ./...                      # what CI runs
go test ./internal/check/destroy -run TestName   # a single test
gofmt -l .                               # must print nothing; CI fails on any output
go vet ./...
shellcheck scripts/*.sh                  # the action's shell is linted
bash scripts/comment_test.sh             # comment.sh against a stubbed `gh`
```

Running the tool on a plan:

```bash
tfgcpvalidator validate --plan tfplan.json           # every check
tfgcpvalidator validate destroy --plan tfplan.json   # one check
```

Provider schema audit (also how the test fixture is generated) — needs a
`terraform providers schema -json` dump, see `docs/architecture.md`:

```bash
go run ./tools/schemaaudit -schema schema.json -audit
go run ./tools/schemaaudit -schema schema.json -provider-version 7.41.0 -fixture
```

## Architecture

`tfplan.json` → `internal/plan` (decode) → `internal/check` registry →
`internal/check/destroy` → `[]check.Finding` → `internal/report` (text /
markdown / github / json) and the exit code via `--fail-on`.

The checks see nothing but the plan JSON: no Google Cloud API call, no HCL, no
state, no provider schema. Keep it that way — it is what makes a released
binary work against provider versions that do not exist yet.

Non-obvious invariants:

- **Protection is read from `change.before`, never `after`.** Terraform deletes
  using the value already in state, so a plan that sets the flag to `false`
  *and* removes the resource still fails.
- **A `MaxItems=1` block is a one-element list in the plan.** `plan.Lookup`
  tries every element when a path hits a list; that is what makes
  `settings.deletion_protection_enabled` resolve.
- **The rule table in `internal/check/destroy` holds field names and blocking
  values, never resource types.** A resource type that gains a known field in a
  future provider release is covered with no change here. The `google_` type
  prefix is the counterweight: the same names exist on other providers.
- Nested paths are declared explicitly rather than searched for recursively — a
  blind search matches fields that describe some *other* resource.
- A protection-shaped field left out of the rule table must be recorded in
  `ExcludedPaths()` with a reason, or the weekly schema audit fails.

Checks are wired in `cmd/tfgcpvalidator/root.go` via `check.NewRegistry`.

Deeper background: `docs/architecture.md` (how the check decides, and provider
version drift), `docs/design.md` (why the tool exists).

## Release and action wiring

- Releases are release-please (`release-please-config.json`); it owns the tag,
  the release and its notes. goreleaser only appends binaries to that release.
- The composite action reads the version to install from
  `.release-please-manifest.json` in its own checkout, so `@v0` installs the
  binary matching the action code that ref points at.
- `.goreleaser.yaml`'s `archives.name_template` is the filename `action.yml`
  builds. Changing it breaks every pinned action version.
- `ci.yml` runs the action on this repository, against the fixture plan, with a
  branch build of the binary on `PATH` — the action prefers a `tfgcpvalidator`
  already on `PATH` over downloading a release. Label a pull request
  `test-action` to also have it post its comment.
