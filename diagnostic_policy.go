package catclip

import (
	"fmt"
	"io"

	"github.com/tigreau/catclip/internal/discovery"
)

type DiagnosticSummary struct {
	HasError              bool
	HasTargetNotFound     bool
	HasScopeUnsatisfiable bool
	HadSelectionCancel    bool
}

func summarizeDiagnostics(diags []discovery.Diagnostic, hadSelectionCancel bool) DiagnosticSummary {
	summary := DiagnosticSummary{HadSelectionCancel: hadSelectionCancel}
	for _, diag := range diags {
		if diag.IsError {
			summary.HasError = true
		}
		if diag.IsTargetNotFound {
			summary.HasTargetNotFound = true
		}
		if diag.IsScopeUnsatisfiable {
			summary.HasScopeUnsatisfiable = true
		}
	}
	return summary
}

func writeDiscoveryDiagnostics(diags []discovery.Diagnostic, quiet bool, stderr io.Writer) {
	for _, diag := range diags {
		if diag.IsError || diag.IsTargetNotFound || diag.IsScopeUnsatisfiable || !quiet {
			fmt.Fprintln(stderr, diag.Message)
		}
	}
}
