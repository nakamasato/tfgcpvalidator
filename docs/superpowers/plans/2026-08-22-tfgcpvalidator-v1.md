# tfgcpvalidator v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Go CLI and GitHub Action that reads a Terraform plan JSON and fails the build when a Google Cloud resource is being destroyed while a deletion-protection flag is still set.

**Architecture:** A `plan` package parses `terraform show -json` into a small normalized struct. A `check` package defines a `Check` interface, a `Finding` value type, and a registry that resolves check names. The `destroy` check is the only implementation in v1. A `report` package renders findings in four formats. The cobra CLI wires these together: `validate` runs every registered check, `validate destroy` runs one. Checks know nothing about output format or exit codes.

**Tech Stack:** Go 1.24, `github.com/spf13/cobra`, goreleaser, GitHub Actions composite action.

**Spec:** `docs/superpowers/specs/2026-08-22-tfgcpvalidator-design.md`

## Global Constraints

- Go module path is `github.com/nakamasato/tfgcpvalidator`.
- Go 1.24 or later.
- All user-facing strings (CLI output, flag help, action descriptions) are **English**, matching README.md. Code comments and this plan are Japanese-friendly but comments should follow the repo rule: write only non-obvious WHY, never WHAT.
- Exit codes are fixed: `0` = no findings above the `--fail-on` threshold, `1` = findings above the threshold, `2` = tool error (unreadable plan, bad flag value).
- The destroy check must NOT contain a list of Google Cloud resource types. It keys off protection field names only.
- Output format is never switched implicitly based on environment variables.
- Only `github.com/spf13/cobra` may be added as a direct dependency. Everything else uses the standard library.
- Every task ends with a commit.

---

### Task 1: Module bootstrap and the `plan` package

**Files:**
- Create: `go.mod`
- Create: `internal/plan/plan.go`
- Test: `internal/plan/plan_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Plan struct { FormatVersion string; ResourceChanges []ResourceChange }`
  - `type ResourceChange struct { Address, Mode, Type, Name, Deposed string; Change Change }`
  - `type Change struct { Actions []string; Before, After map[string]any }`
  - `func (c Change) HasAction(action string) bool`
  - `func (rc ResourceChange) IsManaged() bool`
  - `func Parse(r io.Reader) (*Plan, error)`
  - `func Load(path string) (*Plan, error)`
  - `func Lookup(v any, path string) (any, bool)`

- [ ] **Step 1: Initialize the module**

```bash
go mod init github.com/nakamasato/tfgcpvalidator
```

- [ ] **Step 2: Write the failing tests**

Create `internal/plan/plan_test.go`:

```go
package plan

import (
	"strings"
	"testing"
)

const minimalPlan = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "google_sql_database_instance.main",
      "mode": "managed",
      "type": "google_sql_database_instance",
      "name": "main",
      "change": {
        "actions": ["delete"],
        "before": {
          "deletion_protection": true,
          "settings": [{"deletion_protection_enabled": true}]
        },
        "after": null
      }
    }
  ]
}`

