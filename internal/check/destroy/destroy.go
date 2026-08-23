// Package destroy reports resources that a plan deletes while a Google Cloud
// deletion-protection flag is still set on them.
package destroy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// Every entry is a field NAME, never a resource type. Google Cloud adds
// deletion protection to new resource types continuously — between provider
// 6.44.0 and 7.41.0 the number of protection-bearing fields went from 48 to 868
// — so matching on names is what keeps this working for types that do not exist
// yet. The names themselves come from auditing the provider schema.
//
// google_sql_database_instance carries two independent protections: the
// Terraform-level deletion_protection and the API-level
// settings.deletion_protection_enabled. Clearing one leaves the other blocking.
//
// Nested paths are declared explicitly rather than searched for, which is what
// keeps google_backup_dr_restore_workload's
// compute_instance_restore_properties.deletion_protection out: it describes the
// instance that workload will recreate, not a guard on deleting the workload.
var rules = []rule{
	{path: "deletion_protection", matches: isTrue, fix: "deletion_protection = false"},
	// Bigtable spells the same field as an enum rather than a boolean.
	{path: "deletion_protection", matches: equals("PROTECTED"), fix: `deletion_protection = "UNPROTECTED"`},
	{path: "deletion_protection_enabled", matches: isTrue, fix: "deletion_protection_enabled = false"},
	{path: "settings.deletion_protection_enabled", matches: isTrue, fix: "settings.deletion_protection_enabled = false"},
	{path: "deletion_policy", matches: equals("PREVENT"), fix: `deletion_policy = "DELETE"`},
	{path: "delete_protection_state", matches: equals("DELETE_PROTECTION_ENABLED"), fix: `delete_protection_state = "DELETE_PROTECTION_DISABLED"`},
	{path: "delete_protection", matches: isTrue, fix: "delete_protection = false"},
	{path: "enable_deletion_protection", matches: isTrue, fix: "enable_deletion_protection = false"},
}

func isTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func equals(want string) func(any) bool {
	return func(v any) bool {
		s, ok := v.(string)
		return ok && s == want
	}
}

func (*Check) Run(_ context.Context, in check.Input) ([]check.Finding, error) {
	if in.Plan == nil {
		return nil, errors.New("no plan supplied")
	}

	var findings []check.Finding
	for _, rc := range in.Plan.ResourceChanges {
		if !rc.IsManaged() || !isGoogleCloud(rc) || !rc.Change.HasAction("delete") {
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
				Fix:         r.fix,
				Remediation: remediation,
			})
		}
	}
	return findings, nil
}

// The field names this check matches are not unique to Google Cloud, so without
// this the check would report AWS and Azure resources the tool does not claim to
// cover.
func isGoogleCloud(rc plan.ResourceChange) bool {
	return strings.HasPrefix(rc.Type, "google_")
}

func message(rc plan.ResourceChange, r rule) string {
	if rc.Change.HasAction("create") {
		return fmt.Sprintf("%s is set and this resource is being replaced. A replace deletes before it creates, so the apply will fail.", r.path)
	}
	return fmt.Sprintf("%s is set and this resource is being destroyed. The apply will fail.", r.path)
}

// Terraform deletes with the value already in state, so clearing the protection
// and removing the resource cannot land in a single apply.
const remediation = "apply it before this change"

// RulePaths returns the field paths this check matches, so the schema audit can
// tell which of the provider's protection fields are already covered.
func RulePaths() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if seen[r.path] {
			continue
		}
		seen[r.path] = true
		out = append(out, r.path)
	}
	sort.Strings(out)
	return out
}

// ExcludedPaths are protection-shaped fields deliberately left out, mapped to
// why. The audit reports an unlisted field as uncovered, so a decision to skip
// one has to be recorded here rather than forgotten.
func ExcludedPaths() map[string]string {
	return map[string]string{
		"compute_instance_restore_properties.deletion_protection": "describes the instance a restore workload will recreate, not a guard on deleting the workload",
		"deletion_protection_reason":                              "a human-readable reason string, not a flag that blocks deletion",
	}
}
