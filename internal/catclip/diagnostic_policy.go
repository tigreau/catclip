package catclip

import (
	"fmt"
	"io"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

type DiagnosticSummary struct {
	HasError              bool
	HasTargetNotFound     bool
	HasScopeUnsatisfiable bool
	HadSelectionCancel    bool
	ExplainedEmptyScopes  map[int]struct{}
}

func summarizeDiagnostics(diags []discovery.Diagnostic, hadSelectionCancel bool) DiagnosticSummary {
	summary := DiagnosticSummary{
		HadSelectionCancel:   hadSelectionCancel,
		ExplainedEmptyScopes: make(map[int]struct{}),
	}
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
		if diag.ExplainsEmptyResult && diag.ScopeIndex >= 0 {
			summary.ExplainedEmptyScopes[diag.ScopeIndex] = struct{}{}
		}
	}
	return summary
}

func (s DiagnosticSummary) AllEmptyScopesExplained(scopeCount int) bool {
	if scopeCount <= 0 || len(s.ExplainedEmptyScopes) != scopeCount {
		return false
	}
	for scopeIndex := 0; scopeIndex < scopeCount; scopeIndex++ {
		if _, ok := s.ExplainedEmptyScopes[scopeIndex]; !ok {
			return false
		}
	}
	return true
}

func writeDiscoveryDiagnostics(diags []discovery.Diagnostic, stderr io.Writer, palettes ...platform.Palette) error {
	var colors platform.Palette
	if len(palettes) > 0 {
		colors = palettes[0]
	}
	seen := make(map[string]struct{}, len(diags))
	for _, diag := range diags {
		if _, duplicate := seen[diag.Message]; duplicate {
			continue
		}
		seen[diag.Message] = struct{}{}
		message := styleDiagnosticMessage(diag.Message, colors, diag.IsError || diag.IsScopeUnsatisfiable)
		if _, err := fmt.Fprintln(stderr, message); err != nil {
			return err
		}
	}
	return nil
}
