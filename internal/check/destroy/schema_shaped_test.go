package destroy_test

import (
	"context"
	"sort"
	"testing"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/check/destroy"
	"github.com/nakamasato/tfgcpvalidator/internal/plan"
)

// Every field name, type and nesting level in the fixture was generated against
// hashicorp/google 7.41.0's own `terraform providers schema -json` output, so it
// fails if the parser's idea of a plan diverges from Terraform's. The hand-built
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
