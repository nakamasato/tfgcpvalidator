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
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			f.Severity, codeSpan(escapePipes(f.Address)), escapePipes(f.Message), escapePipes(f.Remediation))
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

// CommonMark code spans have no backslash-escape for a backtick, so a backtick
// in the content can only be neutralized by delimiting with a longer run of
// backticks than any run inside the content, padded with a space on each side
// so the content's own leading/trailing backtick doesn't merge with the delimiter.
func codeSpan(s string) string {
	longest, current := 0, 0
	for _, r := range s {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	delim := strings.Repeat("`", longest+1)
	return delim + " " + s + " " + delim
}
