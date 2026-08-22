package destroy_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/check/destroy"
	"github.com/nakamasato/tfgcpvalidator/internal/plan"
)

// The provider release the fixture was shaped for. Protection fields are not
// stable across releases — between google 6.44.0 and 7.41.0 the number of
// protection-bearing fields went from 48 to 868 — so a fixture is a statement
// about one version and the version has to be pinned to mean anything.
const wantProviderVersion = "7.41.0"

// Every field name, type and nesting level in the fixture was generated against
// hashicorp/google's own `terraform providers schema -json` output, so it fails
// if this tool's idea of a plan diverges from the provider's. The hand-built
// fixtures elsewhere in this package cannot catch that, because they are shaped
// to the parser's expectations by construction.
func TestSchemaShapedPlan(t *testing.T) {
	p, err := plan.Load("testdata/schema_shaped_plan.json")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	findings, err := destroy.New().Run(context.Background(), check.Input{Plan: p})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.Address)
		if f.Severity != check.Error {
			t.Errorf("%s: Severity = %v, want error", f.Address, f.Severity)
		}
	}
	sort.Strings(got)

	want := []string{
		"google_bigquery_table.events",
		"google_project.sandbox",
		// Reported twice: the Terraform-level deletion_protection and the
		// API-level settings.deletion_protection_enabled block it independently.
		"google_sql_database_instance.main",
		"google_sql_database_instance.main",
		// A replace deletes before it creates, so it fails the same way.
		"module.gke.google_container_cluster.primary",
	}

	if len(got) != len(want) {
		t.Fatalf("addresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("addresses[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSchemaShapedPlanReachesNestedSQLProtection(t *testing.T) {
	p, err := plan.Load("testdata/schema_shaped_plan.json")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// settings is a MaxItems=1 block, which Terraform renders as a one-element
	// list. If Lookup ever stopped descending through lists this would be the
	// only test to notice.
	var rc plan.ResourceChange
	for _, c := range p.ResourceChanges {
		if c.Address == "google_sql_database_instance.main" {
			rc = c
		}
	}
	if _, ok := plan.Lookup(rc.Change.Before, "settings.deletion_protection_enabled"); !ok {
		t.Fatal("settings.deletion_protection_enabled not reachable in a schema-shaped plan")
	}
}

func TestSchemaShapedPlanDeclaresItsProviderVersion(t *testing.T) {
	raw, err := os.ReadFile("testdata/schema_shaped_plan.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var doc struct {
		Configuration struct {
			ProviderConfig map[string]struct {
				FullName          string `json:"full_name"`
				VersionConstraint string `json:"version_constraint"`
			} `json:"provider_config"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	google, ok := doc.Configuration.ProviderConfig["google"]
	if !ok {
		t.Fatal("fixture does not record which provider it was shaped for")
	}
	if google.FullName != "registry.terraform.io/hashicorp/google" {
		t.Errorf("full_name = %q", google.FullName)
	}
	if google.VersionConstraint != wantProviderVersion {
		t.Errorf("fixture declares provider %s but the test expects %s; regenerate the fixture and update both together",
			google.VersionConstraint, wantProviderVersion)
	}
}
