package check

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSeverityString(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{Error, "error"},
		{Warn, "warn"},
		{Info, "info"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestSeverityMarshalJSON(t *testing.T) {
	b, err := json.Marshal(Finding{Severity: Error})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["severity"] != "error" {
		t.Errorf("severity = %v, want %q", decoded["severity"], "error")
	}
}

func TestParseFailOn(t *testing.T) {
	tests := []struct {
		in      string
		want    FailOn
		wantErr bool
	}{
		{"error", FailOnError, false},
		{"warn", FailOnWarn, false},
		{"never", FailNever, false},
		{"nonsense", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseFailOn(tt.in)
		if (err != nil) != tt.wantErr {
			t.Fatalf("ParseFailOn(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseFailOn(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestShouldFail(t *testing.T) {
	errFinding := Finding{Severity: Error}
	warnFinding := Finding{Severity: Warn}
	infoFinding := Finding{Severity: Info}

	tests := []struct {
		name     string
		findings []Finding
		failOn   FailOn
		want     bool
	}{
		{"error threshold with error", []Finding{errFinding}, FailOnError, true},
		{"error threshold with warn only", []Finding{warnFinding}, FailOnError, false},
		{"warn threshold with warn", []Finding{warnFinding}, FailOnWarn, true},
		{"warn threshold with info only", []Finding{infoFinding}, FailOnWarn, false},
		{"never with error", []Finding{errFinding}, FailNever, false},
		{"no findings", nil, FailOnError, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldFail(tt.findings, tt.failOn); got != tt.want {
				t.Errorf("ShouldFail() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCounts(t *testing.T) {
	findings := []Finding{{Severity: Error}, {Severity: Error}, {Severity: Warn}, {Severity: Info}}
	gotErrors, gotWarns := Counts(findings)
	if gotErrors != 2 || gotWarns != 1 {
		t.Errorf("Counts() = (%d, %d), want (2, 1)", gotErrors, gotWarns)
	}
}

type stubCheck struct {
	name     string
	findings []Finding
	err      error
}

func (s stubCheck) Name() string { return s.name }

func (s stubCheck) Run(context.Context, Input) ([]Finding, error) { return s.findings, s.err }

func TestRunAggregatesFindings(t *testing.T) {
	a := stubCheck{name: "a", findings: []Finding{{Check: "a"}}}
	b := stubCheck{name: "b", findings: []Finding{{Check: "b"}, {Check: "b"}}}

	got, err := Run(context.Background(), []Check{a, b}, Input{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(findings) = %d, want 3", len(got))
	}
}

func TestRunWrapsCheckError(t *testing.T) {
	boom := errors.New("boom")
	failing := stubCheck{name: "failing", err: boom}

	_, err := Run(context.Background(), []Check{failing}, Input{})
	if !errors.Is(err, boom) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, boom)
	}
}
