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
		{Message: "explained", ExplainsEmptyResult: true, ScopeIndex: 0},
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
	if _, ok := summary.ExplainedEmptyScopes[0]; !ok {
		t.Fatal("expected scope 0 to be recorded as explained")
	}
	if !summary.AllEmptyScopesExplained(1) {
		t.Fatal("expected one explained scope to suppress the generic footer")
	}
}
