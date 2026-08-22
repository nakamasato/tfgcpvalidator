package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

var sample = []check.Finding{
	{
		Check:       "destroy",
		Severity:    check.Error,
		Address:     "google_sql_database_instance.main",
		Type:        "google_sql_database_instance",
		Message:     "deletion_protection is set and this resource is being destroyed. The apply will fail.",
		Remediation: "Apply deletion_protection = false on its own first.",
	},
}

func render(t *testing.T, format string, findings []check.Finding) string {
	t.Helper()
	r, err := For(format)
	if err != nil {
		t.Fatalf("For(%q) error = %v", format, err)
	}
	var buf bytes.Buffer
	if err := r.Report(&buf, findings); err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	return buf.String()
}

func TestForUnknownFormat(t *testing.T) {
	if _, err := For("yaml"); err == nil {
		t.Fatal("For() error = nil, want an error")
	}
}

func TestFormats(t *testing.T) {
	for _, f := range Formats() {
		if _, err := For(f); err != nil {
			t.Errorf("For(%q) error = %v, but it is advertised by Formats()", f, err)
		}
	}
}

func TestTextIncludesEverything(t *testing.T) {
	got := render(t, "text", sample)
	for _, want := range []string{"ERROR", "google_sql_database_instance.main", "deletion_protection is set", "Apply deletion_protection = false"} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "1 error") {
		t.Errorf("text output missing a summary line:\n%s", got)
	}
}

func TestTextWithNoFindings(t *testing.T) {
	got := render(t, "text", nil)
	if !strings.Contains(got, "No issues found") {
		t.Errorf("text output = %q, want a clear all-clear message", got)
	}
}

func TestMarkdownIsATable(t *testing.T) {
	got := render(t, "markdown", sample)
	if !strings.Contains(got, "|") || !strings.Contains(got, "---") {
		t.Errorf("markdown output is not a table:\n%s", got)
	}
	if !strings.Contains(got, "google_sql_database_instance.main") {
		t.Errorf("markdown output missing the address:\n%s", got)
	}
}

func TestMarkdownWithNoFindings(t *testing.T) {
	got := render(t, "markdown", nil)
	if !strings.Contains(got, "No issues found") {
		t.Errorf("markdown output = %q, want a clear all-clear message", got)
	}
}

func TestGitHubEmitsWorkflowCommands(t *testing.T) {
	got := render(t, "github", sample)
	if !strings.HasPrefix(got, "::error ") {
		t.Errorf("github output does not start with a workflow command:\n%s", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("github output must be one line per finding:\n%s", got)
	}
	if !strings.Contains(got, "google_sql_database_instance.main") {
		t.Errorf("github output missing the address:\n%s", got)
	}
}

func TestGitHubEscapesNewlines(t *testing.T) {
	findings := []check.Finding{{
		Severity: check.Error,
		Check:    "destroy",
		Address:  "google_x.a",
		Message:  "line one\nline two",
	}}
	got := render(t, "github", findings)
	if strings.Count(got, "\n") != 1 {
		t.Errorf("newlines inside a message must be escaped, got:\n%q", got)
	}
	if !strings.Contains(got, "%0A") {
		t.Errorf("github output should escape newlines as %%0A, got:\n%q", got)
	}
}

func TestGitHubEscapesInjectedAddress(t *testing.T) {
	findings := []check.Finding{{
		Severity: check.Error,
		Check:    "destroy",
		Address:  "google_storage_bucket.b[\"a\nb::error::injected\"]",
		Message:  "deletion_protection is set and this resource is being destroyed. The apply will fail.",
	}}
	got := render(t, "github", findings)
	if strings.Count(got, "\n") != 1 {
		t.Errorf("a newline inside the address must not produce a second line, got:\n%q", got)
	}
	if !strings.Contains(got, "%0A") {
		t.Errorf("github output should escape the address's newline as %%0A, got:\n%q", got)
	}
}

func TestMarkdownEscapesAddress(t *testing.T) {
	findings := []check.Finding{{
		Severity:    check.Error,
		Check:       "destroy",
		Address:     "google_storage_bucket.b[\"a|b`c\"]",
		Message:     "deletion_protection is set",
		Remediation: "fix it",
	}}
	got := render(t, "markdown", findings)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	var row string
	for _, l := range lines {
		if strings.Contains(l, "a") && strings.Contains(l, "deletion_protection is set") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("could not find the finding's table row in:\n%s", got)
	}
	unescaped := strings.ReplaceAll(row, `\|`, "")
	if strings.Count(unescaped, "|") != 5 {
		t.Errorf("the pipe in the address must not add a table column (want 5 unescaped '|' for 4 columns), got row:\n%q", row)
	}
	if !strings.Contains(row, "deletion_protection is set") || !strings.Contains(row, "fix it") {
		t.Errorf("the backtick in the address must not leak the rest of the row as markup, got row:\n%q", row)
	}
}

func TestJSONIsParseable(t *testing.T) {
	got := render(t, "json", sample)
	var decoded struct {
		Findings []struct {
			Severity string `json:"severity"`
			Address  string `json:"address"`
		} `json:"findings"`
		ErrorCount int `json:"error_count"`
		WarnCount  int `json:"warn_count"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("json output is not valid JSON: %v\n%s", err, got)
	}
	if len(decoded.Findings) != 1 || decoded.Findings[0].Severity != "error" {
		t.Errorf("unexpected findings: %+v", decoded.Findings)
	}
	if decoded.ErrorCount != 1 || decoded.WarnCount != 0 {
		t.Errorf("counts = (%d, %d), want (1, 0)", decoded.ErrorCount, decoded.WarnCount)
	}
}

func TestJSONWithNoFindingsIsAnEmptyArray(t *testing.T) {
	got := render(t, "json", nil)
	if !strings.Contains(got, `"findings": []`) {
		t.Errorf("json output should carry an empty array rather than null:\n%s", got)
	}
}
