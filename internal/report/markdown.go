package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type markdownReporter struct{}

func (markdownReporter) Report(w io.Writer, findings []check.Finding) error {
	if len(findings) == 0 {
		_, err := fmt.Fprintln(w, "No issues found.")
		return err
	}

	var b strings.Builder
	b.WriteString("| Severity | Resource | Problem | Fix |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n",
			f.Severity, f.Address, escapePipes(f.Message), escapePipes(f.Remediation))
	}

	errCount, warnCount := check.Counts(findings)
	fmt.Fprintf(&b, "\n%s, %s\n", pluralize(errCount, "error"), pluralize(warnCount, "warning"))

	_, err := io.WriteString(w, b.String())
	return err
}

// A pipe inside a cell would end the column early.
func escapePipes(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", `\|`)
}
