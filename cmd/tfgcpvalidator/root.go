package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
	"github.com/nakamasato/tfgcpvalidator/internal/check/destroy"
)

// exitCodeError carries a specific process exit code out through cobra so that
// "the check found something" stays distinguishable from "the tool broke".
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func registry() *check.Registry {
	return check.NewRegistry(destroy.New())
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "tfgcpvalidator",
		Short:         "Catch Terraform failures on Google Cloud at plan time",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newValidateCmd())
	return root
}
