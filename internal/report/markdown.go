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

	// One line per finding, with the remediation folded away: the remediations
	// are long and largely identical, and they were burying the addresses.
	for _, f := range findings {
		fmt.Fprintf(&b, "%s %s — %s\n", icon(f.Severity), codeSpan(oneLine(f.Address)), oneLine(f.Message))
		if f.Remediation != "" {
			// The blank line after <summary> is what lets GitHub render the
			// body as markdown rather than as literal text.
			fmt.Fprintf(&b, "<details><summary>Fix</summary>\n\n%s\n</details>\n", oneLine(f.Remediation))
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
