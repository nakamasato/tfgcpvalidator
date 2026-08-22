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
