// Package report renders findings for humans and for machines.
package report

import (
	"fmt"
	"io"

	"github.com/nakamasato/tfgcpvalidator/internal/check"
)

type Reporter interface {
	Report(w io.Writer, findings []check.Finding) error
}

func Formats() []string { return []string{"text", "markdown", "github", "json"} }

func For(format string) (Reporter, error) {
	switch format {
	case "text":
		return textReporter{}, nil
	case "markdown":
		return markdownReporter{}, nil
	case "github":
		return githubReporter{}, nil
	case "json":
		return jsonReporter{}, nil
	}
	return nil, fmt.Errorf("unknown format %q (want one of text, markdown, github, json)", format)
}
