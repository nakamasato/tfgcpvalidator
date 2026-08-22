package schema

import (
	"os"
	"path/filepath"
	"testing"
)

const miniSchema = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/hashicorp/google": {
      "resource_schemas": {
        "google_sql_database_instance": {
          "block": {
            "attributes": {
              "name": {"type": "string"},
              "deletion_protection": {"type": "bool"}
            },
            "block_types": {
              "settings": {
                "nesting_mode": "list",
                "max_items": 1,
                "block": {
                  "attributes": {
                    "deletion_protection_enabled": {"type": "bool"}
                  }
                }
              }
            }
          }
        },
        "google_bigtable_table": {
          "block": {
            "attributes": {
              "deletion_protection": {"type": "string"}
            }
          }
        },
        "google_iam_thing": {
          "block": {
            "attributes": {
              "role": {"type": "string"},
              "members": {"type": ["set", "string"]}
            }
          }
        }
      }
    }
  }
}`

func load(t *testing.T) *Schema {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(miniSchema), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := Load(path, "registry.terraform.io/hashicorp/google")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return s
}

func TestLoadUnknownProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(miniSchema), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Load(path, "registry.terraform.io/hashicorp/aws"); err == nil {
		t.Fatal("Load() error = nil, want an error naming the missing provider")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("nope.json", "x"); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestResourceTypesIsSorted(t *testing.T) {
	got := load(t).ResourceTypes()
	want := []string{"google_bigtable_table", "google_iam_thing", "google_sql_database_instance"}
	if len(got) != len(want) {
		t.Fatalf("ResourceTypes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ResourceTypes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAttributesFlattensNestedBlocks(t *testing.T) {
	attrs, err := load(t).Attributes("google_sql_database_instance")
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}
	if _, ok := attrs["settings.deletion_protection_enabled"]; !ok {
		t.Errorf("nested attribute missing; got paths %v", keys(attrs))
	}
	if _, ok := attrs["deletion_protection"]; !ok {
		t.Errorf("top-level attribute missing; got paths %v", keys(attrs))
	}
}

func keys(m map[string]Attr) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestResolve(t *testing.T) {
	s := load(t)

	tests := []struct {
		name         string
		resourceType string
		path         string
		wantType     string
		wantErr      bool
	}{
		{"top level bool", "google_sql_database_instance", "deletion_protection", "bool", false},
		{"through a list block", "google_sql_database_instance", "settings.deletion_protection_enabled", "bool", false},
		{"string where another type spells it bool", "google_bigtable_table", "deletion_protection", "string", false},
		{"unknown resource type", "google_nope", "deletion_protection", "", true},
		{"unknown attribute", "google_bigtable_table", "nope", "", true},
		{"unknown block", "google_bigtable_table", "nope.deeper", "", true},
		{"attribute is not a primitive", "google_iam_thing", "members", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := s.Resolve(tt.resourceType, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && a.Primitive != tt.wantType {
				t.Errorf("Primitive = %q, want %q", a.Primitive, tt.wantType)
			}
		})
	}
}

func TestAcceptsChecksTheDeclaredType(t *testing.T) {
	s := load(t)

	tests := []struct {
		name         string
		resourceType string
		path         string
		value        any
		wantErr      bool
	}{
		{"bool accepts bool", "google_sql_database_instance", "deletion_protection", true, false},
		{"bool rejects string", "google_sql_database_instance", "deletion_protection", "true", true},
		{"string accepts string", "google_bigtable_table", "deletion_protection", "PROTECTED", false},
		{"string rejects bool", "google_bigtable_table", "deletion_protection", true, true},
		{"nesting must be respected", "google_sql_database_instance", "deletion_protection_enabled", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Accepts(tt.resourceType, tt.path, tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Accepts() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
