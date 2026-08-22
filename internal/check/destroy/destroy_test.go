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
