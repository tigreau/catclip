package catclip

import (
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestSummarizeDiagnostics(t *testing.T) {
	summary := summarizeDiagnostics([]discovery.Diagnostic{
		{Message: "warning"},
		{Message: "error", IsError: true},
		{Message: "missing", IsTargetNotFound: true},
		{Message: "unsatisfiable", IsScopeUnsatisfiable: true},
	}, true)

	if !summary.HasError {
		t.Fatal("expected HasError")
	}
	if !summary.HasTargetNotFound {
		t.Fatal("expected HasTargetNotFound")
	}
	if !summary.HasScopeUnsatisfiable {
		t.Fatal("expected HasScopeUnsatisfiable")
	}
	if !summary.HadSelectionCancel {
		t.Fatal("expected HadSelectionCancel")
	}
}