func TestParse(t *testing.T) {
	p, err := Parse(strings.NewReader(minimalPlan))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if p.FormatVersion != "1.2" {
		t.Errorf("FormatVersion = %q, want %q", p.FormatVersion, "1.2")
	}
	if len(p.ResourceChanges) != 1 {
		t.Fatalf("len(ResourceChanges) = %d, want 1", len(p.ResourceChanges))
	}
	rc := p.ResourceChanges[0]
	if rc.Address != "google_sql_database_instance.main" {
		t.Errorf("Address = %q", rc.Address)
	}
	if !rc.IsManaged() {
		t.Error("IsManaged() = false, want true")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse(strings.NewReader("not json")); err == nil {
		t.Fatal("Parse() error = nil, want an error")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("does-not-exist.json"); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestHasAction(t *testing.T) {
	tests := []struct {
		name    string
		actions []string
		action  string
		want    bool
	}{
		{"delete only", []string{"delete"}, "delete", true},
		{"replace delete first", []string{"delete", "create"}, "delete", true},
		{"replace create first", []string{"create", "delete"}, "delete", true},
		{"create only", []string{"create"}, "delete", false},
		{"no-op", []string{"no-op"}, "delete", false},
		{"empty", nil, "delete", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Change{Actions: tt.actions}
			if got := c.HasAction(tt.action); got != tt.want {
				t.Errorf("HasAction(%q) = %v, want %v", tt.action, got, tt.want)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	before := map[string]any{
		"deletion_protection": true,
		"deletion_policy":     "PREVENT",
		"settings":            []any{map[string]any{"deletion_protection_enabled": true}},
		"empty_block":         []any{},
	}

	tests := []struct {
		name   string
		path   string
		want   any
		wantOK bool
	}{
		{"top level bool", "deletion_protection", true, true},
		{"top level string", "deletion_policy", "PREVENT", true},
		{"through single element list", "settings.deletion_protection_enabled", true, true},
		{"missing key", "nope", nil, false},
		{"missing nested key", "settings.nope", nil, false},
		{"empty list", "empty_block.anything", nil, false},
		{"descend into scalar", "deletion_policy.nope", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(before, tt.path)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("Lookup(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestLookupNilMap(t *testing.T) {
	var before map[string]any
	if _, ok := Lookup(before, "deletion_protection"); ok {
		t.Error("Lookup() on a nil map returned ok = true, want false")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/plan/...`
Expected: FAIL — the package does not compile because `plan.go` does not exist.

- [ ] **Step 4: Write the implementation**

Create `internal/plan/plan.go`:

```go
// Package plan reads the subset of `terraform show -json` output that checks need.
package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type Plan struct {
	FormatVersion   string           `json:"format_version"`
	ResourceChanges []ResourceChange `json:"resource_changes"`
}

type ResourceChange struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Deposed string `json:"deposed"`
	Change  Change `json:"change"`
}

type Change struct {
	Actions []string       `json:"actions"`
	Before  map[string]any `json:"before"`
	After   map[string]any `json:"after"`
}

func (c Change) HasAction(action string) bool {
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func (rc ResourceChange) IsManaged() bool { return rc.Mode == "managed" }

func Parse(r io.Reader) (*Plan, error) {
	var p Plan
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan json: %w", err)
	}
	return &p, nil
}

func Load(path string) (*Plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plan file: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Lookup walks a dot-separated path through decoded JSON.
// Terraform renders a single-instance block as a one-element list, so a list on
// the path is searched element by element rather than treated as a dead end.
func Lookup(v any, path string) (any, bool) {
	if path == "" {
		return v, v != nil
	}
	key, rest, _ := strings.Cut(path, ".")
	switch t := v.(type) {
	case map[string]any:
		next, ok := t[key]
		if !ok {
			return nil, false
		}
		return Lookup(next, rest)
	case []any:
		for _, elem := range t {
			if got, ok := Lookup(elem, path); ok {
				return got, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/plan/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/plan/
git commit -m "feat: add terraform plan json parser"
```

---

### Task 2: The `check` package

**Files:**
- Create: `internal/check/check.go`
- Create: `internal/check/registry.go`
- Test: `internal/check/check_test.go`
- Test: `internal/check/registry_test.go`

**Interfaces:**
- Consumes: `plan.Plan` from Task 1.
- Produces:
  - `type Severity int` with constants `Info`, `Warn`, `Error`; `func (s Severity) String() string`; `func (s Severity) MarshalJSON() ([]byte, error)`
  - `type Finding struct { Check string; Severity Severity; Address, Type, Message, Remediation string }`
  - `type Input struct { Plan *plan.Plan }`
  - `type Check interface { Name() string; Run(ctx context.Context, in Input) ([]Finding, error) }`
  - `type Registry struct{ ... }`; `func NewRegistry(checks ...Check) *Registry`; `func (r *Registry) All() []Check`; `func (r *Registry) Get(name string) (Check, bool)`; `func (r *Registry) Names() []string`
  - `func Run(ctx context.Context, checks []Check, in Input) ([]Finding, error)`
  - `type FailOn int` with constants `FailNever`, `FailOnWarn`, `FailOnError`; `func ParseFailOn(s string) (FailOn, error)`; `func ShouldFail(findings []Finding, f FailOn) bool`
  - `func Counts(findings []Finding) (errors, warns int)`

- [ ] **Step 1: Write the failing tests**

Create `internal/check/check_test.go`:

```go
package check

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSeverityString(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{Error, "error"},
		{Warn, "warn"},
		{Info, "info"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestSeverityMarshalJSON(t *testing.T) {
	b, err := json.Marshal(Finding{Severity: Error})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["severity"] != "error" {
		t.Errorf("severity = %v, want %q", decoded["severity"], "error")
	}
}

func TestParseFailOn(t *testing.T) {
	tests := []struct {
		in      string
		want    FailOn
		wantErr bool
	}{
		{"error", FailOnError, false},
		{"warn", FailOnWarn, false},
		{"never", FailNever, false},
		{"nonsense", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseFailOn(tt.in)
		if (err != nil) != tt.wantErr {
			t.Fatalf("ParseFailOn(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseFailOn(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestShouldFail(t *testing.T) {
	errFinding := Finding{Severity: Error}
	warnFinding := Finding{Severity: Warn}
	infoFinding := Finding{Severity: Info}

	tests := []struct {
		name     string
		findings []Finding
		failOn   FailOn
		want     bool
	}{
		{"error threshold with error", []Finding{errFinding}, FailOnError, true},
		{"error threshold with warn only", []Finding{warnFinding}, FailOnError, false},
		{"warn threshold with warn", []Finding{warnFinding}, FailOnWarn, true},
		{"warn threshold with info only", []Finding{infoFinding}, FailOnWarn, false},
		{"never with error", []Finding{errFinding}, FailNever, false},
		{"no findings", nil, FailOnError, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldFail(tt.findings, tt.failOn); got != tt.want {
				t.Errorf("ShouldFail() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCounts(t *testing.T) {
	findings := []Finding{{Severity: Error}, {Severity: Error}, {Severity: Warn}, {Severity: Info}}
	gotErrors, gotWarns := Counts(findings)
	if gotErrors != 2 || gotWarns != 1 {
		t.Errorf("Counts() = (%d, %d), want (2, 1)", gotErrors, gotWarns)
	}
}

type stubCheck struct {
	name     string
	findings []Finding
	err      error
}

func (s stubCheck) Name() string { return s.name }

func (s stubCheck) Run(context.Context, Input) ([]Finding, error) { return s.findings, s.err }

func TestRunAggregatesFindings(t *testing.T) {
	a := stubCheck{name: "a", findings: []Finding{{Check: "a"}}}
	b := stubCheck{name: "b", findings: []Finding{{Check: "b"}, {Check: "b"}}}

	got, err := Run(context.Background(), []Check{a, b}, Input{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(findings) = %d, want 3", len(got))
	}
}

func TestRunWrapsCheckError(t *testing.T) {
	boom := errors.New("boom")
	failing := stubCheck{name: "failing", err: boom}

	_, err := Run(context.Background(), []Check{failing}, Input{})
	if !errors.Is(err, boom) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, boom)
	}
}
```

Create `internal/check/registry_test.go`:

```go
package check

import (
	"slices"
	"testing"
)

func TestRegistryAll(t *testing.T) {
	r := NewRegistry(stubCheck{name: "a"}, stubCheck{name: "b"})
	if len(r.All()) != 2 {
		t.Errorf("len(All()) = %d, want 2", len(r.All()))
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry(stubCheck{name: "a"})

	if _, ok := r.Get("a"); !ok {
		t.Error(`Get("a") ok = false, want true`)
	}
	if _, ok := r.Get("missing"); ok {
		t.Error(`Get("missing") ok = true, want false`)
	}
}

func TestRegistryNames(t *testing.T) {
	r := NewRegistry(stubCheck{name: "b"}, stubCheck{name: "a"})
	if got := r.Names(); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("Names() = %v, want [b a]", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/check/...`
Expected: FAIL — the package does not compile.

- [ ] **Step 3: Write `check.go`**

```go
// Package check defines the contract every validation implements and the
// vocabulary they report in.
package check

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nakamasato/tfgcpvalidator/internal/plan"
)

type Severity int

const (
	Info Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warn:
		return "warn"
	default:
		return "info"
	}
}

func (s Severity) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

type Finding struct {
	Check       string   `json:"check"`
	Severity    Severity `json:"severity"`
	Address     string   `json:"address"`
	Type        string   `json:"type"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation"`
}

type Input struct {
	Plan *plan.Plan
}

type Check interface {
	Name() string
	Run(ctx context.Context, in Input) ([]Finding, error)
}

func Run(ctx context.Context, checks []Check, in Input) ([]Finding, error) {
	var out []Finding
	for _, c := range checks {
		findings, err := c.Run(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", c.Name(), err)
		}
		out = append(out, findings...)
	}
	return out, nil
}

type FailOn int

const (
	FailNever FailOn = iota
	FailOnWarn
	FailOnError
)

func ParseFailOn(s string) (FailOn, error) {
	switch s {
	case "never":
		return FailNever, nil
	case "warn":
		return FailOnWarn, nil
	case "error":
		return FailOnError, nil
	}
	return 0, fmt.Errorf("unknown fail-on value %q (want error, warn or never)", s)
}

func ShouldFail(findings []Finding, f FailOn) bool {
	var threshold Severity
	switch f {
	case FailNever:
		return false
	case FailOnWarn:
		threshold = Warn
	case FailOnError:
		threshold = Error
	}
	for _, finding := range findings {
		if finding.Severity >= threshold {
			return true
		}
	}
	return false
}

func Counts(findings []Finding) (errors, warns int) {
	for _, f := range findings {
		switch f.Severity {
		case Error:
			errors++
		case Warn:
			warns++
		}
	}
	return errors, warns
}
```

- [ ] **Step 4: Write `registry.go`**

```go
package check

type Registry struct {
	checks []Check
}

func NewRegistry(checks ...Check) *Registry { return &Registry{checks: checks} }

func (r *Registry) All() []Check { return r.checks }

func (r *Registry) Get(name string) (Check, bool) {
	for _, c := range r.checks {
		if c.Name() == name {
			return c, true
		}
	}
	return nil, false
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.checks))
	for _, c := range r.checks {
		names = append(names, c.Name())
	}
	return names
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/check/
git commit -m "feat: add check interface, registry and severity handling"
```

---

### Task 3: The `destroy` check

**Files:**
- Create: `internal/check/destroy/destroy.go`
- Test: `internal/check/destroy/destroy_test.go`

**Interfaces:**
- Consumes: `plan.Plan`, `plan.Lookup` (Task 1); `check.Input`, `check.Finding`, `check.Error` (Task 2).
- Produces:
  - `const Name = "destroy"`
  - `type Check struct{}`; `func New() *Check`; `func (*Check) Name() string`; `func (*Check) Run(ctx context.Context, in check.Input) ([]check.Finding, error)`

This is the only task that encodes Google Cloud knowledge. Read spec section 5 before starting.

- [ ] **Step 1: Write the failing tests**

Create `internal/check/destroy/destroy_test.go`:

```go
package destroy

import (
	"context"
	"strings"
	"testing"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/plan"
)

func change(actions []string, before map[string]any) plan.Change {
	return plan.Change{Actions: actions, Before: before}
}

func run(t *testing.T, changes ...plan.ResourceChange) []check.Finding {
	t.Helper()
	findings, err := New().Run(context.Background(), check.Input{
		Plan: &plan.Plan{ResourceChanges: changes},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return findings
}

func managed(address, resourceType string, c plan.Change) plan.ResourceChange {
	return plan.ResourceChange{Address: address, Mode: "managed", Type: resourceType, Change: c}
}

func TestProtectedDelete(t *testing.T) {
	tests := []struct {
		name    string
		before  map[string]any
		wantHit bool
	}{
		{"deletion_protection true", map[string]any{"deletion_protection": true}, true},
		{"deletion_protection false", map[string]any{"deletion_protection": false}, false},
		{"no protection field", map[string]any{"name": "x"}, false},
		{"deletion_policy PREVENT", map[string]any{"deletion_policy": "PREVENT"}, true},
		{"deletion_policy DELETE", map[string]any{"deletion_policy": "DELETE"}, false},
		{
			"nested settings deletion_protection_enabled true",
			map[string]any{"settings": []any{map[string]any{"deletion_protection_enabled": true}}},
			true,
		},
		{
			"nested settings deletion_protection_enabled false",
			map[string]any{"settings": []any{map[string]any{"deletion_protection_enabled": false}}},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := run(t, managed("google_x.a", "google_x", change([]string{"delete"}, tt.before)))
			if tt.wantHit && len(findings) != 1 {
				t.Fatalf("len(findings) = %d, want 1", len(findings))
			}
			if !tt.wantHit && len(findings) != 0 {
				t.Fatalf("len(findings) = %d, want 0: %+v", len(findings), findings)
			}
			if tt.wantHit && findings[0].Severity != check.Error {
				t.Errorf("Severity = %v, want error", findings[0].Severity)
			}
		})
	}
}

func TestActions(t *testing.T) {
	protected := map[string]any{"deletion_protection": true}

	tests := []struct {
		name    string
		actions []string
		wantHit bool
	}{
		{"delete", []string{"delete"}, true},
		{"replace delete first", []string{"delete", "create"}, true},
		{"replace create first", []string{"create", "delete"}, true},
		{"create", []string{"create"}, false},
		{"update", []string{"update"}, false},
		{"no-op", []string{"no-op"}, false},
		{"read", []string{"read"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := run(t, managed("google_x.a", "google_x", change(tt.actions, protected)))
			if got := len(findings) > 0; got != tt.wantHit {
				t.Errorf("findings present = %v, want %v", got, tt.wantHit)
			}
		})
	}
}

func TestReplaceMessageMentionsReplace(t *testing.T) {
	findings := run(t, managed("google_x.a", "google_x",
		change([]string{"delete", "create"}, map[string]any{"deletion_protection": true})))
	if len(findings) != 1 {
		t.Fatalf("len(findings) = %d, want 1", len(findings))
	}
	if !strings.Contains(findings[0].Message, "replaced") {
		t.Errorf("Message = %q, want it to mention that the resource is replaced", findings[0].Message)
	}
}

func TestDeleteMessageDoesNotMentionReplace(t *testing.T) {
	findings := run(t, managed("google_x.a", "google_x",
		change([]string{"delete"}, map[string]any{"deletion_protection": true})))
	if strings.Contains(findings[0].Message, "replaced") {
		t.Errorf("Message = %q, want a plain destroy message", findings[0].Message)
	}
}

func TestRemediationDescribesTwoApplies(t *testing.T) {
	findings := run(t, managed("google_x.a", "google_x",
		change([]string{"delete"}, map[string]any{"deletion_protection": true})))
	got := findings[0].Remediation
	if !strings.Contains(got, "deletion_protection = false") {
		t.Errorf("Remediation = %q, want the exact field change", got)
	}
	if !strings.Contains(got, "single apply") {
		t.Errorf("Remediation = %q, want it to explain that one apply is not enough", got)
	}
}

func TestDataSourcesAreIgnored(t *testing.T) {
	rc := plan.ResourceChange{
		Address: "data.google_x.a",
		Mode:    "data",
		Type:    "google_x",
		Change:  change([]string{"delete"}, map[string]any{"deletion_protection": true}),
	}
	if findings := run(t, rc); len(findings) != 0 {
		t.Errorf("len(findings) = %d, want 0", len(findings))
	}
}

func TestDeposedResourcesAreChecked(t *testing.T) {
	rc := managed("google_x.a", "google_x", change([]string{"delete"}, map[string]any{"deletion_protection": true}))
	rc.Deposed = "abc123"
	if findings := run(t, rc); len(findings) != 1 {
		t.Errorf("len(findings) = %d, want 1", len(findings))
	}
}

func TestAddressAndTypeArePropagated(t *testing.T) {
	findings := run(t, managed(
		"module.db.google_sql_database_instance.main",
		"google_sql_database_instance",
		change([]string{"delete"}, map[string]any{"deletion_protection": true}),
	))
	if findings[0].Address != "module.db.google_sql_database_instance.main" {
		t.Errorf("Address = %q", findings[0].Address)
	}
	if findings[0].Type != "google_sql_database_instance" {
		t.Errorf("Type = %q", findings[0].Type)
	}
	if findings[0].Check != Name {
		t.Errorf("Check = %q, want %q", findings[0].Check, Name)
	}
}

func TestBothProtectionsOnOneResourceReportBoth(t *testing.T) {
	before := map[string]any{
		"deletion_protection": true,
		"settings":            []any{map[string]any{"deletion_protection_enabled": true}},
	}
	if findings := run(t, managed("google_sql_database_instance.main", "google_sql_database_instance", change([]string{"delete"}, before))); len(findings) != 2 {
		t.Errorf("len(findings) = %d, want 2 (both protections block the apply)", len(findings))
	}
}

func TestNilPlanIsAnError(t *testing.T) {
	if _, err := New().Run(context.Background(), check.Input{}); err == nil {
		t.Fatal("Run() error = nil, want an error when the plan is missing")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/check/destroy/...`
Expected: FAIL — the package does not compile.

- [ ] **Step 3: Write the implementation**

Create `internal/check/destroy/destroy.go`:

```go
// Package destroy reports resources that a plan deletes while a Google Cloud
// deletion-protection flag is still set on them.
package destroy

import (
	"context"
	"errors"
	"fmt"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/plan"
)

const Name = "destroy"

type Check struct{}

func New() *Check { return &Check{} }

func (*Check) Name() string { return Name }

// rule is one protection field. Matching on field names rather than resource
// types is what keeps this check working for resource types that do not exist
// yet.
type rule struct {
	path    string
	matches func(any) bool
	// fix is the change that has to land in its own apply before the removal.
	fix string
}

// google_sql_database_instance carries two independent protections: the
// Terraform-level deletion_protection and the API-level
// settings.deletion_protection_enabled. Clearing one leaves the other blocking.
var rules = []rule{
	{path: "deletion_protection", matches: isTrue, fix: "deletion_protection = false"},
	{path: "deletion_policy", matches: isPrevent, fix: `deletion_policy = "DELETE"`},
	{path: "settings.deletion_protection_enabled", matches: isTrue, fix: "settings.deletion_protection_enabled = false"},
}

func isTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func isPrevent(v any) bool {
	s, ok := v.(string)
	return ok && s == "PREVENT"
}

func (*Check) Run(_ context.Context, in check.Input) ([]check.Finding, error) {
	if in.Plan == nil {
		return nil, errors.New("no plan supplied")
	}

	var findings []check.Finding
	for _, rc := range in.Plan.ResourceChanges {
		if !rc.IsManaged() || !rc.Change.HasAction("delete") {
			continue
		}
		// Terraform deletes using the value already in state, so the protection
		// that matters is the one in Before, never the one in After.
		for _, r := range rules {
			v, ok := plan.Lookup(rc.Change.Before, r.path)
			if !ok || !r.matches(v) {
				continue
			}
			findings = append(findings, check.Finding{
				Check:       Name,
				Severity:    check.Error,
				Address:     rc.Address,
				Type:        rc.Type,
				Message:     message(rc, r),
				Remediation: remediation(r),
			})
		}
	}
	return findings, nil
}

func message(rc plan.ResourceChange, r rule) string {
	if rc.Change.HasAction("create") {
		return fmt.Sprintf("%s is set and this resource is being replaced. A replace deletes before it creates, so the apply will fail.", r.path)
	}
	return fmt.Sprintf("%s is set and this resource is being destroyed. The apply will fail.", r.path)
}

func remediation(r rule) string {
	return fmt.Sprintf("Apply %s on its own first, then apply the removal. Terraform deletes with the value already in state, so both changes cannot land in a single apply.", r.fix)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/check/destroy/
git commit -m "feat: add destroy check for deletion protection flags"
```

---

### Task 4: The `report` package

**Files:**
- Create: `internal/report/report.go`
- Create: `internal/report/text.go`
- Create: `internal/report/markdown.go`
- Create: `internal/report/github.go`
- Create: `internal/report/json.go`
- Test: `internal/report/report_test.go`

**Interfaces:**
- Consumes: `check.Finding`, `check.Counts` (Task 2).
- Produces:
  - `type Reporter interface { Report(w io.Writer, findings []check.Finding) error }`
  - `func For(format string) (Reporter, error)`
  - `func Formats() []string`

- [ ] **Step 1: Write the failing tests**

Create `internal/report/report_test.go`:

```go
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

var sample = []check.Finding{
	{
		Check:       "destroy",
		Severity:    check.Error,
		Address:     "google_sql_database_instance.main",
		Type:        "google_sql_database_instance",
		Message:     "deletion_protection is set and this resource is being destroyed. The apply will fail.",
		Remediation: "Apply deletion_protection = false on its own first.",
	},
}

func render(t *testing.T, format string, findings []check.Finding) string {
	t.Helper()
	r, err := For(format)
	if err != nil {
		t.Fatalf("For(%q) error = %v", format, err)
	}
	var buf bytes.Buffer
	if err := r.Report(&buf, findings); err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	return buf.String()
}

func TestForUnknownFormat(t *testing.T) {
	if _, err := For("yaml"); err == nil {
		t.Fatal("For() error = nil, want an error")
	}
}

func TestFormats(t *testing.T) {
	for _, f := range Formats() {
		if _, err := For(f); err != nil {
			t.Errorf("For(%q) error = %v, but it is advertised by Formats()", f, err)
		}
	}
}

func TestTextIncludesEverything(t *testing.T) {
	got := render(t, "text", sample)
	for _, want := range []string{"ERROR", "google_sql_database_instance.main", "deletion_protection is set", "Apply deletion_protection = false"} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "1 error") {
		t.Errorf("text output missing a summary line:\n%s", got)
	}
}

func TestTextWithNoFindings(t *testing.T) {
	got := render(t, "text", nil)
	if !strings.Contains(got, "No issues found") {
		t.Errorf("text output = %q, want a clear all-clear message", got)
	}
}

func TestMarkdownIsATable(t *testing.T) {
	got := render(t, "markdown", sample)
	if !strings.Contains(got, "|") || !strings.Contains(got, "---") {
		t.Errorf("markdown output is not a table:\n%s", got)
	}
	if !strings.Contains(got, "google_sql_database_instance.main") {
		t.Errorf("markdown output missing the address:\n%s", got)
	}
}

func TestMarkdownWithNoFindings(t *testing.T) {
	got := render(t, "markdown", nil)
	if !strings.Contains(got, "No issues found") {
		t.Errorf("markdown output = %q, want a clear all-clear message", got)
	}
}

func TestGitHubEmitsWorkflowCommands(t *testing.T) {
	got := render(t, "github", sample)
	if !strings.HasPrefix(got, "::error ") {
		t.Errorf("github output does not start with a workflow command:\n%s", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("github output must be one line per finding:\n%s", got)
	}
	if !strings.Contains(got, "google_sql_database_instance.main") {
		t.Errorf("github output missing the address:\n%s", got)
	}
}

func TestGitHubEscapesNewlines(t *testing.T) {
	findings := []check.Finding{{
		Severity: check.Error,
		Check:    "destroy",
		Address:  "google_x.a",
		Message:  "line one\nline two",
	}}
	got := render(t, "github", findings)
	if strings.Count(got, "\n") != 1 {
		t.Errorf("newlines inside a message must be escaped, got:\n%q", got)
	}
	if !strings.Contains(got, "%0A") {
		t.Errorf("github output should escape newlines as %%0A, got:\n%q", got)
	}
}

func TestJSONIsParseable(t *testing.T) {
	got := render(t, "json", sample)
	var decoded struct {
		Findings []struct {
			Severity string `json:"severity"`
			Address  string `json:"address"`
		} `json:"findings"`
		ErrorCount int `json:"error_count"`
		WarnCount  int `json:"warn_count"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("json output is not valid JSON: %v\n%s", err, got)
	}
	if len(decoded.Findings) != 1 || decoded.Findings[0].Severity != "error" {
		t.Errorf("unexpected findings: %+v", decoded.Findings)
	}
	if decoded.ErrorCount != 1 || decoded.WarnCount != 0 {
		t.Errorf("counts = (%d, %d), want (1, 0)", decoded.ErrorCount, decoded.WarnCount)
	}
}

func TestJSONWithNoFindingsIsAnEmptyArray(t *testing.T) {
	got := render(t, "json", nil)
	if !strings.Contains(got, `"findings": []`) {
		t.Errorf("json output should carry an empty array rather than null:\n%s", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/report/...`
Expected: FAIL — the package does not compile.

- [ ] **Step 3: Write `report.go`**

```go
// Package report renders findings for humans and for machines.
package report

import (
	"fmt"
	"io"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type Reporter interface {
	Report(w io.Writer, findings []check.Finding) error
}

func Formats() []string { return []string{"text", "markdown", "github", "json"} }

func For(format string) (Reporter, error) {
	switch format {
	case "text":
		return textReporter{}, nil
	case "markdown":
		return markdownReporter{}, nil
	case "github":
		return githubReporter{}, nil
	case "json":
		return jsonReporter{}, nil
	}
	return nil, fmt.Errorf("unknown format %q (want one of text, markdown, github, json)", format)
}
```

- [ ] **Step 4: Write `text.go`**

```go
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type textReporter struct{}

func (textReporter) Report(w io.Writer, findings []check.Finding) error {
	if len(findings) == 0 {
		_, err := fmt.Fprintln(w, "No issues found.")
		return err
	}

	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "%s  %s\n", strings.ToUpper(f.Severity.String()), f.Address)
		fmt.Fprintf(&b, "  %s\n", f.Message)
		if f.Remediation != "" {
			fmt.Fprintf(&b, "  fix: %s\n", f.Remediation)
		}
		b.WriteString("\n")
	}

	errCount, warnCount := check.Counts(findings)
	fmt.Fprintf(&b, "%s, %s\n", pluralize(errCount, "error"), pluralize(warnCount, "warning"))

	_, err := io.WriteString(w, b.String())
	return err
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
```

- [ ] **Step 5: Write `markdown.go`**

```go
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type markdownReporter struct{}

func (markdownReporter) Report(w io.Writer, findings []check.Finding) error {
	if len(findings) == 0 {
		_, err := fmt.Fprintln(w, "No issues found.")
		return err
	}

	var b strings.Builder
	b.WriteString("| Severity | Resource | Problem | Fix |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n",
			f.Severity, f.Address, escapePipes(f.Message), escapePipes(f.Remediation))
	}

	errCount, warnCount := check.Counts(findings)
	fmt.Fprintf(&b, "\n%s, %s\n", pluralize(errCount, "error"), pluralize(warnCount, "warning"))

	_, err := io.WriteString(w, b.String())
	return err
}

// A pipe inside a cell would end the column early.
func escapePipes(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", `\|`)
}
```

- [ ] **Step 6: Write `github.go`**

```go
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type githubReporter struct{}

func (githubReporter) Report(w io.Writer, findings []check.Finding) error {
	for _, f := range findings {
		level := "error"
		if f.Severity < check.Error {
			level = "warning"
		}
		body := f.Message
		if f.Remediation != "" {
			body += " " + f.Remediation
		}
		if _, err := fmt.Fprintf(w, "::%s title=tfgcpvalidator/%s::%s: %s\n",
			level, f.Check, f.Address, escapeCommand(body)); err != nil {
			return err
		}
	}
	return nil
}

// Workflow commands are line-oriented, so the payload has to survive on one line.
func escapeCommand(s string) string {
	r := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	return r.Replace(s)
}
```

- [ ] **Step 7: Write `json.go`**

```go
package report

import (
	"encoding/json"
	"io"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type jsonReporter struct{}

type jsonPayload struct {
	Findings   []check.Finding `json:"findings"`
	ErrorCount int             `json:"error_count"`
	WarnCount  int             `json:"warn_count"`
}

func (jsonReporter) Report(w io.Writer, findings []check.Finding) error {
	// Consumers index into findings, so an absent list has to be [] and not null.
	if findings == nil {
		findings = []check.Finding{}
	}
	errCount, warnCount := check.Counts(findings)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonPayload{Findings: findings, ErrorCount: errCount, WarnCount: warnCount})
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/report/
git commit -m "feat: add text, markdown, github and json reporters"
```

---

### Task 5: The CLI

**Files:**
- Create: `cmd/tfgcpvalidator/main.go`
- Create: `cmd/tfgcpvalidator/root.go`
- Create: `cmd/tfgcpvalidator/validate.go`
- Test: `cmd/tfgcpvalidator/validate_test.go`
- Modify: `go.mod` (adds cobra)

**Interfaces:**
- Consumes: `plan.Load` (Task 1); `check.Registry`, `check.Run`, `check.ParseFailOn`, `check.ShouldFail` (Task 2); `destroy.New` (Task 3); `report.For` (Task 4).
- Produces: the `tfgcpvalidator` binary. `func newRootCmd() *cobra.Command` and `type exitCodeError struct{ code int }` are internal to package main.

- [ ] **Step 1: Add cobra**

```bash
go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write the failing tests**

Create `cmd/tfgcpvalidator/validate_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const protectedDeletePlan = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "google_sql_database_instance.main",
      "mode": "managed",
      "type": "google_sql_database_instance",
      "name": "main",
      "change": {"actions": ["delete"], "before": {"deletion_protection": true}, "after": null}
    }
  ]
}`

const cleanPlan = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "google_storage_bucket.assets",
      "mode": "managed",
      "type": "google_storage_bucket",
      "name": "assets",
      "change": {"actions": ["create"], "before": null, "after": {"name": "assets"}}
    }
  ]
}`

func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tfplan.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// execute runs the CLI and returns stdout plus the error the command returned.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ec exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
	return 2
}

func TestValidateFailsOnProtectedDelete(t *testing.T) {
	out, err := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan))
	if got := exitCode(t, err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (output: %s)", got, out)
	}
	if !strings.Contains(out, "google_sql_database_instance.main") {
		t.Errorf("output missing the offending resource:\n%s", out)
	}
}

func TestValidateSucceedsOnCleanPlan(t *testing.T) {
	out, err := execute(t, "validate", "--plan", writePlan(t, cleanPlan))
	if got := exitCode(t, err); got != 0 {
		t.Fatalf("exit code = %d, want 0 (output: %s)", got, out)
	}
	if !strings.Contains(out, "No issues found") {
		t.Errorf("output = %q, want an all-clear message", out)
	}
}

func TestDestroySubcommandBehavesTheSame(t *testing.T) {
	out, err := execute(t, "validate", "destroy", "--plan", writePlan(t, protectedDeletePlan))
	if got := exitCode(t, err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (output: %s)", got, out)
	}
	if !strings.Contains(out, "google_sql_database_instance.main") {
		t.Errorf("output missing the offending resource:\n%s", out)
	}
}

func TestFailOnNeverStillReportsButExitsZero(t *testing.T) {
	out, err := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan), "--fail-on", "never")
	if got := exitCode(t, err); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if !strings.Contains(out, "google_sql_database_instance.main") {
		t.Errorf("--fail-on never must still print findings:\n%s", out)
	}
}

func TestMissingPlanFileIsAToolError(t *testing.T) {
	_, err := execute(t, "validate", "--plan", "no-such-file.json")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestUnknownFormatIsAToolError(t *testing.T) {
	_, err := execute(t, "validate", "--plan", writePlan(t, cleanPlan), "--format", "yaml")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestUnknownFailOnIsAToolError(t *testing.T) {
	_, err := execute(t, "validate", "--plan", writePlan(t, cleanPlan), "--fail-on", "sometimes")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestMissingPlanFlagIsAToolError(t *testing.T) {
	_, err := execute(t, "validate")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestJSONFormat(t *testing.T) {
	out, _ := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan), "--format", "json")
	if !strings.Contains(out, `"error_count": 1`) {
		t.Errorf("json output missing the error count:\n%s", out)
	}
}

func TestGitHubFormat(t *testing.T) {
	out, _ := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan), "--format", "github")
	if !strings.HasPrefix(out, "::error ") {
		t.Errorf("github output missing the workflow command:\n%s", out)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./cmd/...`
Expected: FAIL — package main does not compile.

- [ ] **Step 4: Write `root.go`**

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/check/destroy"
)

// exitCodeError carries a specific process exit code out through cobra so that
// "the check found something" stays distinguishable from "the tool broke".
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func registry() *check.Registry {
	return check.NewRegistry(destroy.New())
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "tfgcpvalidator",
		Short:         "Catch Terraform failures on Google Cloud at plan time",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newValidateCmd())
	return root
}
```

- [ ] **Step 5: Write `validate.go`**

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/plan"
	"github.com/nakamasato/tfgcpvalidator/internal/report"
)

type validateOpts struct {
	planPath string
	format   string
	failOn   string
}

func (o *validateOpts) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.planPath, "plan", "", "path to the output of `terraform show -json` (required)")
	cmd.Flags().StringVar(&o.format, "format", "text", "output format: text, markdown, github or json")
	cmd.Flags().StringVar(&o.failOn, "fail-on", "error", "exit non-zero when a finding reaches this severity: error, warn or never")
	_ = cmd.MarkFlagRequired("plan")
}

func newValidateCmd() *cobra.Command {
	reg := registry()
	opts := &validateOpts{}

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Run every check against a plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChecks(cmd, opts, reg.All())
		},
	}
	opts.bind(cmd)

	for _, c := range reg.All() {
		cmd.AddCommand(newCheckCmd(c))
	}
	return cmd
}

func newCheckCmd(c check.Check) *cobra.Command {
	opts := &validateOpts{}
	cmd := &cobra.Command{
		Use:   c.Name(),
		Short: fmt.Sprintf("Run only the %s check", c.Name()),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChecks(cmd, opts, []check.Check{c})
		},
	}
	opts.bind(cmd)
	return cmd
}

func runChecks(cmd *cobra.Command, o *validateOpts, checks []check.Check) error {
	failOn, err := check.ParseFailOn(o.failOn)
	if err != nil {
		return err
	}
	reporter, err := report.For(o.format)
	if err != nil {
		return err
	}
	p, err := plan.Load(o.planPath)
	if err != nil {
		return err
	}

	findings, err := check.Run(cmd.Context(), checks, check.Input{Plan: p})
	if err != nil {
		return err
	}
	if err := reporter.Report(cmd.OutOrStdout(), findings); err != nil {
		return err
	}

	if check.ShouldFail(findings, failOn) {
		return exitCodeError{code: 1}
	}
	return nil
}
```

- [ ] **Step 6: Write `main.go`**

```go
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}

	var ec exitCodeError
	if errors.As(err, &ec) {
		os.Exit(ec.code)
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 8: Verify the real binary end to end**

```bash
go build -o /tmp/tfgcpvalidator ./cmd/tfgcpvalidator
cat > /tmp/protected.json <<'JSON'
{"format_version":"1.2","resource_changes":[{"address":"google_sql_database_instance.main","mode":"managed","type":"google_sql_database_instance","name":"main","change":{"actions":["delete"],"before":{"deletion_protection":true},"after":null}}]}
JSON
/tmp/tfgcpvalidator validate --plan /tmp/protected.json; echo "exit=$?"
```

Expected: the finding is printed and `exit=1`.

```bash
/tmp/tfgcpvalidator validate --plan /tmp/protected.json --fail-on never; echo "exit=$?"
```

Expected: the same finding and `exit=0`.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum cmd/
git commit -m "feat: add tfgcpvalidator CLI"
```

---

### Task 6: CI

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: the Go module from Tasks 1-5.
- Produces: a `test` job that gates pull requests.

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: ci

on:
  push:
    branches:
      - main
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Verify formatting
        run: |
          unformatted=$(gofmt -l .)
          if [ -n "$unformatted" ]; then
            echo "These files are not gofmt-ed:"
            echo "$unformatted"
            exit 1
          fi

      - name: Vet
        run: go vet ./...

      - name: Test
        run: go test -race ./...
```

- [ ] **Step 2: Verify the same commands pass locally**

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

Expected: `gofmt -l .` prints nothing, the other two succeed.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run gofmt, vet and tests"
```

---

### Task 7: Release pipeline

**Files:**
- Create: `.goreleaser.yaml`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `cmd/tfgcpvalidator` from Task 5.
- Produces: release archives named `tfgcpvalidator_<Os>_<Arch>.tar.gz`, which Task 8 downloads.

- [ ] **Step 1: Write `.goreleaser.yaml`**

```yaml
version: 2

builds:
  - id: tfgcpvalidator
    main: ./cmd/tfgcpvalidator
    binary: tfgcpvalidator
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w

archives:
  # The composite action builds this exact filename, so changing the template
  # breaks every pinned action version.
  - name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]

checksum:
  name_template: checksums.txt

changelog:
  use: github
```

- [ ] **Step 2: Write `.github/workflows/release.yml`**

```yaml
name: release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - uses: goreleaser/goreleaser-action@v6
        with:
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 3: Verify the config parses**

```bash
go run github.com/goreleaser/goreleaser/v2@latest check
```

Expected: `1 configuration file(s) validated`. If the network is unavailable, skip this step and note it in the commit message.

- [ ] **Step 4: Commit**

```bash
git add .goreleaser.yaml .github/workflows/release.yml
git commit -m "ci: add goreleaser release pipeline"
```

---

### Task 8: The composite action

**Files:**
- Create: `action.yml`

**Interfaces:**
- Consumes: release archives from Task 7 named `tfgcpvalidator_<Os>_<Arch>.tar.gz`; the CLI flags from Task 5.
- Produces: an action with inputs `plan`, `check`, `format`, `fail-on`, `version` and outputs `findings`, `error-count`, `warn-count`.

The action runs the CLI twice on purpose: once with `--format json --fail-on never` to build the outputs without affecting the job result, then once with the caller's format and threshold so the real exit code and annotations come from a single source of truth. Parsing a plan twice costs milliseconds.

- [ ] **Step 1: Write `action.yml`**

```yaml
name: tfgcpvalidator
description: Catch Terraform failures on Google Cloud at plan time
branding:
  icon: shield
  color: blue

inputs:
  plan:
    description: Path to the output of `terraform show -json`
    required: true
  check:
    description: Name of a single check to run. Leave empty to run every check.
    required: false
    default: ''
  format:
    description: 'Output format: text, markdown, github or json'
    required: false
    default: github
  fail-on:
    description: 'Exit non-zero when a finding reaches this severity: error, warn or never'
    required: false
    default: error
  version:
    description: Release tag to install, or `latest`
    required: false
    default: latest

outputs:
  findings:
    description: All findings as JSON
    value: ${{ steps.collect.outputs.findings }}
  error-count:
    description: Number of findings with severity error
    value: ${{ steps.collect.outputs.error-count }}
  warn-count:
    description: Number of findings with severity warn
    value: ${{ steps.collect.outputs.warn-count }}

runs:
  using: composite
  steps:
    - name: Install tfgcpvalidator
      shell: bash
      env:
        VERSION: ${{ inputs.version }}
      run: |
        set -euo pipefail

        os=$(uname -s)
        arch=$(uname -m)
        case "$arch" in
          x86_64) arch=amd64 ;;
          aarch64) arch=arm64 ;;
        esac
        case "$os" in
          Linux) os=linux ;;
          Darwin) os=darwin ;;
          *) echo "unsupported operating system: $os" >&2; exit 1 ;;
        esac

        base=https://github.com/nakamasato/tfgcpvalidator/releases
        if [ "$VERSION" = "latest" ]; then
          url="$base/latest/download/tfgcpvalidator_${os}_${arch}.tar.gz"
        else
          url="$base/download/${VERSION}/tfgcpvalidator_${os}_${arch}.tar.gz"
        fi

        dir="$RUNNER_TEMP/tfgcpvalidator"
        mkdir -p "$dir"
        curl -sSfL "$url" | tar -xz -C "$dir" tfgcpvalidator
        echo "$dir" >> "$GITHUB_PATH"

    - name: Collect findings
      id: collect
      shell: bash
      env:
        PLAN: ${{ inputs.plan }}
        CHECK: ${{ inputs.check }}
      run: |
        set -euo pipefail
        # --fail-on never keeps this step green so the outputs are always set,
        # even when the report step is about to fail the job.
        tfgcpvalidator validate ${CHECK:+"$CHECK"} \
          --plan "$PLAN" --format json --fail-on never > findings.json

        {
          echo "findings<<TFGCPVALIDATOR_EOF"
          cat findings.json
          echo "TFGCPVALIDATOR_EOF"
          echo "error-count=$(jq '.error_count' findings.json)"
          echo "warn-count=$(jq '.warn_count' findings.json)"
        } >> "$GITHUB_OUTPUT"

    - name: Report
      shell: bash
      env:
        PLAN: ${{ inputs.plan }}
        CHECK: ${{ inputs.check }}
        FORMAT: ${{ inputs.format }}
        FAIL_ON: ${{ inputs.fail-on }}
      run: |
        set -euo pipefail
        tfgcpvalidator validate ${CHECK:+"$CHECK"} \
          --plan "$PLAN" --format "$FORMAT" --fail-on "$FAIL_ON"
```

- [ ] **Step 2: Check the shell logic locally**

```bash
CHECK="" ; echo "no check -> [validate ${CHECK:+"$CHECK"}]"
CHECK="destroy" ; echo "with check -> [validate ${CHECK:+"$CHECK"}]"
```

Expected: the first prints `[validate ]` (no positional argument) and the second prints `[validate destroy]`.

- [ ] **Step 3: Commit**

```bash
git add action.yml
git commit -m "feat: add composite github action"
```

---

### Task 9: Usage documentation

**Files:**
- Modify: `README.md` (append after the existing "What it does" section)

**Interfaces:**
- Consumes: the CLI flags from Task 5 and the action inputs from Task 8.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Remove the pre-release warning**

Delete this line near the top of `README.md`:

```markdown
> Status: early design. Not yet usable.
```

- [ ] **Step 2: Insert a Usage section immediately after the "What it does" section**

````markdown
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
````

- [ ] **Step 3: Verify every documented command works**

```bash
go build -o /tmp/tfgcpvalidator ./cmd/tfgcpvalidator
/tmp/tfgcpvalidator validate --help
/tmp/tfgcpvalidator validate destroy --help
```

Expected: both print help without an error, and the flags match the table above.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document CLI and action usage"
```

---

## After the plan

The GitHub repository `nakamasato/tfgcpvalidator` does not exist yet. Per the
user's instruction it is created only once the CLI runs, which is after Task 5.
Creating it and pushing is an outward-facing action: ask before doing it.
