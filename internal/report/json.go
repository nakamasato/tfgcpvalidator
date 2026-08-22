package report

import (
	"encoding/json"
	"io"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type jsonReporter struct{}

type jsonPayload struct {
	Findings   []check.Finding `json:"findings"`
	ErrorCount int             `json:"error_count"`
	WarnCount  int             `json:"warn_count"`
}

func (jsonReporter) Report(w io.Writer, findings []check.Finding) error {
	// Consumers index into findings, so an absent list has to be [] and not null.
	if findings == nil {
		findings = []check.Finding{}
	}
	errCount, warnCount := check.Counts(findings)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonPayload{Findings: findings, ErrorCount: errCount, WarnCount: warnCount})
}
