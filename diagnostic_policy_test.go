package catclip

import "testing"

func TestSummarizeDiagnostics(t *testing.T) {
	summary := summarizeDiagnostics([]diagnostic{
		{message: "warning"},
		{message: "error", isError: true},
		{message: "missing", isTargetNotFound: true},
		{message: "unsatisfiable", isScopeUnsatisfiable: true},
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
