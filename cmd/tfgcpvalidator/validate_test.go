package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const protectedDeletePlan = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "google_sql_database_instance.main",
      "mode": "managed",
      "type": "google_sql_database_instance",
      "name": "main",
      "change": {"actions": ["delete"], "before": {"deletion_protection": true}, "after": null}
    }
  ]
}`

const cleanPlan = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "google_storage_bucket.assets",
      "mode": "managed",
      "type": "google_storage_bucket",
      "name": "assets",
      "change": {"actions": ["create"], "before": null, "after": {"name": "assets"}}
    }
  ]
}`

func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tfplan.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// execute runs the CLI and returns stdout plus the error the command returned.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ec exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
	return 2
}

func TestValidateFailsOnProtectedDelete(t *testing.T) {
	out, err := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan))
	if got := exitCode(t, err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (output: %s)", got, out)
	}
	if !strings.Contains(out, "google_sql_database_instance.main") {
		t.Errorf("output missing the offending resource:\n%s", out)
	}
}

func TestValidateSucceedsOnCleanPlan(t *testing.T) {
	out, err := execute(t, "validate", "--plan", writePlan(t, cleanPlan))
	if got := exitCode(t, err); got != 0 {
		t.Fatalf("exit code = %d, want 0 (output: %s)", got, out)
	}
	if !strings.Contains(out, "No issues found") {
		t.Errorf("output = %q, want an all-clear message", out)
	}
}

func TestDestroySubcommandBehavesTheSame(t *testing.T) {
	out, err := execute(t, "validate", "destroy", "--plan", writePlan(t, protectedDeletePlan))
	if got := exitCode(t, err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (output: %s)", got, out)
	}
	if !strings.Contains(out, "google_sql_database_instance.main") {
		t.Errorf("output missing the offending resource:\n%s", out)
	}
}

func TestFailOnNeverStillReportsButExitsZero(t *testing.T) {
	out, err := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan), "--fail-on", "never")
	if got := exitCode(t, err); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if !strings.Contains(out, "google_sql_database_instance.main") {
		t.Errorf("--fail-on never must still print findings:\n%s", out)
	}
}

func TestMissingPlanFileIsAToolError(t *testing.T) {
	_, err := execute(t, "validate", "--plan", "no-such-file.json")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestUnknownFormatIsAToolError(t *testing.T) {
	_, err := execute(t, "validate", "--plan", writePlan(t, cleanPlan), "--format", "yaml")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestUnknownFailOnIsAToolError(t *testing.T) {
	_, err := execute(t, "validate", "--plan", writePlan(t, cleanPlan), "--fail-on", "sometimes")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestMissingPlanFlagIsAToolError(t *testing.T) {
	_, err := execute(t, "validate")
	if got := exitCode(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestJSONFormat(t *testing.T) {
	out, _ := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan), "--format", "json")
	if !strings.Contains(out, `"error_count": 1`) {
		t.Errorf("json output missing the error count:\n%s", out)
	}
}

func TestGitHubFormat(t *testing.T) {
	out, _ := execute(t, "validate", "--plan", writePlan(t, protectedDeletePlan), "--format", "github")
	if !strings.HasPrefix(out, "::error ") {
		t.Errorf("github output missing the workflow command:\n%s", out)
	}
}
