package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/schema"
)

// resourceSpec describes one resource_change to emit. Values are keyed by the
// dotted field path so every one of them can be resolved against the schema
// before it reaches the file.
type resourceSpec struct {
	address string
	rtype   string
	mode    string
	actions []string
	values  map[string]any
}

// The fixture exists to prove this tool's idea of a plan matches the provider's,
// so it covers one case per shape the destroy check depends on: a boolean flag,
// an enum flag, a flag nested in a MaxItems=1 block, a replace, a resource with
// no protection at all, a create, and a data source.
var fixtureSpecs = []resourceSpec{
	{
		address: "google_sql_database_instance.main",
		rtype:   "google_sql_database_instance",
		actions: []string{"delete"},
		values: map[string]any{
			"deletion_protection":                  true,
			"settings.deletion_protection_enabled": true,
		},
	},
	{
		address: "google_bigtable_table.events",
		rtype:   "google_bigtable_table",
		actions: []string{"delete"},
		values:  map[string]any{"deletion_protection": "PROTECTED"},
	},
	{
		address: "google_bigquery_table.events",
		rtype:   "google_bigquery_table",
		actions: []string{"delete"},
		values:  map[string]any{"deletion_protection": true},
	},
	{
		address: "module.gke.google_container_cluster.primary",
		rtype:   "google_container_cluster",
		actions: []string{"delete", "create"},
		values:  map[string]any{"deletion_protection": true},
	},
	{
		address: "google_project.sandbox",
		rtype:   "google_project",
		actions: []string{"delete"},
		values:  map[string]any{"deletion_policy": "PREVENT"},
	},
	{
		address: "google_firestore_database.db",
		rtype:   "google_firestore_database",
		actions: []string{"delete"},
		values:  map[string]any{"delete_protection_state": "DELETE_PROTECTION_ENABLED"},
	},
	{
		address: "google_storage_bucket.assets",
		rtype:   "google_storage_bucket",
		actions: []string{"delete"},
		values:  map[string]any{},
	},
	{
		address: "google_compute_instance.worker",
		rtype:   "google_compute_instance",
		actions: []string{"create"},
		values:  map[string]any{"deletion_protection": false},
	},
	{
		address: "data.google_project.current",
		rtype:   "google_project",
		mode:    "data",
		actions: []string{"read"},
		values:  map[string]any{"deletion_policy": "PREVENT"},
	},
}

func writeFixture(s *schema.Schema, provider, version, out string) error {
	changes := make([]map[string]any, 0, len(fixtureSpecs))
	for _, spec := range fixtureSpecs {
		before, err := buildBefore(s, spec)
		if err != nil {
			return err
		}
		mode := spec.mode
		if mode == "" {
			mode = "managed"
		}
		var after any
		if !(contains(spec.actions, "delete") && !contains(spec.actions, "create")) {
			after = map[string]any{}
		}
		changes = append(changes, map[string]any{
			"address":       spec.address,
			"mode":          mode,
			"type":          spec.rtype,
			"name":          spec.address[strings.LastIndex(spec.address, ".")+1:],
			"provider_name": provider,
			"change": map[string]any{
				"actions": spec.actions,
				"before":  before,
				"after":   after,
			},
		})
	}

	doc := map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.14.3",
		"resource_changes":  changes,
		"configuration": map[string]any{
			"provider_config": map[string]any{
				"google": map[string]any{
					"name":               "google",
					"full_name":          provider,
					"version_constraint": version,
				},
			},
		},
	}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture: %w", err)
	}
	return os.WriteFile(out, append(body, '\n'), 0o644)
}

// buildBefore renders each value at its path, turning a nested segment into a
// one-element list because that is how Terraform writes a MaxItems=1 block.
func buildBefore(s *schema.Schema, spec resourceSpec) (map[string]any, error) {
	before := map[string]any{}
	for _, path := range sortedKeys(spec.values) {
		value := spec.values[path]
		if err := s.Accepts(spec.rtype, path, value); err != nil {
			return nil, fmt.Errorf("fixture for %s: %w", spec.address, err)
		}
		parts := strings.Split(path, ".")
		cur := before
		for _, p := range parts[:len(parts)-1] {
			list, ok := cur[p].([]any)
			if !ok {
				list = []any{map[string]any{}}
				cur[p] = list
			}
			cur = list[0].(map[string]any)
		}
		cur[parts[len(parts)-1]] = value
	}
	return before, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
