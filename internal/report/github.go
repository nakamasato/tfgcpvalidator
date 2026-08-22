package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type githubReporter struct{}

func (githubReporter) Report(w io.Writer, findings []check.Finding) error {
	for _, f := range findings {
		level := "error"
		if f.Severity < check.Error {
			level = "warning"
		}
		body := f.Message
		if f.Remediation != "" {
			body += " " + f.Remediation
		}
		if _, err := fmt.Fprintf(w, "::%s title=tfgcpvalidator/%s::%s: %s\n",
			level, f.Check, f.Address, escapeCommand(body)); err != nil {
			return err
		}
	}
	return nil
}

// Workflow commands are line-oriented, so the payload has to survive on one line.
func escapeCommand(s string) string {
	r := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	return r.Replace(s)
}
