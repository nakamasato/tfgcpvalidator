package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/plan"
	"github.com/nakamasato/tfgcpvalidator/internal/report"
)

type validateOpts struct {
	planPath string
	format   string
	failOn   string
}

func (o *validateOpts) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.planPath, "plan", "", "path to the output of `terraform show -json` (required)")
	cmd.Flags().StringVar(&o.format, "format", "text", "output format: text, markdown, github or json")
	cmd.Flags().StringVar(&o.failOn, "fail-on", "error", "exit non-zero when a finding reaches this severity: error, warn or never")
	_ = cmd.MarkFlagRequired("plan")
}

func newValidateCmd() *cobra.Command {
	reg := registry()
	opts := &validateOpts{}

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Run every check against a plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChecks(cmd, opts, reg.All())
		},
	}
	opts.bind(cmd)

	for _, c := range reg.All() {
		cmd.AddCommand(newCheckCmd(c))
	}
	return cmd
}

func newCheckCmd(c check.Check) *cobra.Command {
	opts := &validateOpts{}
	cmd := &cobra.Command{
		Use:   c.Name(),
		Short: fmt.Sprintf("Run only the %s check", c.Name()),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChecks(cmd, opts, []check.Check{c})
		},
	}
	opts.bind(cmd)
	return cmd
}

func runChecks(cmd *cobra.Command, o *validateOpts, checks []check.Check) error {
	failOn, err := check.ParseFailOn(o.failOn)
	if err != nil {
		return err
	}
	reporter, err := report.For(o.format)
	if err != nil {
		return err
	}
	p, err := plan.Load(o.planPath)
	if err != nil {
		return err
	}

	findings, err := check.Run(cmd.Context(), checks, check.Input{Plan: p})
	if err != nil {
		return err
	}
	if err := reporter.Report(cmd.OutOrStdout(), findings); err != nil {
		return err
	}

	if check.ShouldFail(findings, failOn) {
		return exitCodeError{code: 1}
	}
	return nil
}
