package catclip

import (
	"fmt"
	"io"
)

type DiagnosticSummary struct {
	HasError              bool
	HasTargetNotFound     bool
	HasScopeUnsatisfiable bool
	HadSelectionCancel    bool
}

func summarizeDiagnostics(diags []diagnostic, hadSelectionCancel bool) DiagnosticSummary {
	summary := DiagnosticSummary{HadSelectionCancel: hadSelectionCancel}
	for _, diag := range diags {
		if diag.isError {
			summary.HasError = true
		}
		if diag.isTargetNotFound {
			summary.HasTargetNotFound = true
		}
		if diag.isScopeUnsatisfiable {
			summary.HasScopeUnsatisfiable = true
		}
	}
	return summary
}

func writeTreePayloadDiagnostics(diags []diagnostic, stderr io.Writer) {
	for _, diag := range diags {
		if diag.isScopeUnsatisfiable || diag.isTargetNotFound || diag.isError {
			fmt.Fprintln(stderr, diag.message)
		}
	}
}

func writeDiscoveryDiagnostics(diags []diagnostic, quiet bool, stderr io.Writer) {
	for _, diag := range diags {
		if diag.isError || diag.isTargetNotFound || diag.isScopeUnsatisfiable || !quiet {
			fmt.Fprintln(stderr, diag.message)
		}
	}
}
