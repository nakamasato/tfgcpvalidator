// Command schemaaudit keeps the destroy check's field-name table honest against
// a real provider schema, and regenerates the schema-shaped test fixture from
// that same schema. It is a maintenance tool: it never ships in the CLI.
//
//	terraform providers schema -json > schema.json
//	go run ./tools/schemaaudit -schema schema.json -audit
//	go run ./tools/schemaaudit -schema schema.json -provider-version 7.41.0 -fixture
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check/destroy"
	"github.com/nakamasato/tfgcpvalidator/internal/schema"
)

const defaultProvider = "registry.terraform.io/hashicorp/google"

func main() {
	var (
		schemaPath = flag.String("schema", "", "path to `terraform providers schema -json` output (required)")
		provider   = flag.String("provider", defaultProvider, "provider address to audit")
		version    = flag.String("provider-version", "", "provider version to record in a generated fixture")
		out        = flag.String("out", "internal/check/destroy/testdata/schema_shaped_plan.json", "fixture path to write")
		doAudit    = flag.Bool("audit", false, "report protection fields the rule table does not cover")
		doFixture  = flag.Bool("fixture", false, "regenerate the schema-shaped plan fixture")
	)
	flag.Parse()

	switch err := run(*schemaPath, *provider, *version, *out, *doAudit, *doFixture); {
	case err == nil:
	case errors.Is(err, errUncovered):
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func run(schemaPath, provider, version, out string, doAudit, doFixture bool) error {
	if schemaPath == "" {
		return fmt.Errorf("-schema is required")
	}
	if !doAudit && !doFixture {
		return fmt.Errorf("pass -audit, -fixture, or both")
	}

	s, err := schema.Load(schemaPath, provider)
	if err != nil {
		return err
	}

	if doFixture {
		if version == "" {
			return fmt.Errorf("-provider-version is required with -fixture: a fixture that does not name its provider version claims nothing")
		}
		if err := writeFixture(s, provider, version, out); err != nil {
			return err
		}
		fmt.Printf("wrote %s (shaped for %s %s)\n", out, provider, version)
	}

	if doAudit && audit(s, os.Stdout) > 0 {
		return errUncovered
	}
	return nil
}

// errUncovered separates "the audit found something" from "the tool broke", the
// same way the CLI separates exit 1 from exit 2.
var errUncovered = errors.New("uncovered protection fields")

// isProtectionShaped picks the field names that could plausibly block a delete.
// It is deliberately broader than the rule table: its whole purpose is to
// surface names nobody has classified yet.
func isProtectionShaped(leaf string) bool {
	return leaf == "deletion_policy" ||
		strings.Contains(leaf, "deletion_protection") ||
		strings.Contains(leaf, "delete_protection")
}

type finding struct {
	path      string
	primitive string
	types     []string
}

func audit(s *schema.Schema, w io.Writer) int {
	covered := map[string]bool{}
	for _, p := range destroy.RulePaths() {
		covered[p] = true
	}
	excluded := destroy.ExcludedPaths()

	byPath := map[string]*finding{}
	for _, rtype := range s.ResourceTypes() {
		attrs, err := s.Attributes(rtype)
		if err != nil {
			continue
		}
		for path, a := range attrs {
			leaf := path[strings.LastIndex(path, ".")+1:]
			if !isProtectionShaped(leaf) {
				continue
			}
			key := path + "\x00" + a.Primitive
			f, ok := byPath[key]
			if !ok {
				f = &finding{path: path, primitive: a.Primitive}
				byPath[key] = f
			}
			f.types = append(f.types, rtype)
		}
	}

	var cov, exc, unc []*finding
	for _, f := range byPath {
		sort.Strings(f.types)
		switch {
		case covered[f.path]:
			cov = append(cov, f)
		case excluded[f.path] != "":
			exc = append(exc, f)
		default:
			unc = append(unc, f)
		}
	}
	byName := func(s []*finding) { sort.Slice(s, func(i, j int) bool { return s[i].path < s[j].path }) }
	byName(cov)
	byName(exc)
	byName(unc)

	fmt.Fprintf(w, "Scanned %d resource types in %s.\n\n", len(s.ResourceTypes()), s.Provider())

	fmt.Fprintln(w, "Covered by the rule table:")
	for _, f := range cov {
		fmt.Fprintf(w, "  %-40s %-7s %d resource types\n", f.path, f.primitive, len(f.types))
	}

	if len(exc) > 0 {
		fmt.Fprintln(w, "\nExcluded on purpose:")
		for _, f := range exc {
			fmt.Fprintf(w, "  %-40s %-7s %s\n", f.path, f.primitive, excluded[f.path])
		}
	}

	if len(unc) == 0 {
		fmt.Fprintln(w, "\nNo uncovered protection fields.")
		return 0
	}

	fmt.Fprintln(w, "\nUNCOVERED protection fields:")
	for _, f := range unc {
		shown := f.types
		if len(shown) > 4 {
			shown = append(append([]string{}, shown[:4]...), fmt.Sprintf("and %d more", len(f.types)-4))
		}
		fmt.Fprintf(w, "  %-40s %-7s %s\n", f.path, f.primitive, strings.Join(shown, ", "))
	}
	fmt.Fprintf(w, "\n%d uncovered field(s). Add each to the rule table in internal/check/destroy,\n"+
		"or record it in ExcludedPaths with the reason it cannot block a delete.\n", len(unc))
	return len(unc)
}
