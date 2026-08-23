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

func TestMarkdownIsOneLinePerFinding(t *testing.T) {
	findings := append(append([]check.Finding{}, sample...), check.Finding{
		Check:    "destroy",
		Severity: check.Warn,
		Address:  "google_bigtable_table.events",
		Message:  "line one\nline two",
	})
	got := render(t, "markdown", findings)

	var headlines []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "❌") || strings.HasPrefix(l, "⚠️") {
			headlines = append(headlines, l)
		}
	}
	if len(headlines) != 2 {
		t.Fatalf("want one headline per finding, got %d:\n%s", len(headlines), got)
	}
	if !strings.HasPrefix(headlines[0], "❌") {
		t.Errorf("an error must be marked with a cross, got:\n%q", headlines[0])
	}
	if !strings.HasPrefix(headlines[1], "⚠️") {
		t.Errorf("a warning must be marked as one, got:\n%q", headlines[1])
	}
	if !strings.Contains(headlines[0], "google_sql_database_instance.main") {
		t.Errorf("markdown output missing the address:\n%s", got)
	}
	if !strings.Contains(headlines[1], "line one line two") {
		t.Errorf("a newline in a message must not split the finding, got:\n%q", headlines[1])
	}
}

func TestMarkdownFoldsTheRemediation(t *testing.T) {
	got := render(t, "markdown", sample)
	if !strings.Contains(got, "<details><summary>Fix</summary>") {
		t.Errorf("the remediation must be folded away:\n%s", got)
	}
	if !strings.Contains(got, sample[0].Remediation) {
		t.Errorf("markdown output missing the remediation:\n%s", got)
	}
	// Without the blank line GitHub renders the body as literal text.
	if !strings.Contains(got, "<summary>Fix</summary>\n\n") {
		t.Errorf("the folded body needs a blank line before it:\n%s", got)
	}
}

func TestMarkdownLeadsWithTheCounts(t *testing.T) {
	got := render(t, "markdown", sample)
	if !strings.HasPrefix(got, "**1 error**, 0 warnings") {
		t.Errorf("markdown output should open with the counts, got:\n%s", got)
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
	var headline string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "❌") {
			headline = l
			break
		}
	}
	if headline == "" {
		t.Fatalf("could not find the finding's line in:\n%s", got)
	}
	if !strings.Contains(headline, "deletion_protection is set") {
		t.Errorf("the backtick in the address must not swallow the message, got:\n%q", headline)
	}
	// A code span delimited by one backtick would end at the address's own
	// backtick and render the rest as markup.
	if strings.Contains(headline, "`` ") == strings.Contains(headline, "``` ") {
		t.Errorf("the code span must be delimited by a longer backtick run than the address contains, got:\n%q", headline)
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
