package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nakamasato/tfgcpvalidator/internal/schema"
)

func TestIsProtectionShaped(t *testing.T) {
	tests := []struct {
		leaf string
		want bool
	}{
		{"deletion_protection", true},
		{"deletion_protection_enabled", true},
		{"enable_deletion_protection", true},
		{"deletion_protection_reason", true},
		{"delete_protection", true},
		{"delete_protection_state", true},
		{"deletion_policy", true},
		// Noise the provider schema is full of. Matching these would bury the
		// signal the audit exists to surface.
		{"backup_deletion_policy", false},
		{"source_deletion_option", false},
		{"deletion_time", false},
		{"deletion_delay_hours", false},
		{"email_protection", false},
		{"protection_level", false},
		{"name", false},
	}
	for _, tt := range tests {
		if got := isProtectionShaped(tt.leaf); got != tt.want {
			t.Errorf("isProtectionShaped(%q) = %v, want %v", tt.leaf, got, tt.want)
		}
	}
}

func load(t *testing.T, body string) *schema.Schema {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := schema.Load(path, defaultProvider)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return s
}

func schemaWith(attrs string) string {
	return `{"provider_schemas":{"registry.terraform.io/hashicorp/google":{"resource_schemas":{
	  "google_thing":{"block":{"attributes":{` + attrs + `}}}}}}}`
}

func TestAuditPassesWhenEveryFieldIsCovered(t *testing.T) {
	s := load(t, schemaWith(`"deletion_protection":{"type":"bool"},"name":{"type":"string"}`))

	var out strings.Builder
	if got := audit(s, &out); got != 0 {
		t.Fatalf("audit() = %d uncovered, want 0\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "No uncovered protection fields") {
		t.Errorf("output does not say it is clean:\n%s", out.String())
	}
}

func TestAuditFailsOnAnUnknownProtectionField(t *testing.T) {
	s := load(t, schemaWith(`"deletion_protection_mode":{"type":"string"}`))

	var out strings.Builder
	if got := audit(s, &out); got != 1 {
		t.Fatalf("audit() = %d uncovered, want 1\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "deletion_protection_mode") {
		t.Errorf("output does not name the uncovered field:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "UNCOVERED") {
		t.Errorf("output does not flag the field as uncovered:\n%s", out.String())
	}
}

func TestAuditTreatsDocumentedExclusionsAsResolved(t *testing.T) {
	s := load(t, schemaWith(`"deletion_protection_reason":{"type":"string"}`))

	var out strings.Builder
	if got := audit(s, &out); got != 0 {
		t.Fatalf("audit() = %d uncovered, want 0 — an excluded field must not fail the audit\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "Excluded on purpose") {
		t.Errorf("output does not report the exclusion:\n%s", out.String())
	}
}

func TestFixtureRejectsAValueTheSchemaDoesNotAllow(t *testing.T) {
	// google_bigquery_table declares deletion_protection as a bool, so a fixture
	// claiming the Bigtable spelling has to be refused rather than written out.
	s := load(t, `{"provider_schemas":{"registry.terraform.io/hashicorp/google":{"resource_schemas":{
	  "google_bigquery_table":{"block":{"attributes":{"deletion_protection":{"type":"bool"}}}}}}}}`)

	_, err := buildBefore(s, resourceSpec{
		address: "google_bigquery_table.x",
		rtype:   "google_bigquery_table",
		values:  map[string]any{"deletion_protection": "PROTECTED"},
	})
	if err == nil {
		t.Fatal("buildBefore() error = nil, want the schema to reject a string where it declares bool")
	}
	if !strings.Contains(err.Error(), "declared bool") {
		t.Errorf("error = %v, want it to name the declared type", err)
	}
}

func TestFixtureNestsAListBlockTheWayTerraformRendersIt(t *testing.T) {
	s := load(t, `{"provider_schemas":{"registry.terraform.io/hashicorp/google":{"resource_schemas":{
	  "google_sql_database_instance":{"block":{"block_types":{"settings":{"nesting_mode":"list","block":{
	    "attributes":{"deletion_protection_enabled":{"type":"bool"}}}}}}}}}}}`)

	before, err := buildBefore(s, resourceSpec{
		address: "google_sql_database_instance.x",
		rtype:   "google_sql_database_instance",
		values:  map[string]any{"settings.deletion_protection_enabled": true},
	})
	if err != nil {
		t.Fatalf("buildBefore() error = %v", err)
	}

	list, ok := before["settings"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("settings = %#v, want a one-element list", before["settings"])
	}
	inner, ok := list[0].(map[string]any)
	if !ok || inner["deletion_protection_enabled"] != true {
		t.Errorf("settings[0] = %#v", list[0])
	}
}
