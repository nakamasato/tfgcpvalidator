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
	errCount, warnCount := check.Counts(findings)
	fmt.Fprintf(&b, "**%s**, %s\n\n", pluralize(errCount, "error"), pluralize(warnCount, "warning"))

	// One line per finding plus one for the fix: a table gave the remediation a
	// column of its own, and the repeated prose in it buried the addresses.
	for _, f := range findings {
		// The two trailing spaces are a markdown hard break, which keeps the fix
		// on its own line without opening a second paragraph.
		fmt.Fprintf(&b, "%s %s — %s  \n", icon(f.Severity), codeSpan(oneLine(f.Address)), oneLine(f.Message))
		if f.Fix != "" {
			fmt.Fprintf(&b, "Fix: set %s and %s.\n", codeSpan(f.Fix), oneLine(f.Remediation))
		}
		b.WriteString("\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func icon(s check.Severity) string {
	if s >= check.Error {
		return "❌"
	}
	return "⚠️"
}

// A newline would split one finding across two lines.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// CommonMark code spans have no backslash-escape for a backtick, so a backtick
// in the content can only be neutralized by delimiting with a longer run of
// backticks than any run inside the content, padded with a space on each side
// so the content's own leading/trailing backtick doesn't merge with the delimiter.
// The padding renders away, but it is noise in the raw text, so it is added
// only for the content that needs it.
func codeSpan(s string) string {
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}

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
