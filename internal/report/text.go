package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type textReporter struct{}

func (textReporter) Report(w io.Writer, findings []check.Finding) error {
	if len(findings) == 0 {
		_, err := fmt.Fprintln(w, "No issues found.")
		return err
	}

	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "%s  %s\n", strings.ToUpper(f.Severity.String()), f.Address)
		fmt.Fprintf(&b, "  %s\n", f.Message)
		if f.Fix != "" {
			fmt.Fprintf(&b, "  fix: set %s and %s\n", f.Fix, f.Remediation)
		}
		b.WriteString("\n")
	}

	errCount, warnCount := check.Counts(findings)
	fmt.Fprintf(&b, "%s, %s\n", pluralize(errCount, "error"), pluralize(warnCount, "warning"))

	_, err := io.WriteString(w, b.String())
	return err
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
