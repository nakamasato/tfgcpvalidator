// Package check defines the contract every validation implements and the
// vocabulary they report in.
package check

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nakamasato/tfgcpvalidator/internal/plan"
)

type Severity int

const (
	Info Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warn:
		return "warn"
	default:
		return "info"
	}
}

func (s Severity) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Address  string   `json:"address"`
	Type     string   `json:"type"`
	Message  string   `json:"message"`
	// Fix is the change to apply, as HCL, so a reporter can render it as code.
	Fix         string `json:"fix,omitempty"`
	Remediation string `json:"remediation"`
}

type Input struct {
	Plan *plan.Plan
}

type Check interface {
	Name() string
	Run(ctx context.Context, in Input) ([]Finding, error)
}

func Run(ctx context.Context, checks []Check, in Input) ([]Finding, error) {
	var out []Finding
	for _, c := range checks {
		findings, err := c.Run(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", c.Name(), err)
		}
		out = append(out, findings...)
	}
	return out, nil
}

type FailOn int

const (
	FailNever FailOn = iota
	FailOnWarn
	FailOnError
)

func ParseFailOn(s string) (FailOn, error) {
	switch s {
	case "never":
		return FailNever, nil
	case "warn":
		return FailOnWarn, nil
	case "error":
		return FailOnError, nil
	}
	return 0, fmt.Errorf("unknown fail-on value %q (want error, warn or never)", s)
}

func ShouldFail(findings []Finding, f FailOn) bool {
	var threshold Severity
	switch f {
	case FailNever:
		return false
	case FailOnWarn:
		threshold = Warn
	case FailOnError:
		threshold = Error
	}
	for _, finding := range findings {
		if finding.Severity >= threshold {
			return true
		}
	}
	return false
}

func Counts(findings []Finding) (errors, warns int) {
	for _, f := range findings {
		switch f.Severity {
		case Error:
			errors++
		case Warn:
			warns++
		}
	}
	return errors, warns
}
