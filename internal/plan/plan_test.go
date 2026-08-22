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
