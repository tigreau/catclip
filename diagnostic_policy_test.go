package catclip

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/ui"
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

func TestDiagnosticSummaryEffectsAreIndependent(t *testing.T) {
	tests := []struct {
		name                    string
		diagnostic              discovery.Diagnostic
		wantError               bool
		wantTargetNotFound      bool
		wantScopeUnsatisfiable  bool
		wantExplainedScopeCount int
	}{
		{name: "plain warning", diagnostic: discovery.Diagnostic{Message: "warning"}},
		{name: "hard target error", diagnostic: discovery.Diagnostic{Message: "error", IsError: true}, wantError: true},
		{name: "missing target", diagnostic: discovery.Diagnostic{Message: "missing", IsTargetNotFound: true}, wantTargetNotFound: true},
		{name: "unsatisfiable scope", diagnostic: discovery.Diagnostic{Message: "blocked", IsScopeUnsatisfiable: true}, wantScopeUnsatisfiable: true},
		{name: "explained scope", diagnostic: discovery.Diagnostic{Message: "explained", ExplainsEmptyResult: true, ScopeIndex: 1}, wantExplainedScopeCount: 1},
		{
			name: "promoted hard error",
			diagnostic: discovery.Diagnostic{
				Message:              "hard",
				IsError:              true,
				IsScopeUnsatisfiable: true,
				ExplainsEmptyResult:  true,
				ScopeIndex:           0,
			},
			wantError:               true,
			wantScopeUnsatisfiable:  true,
			wantExplainedScopeCount: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := summarizeDiagnostics([]discovery.Diagnostic{tt.diagnostic}, false)
			if summary.HasError != tt.wantError {
				t.Fatalf("HasError = %v, want %v", summary.HasError, tt.wantError)
			}
			if summary.HasTargetNotFound != tt.wantTargetNotFound {
				t.Fatalf("HasTargetNotFound = %v, want %v", summary.HasTargetNotFound, tt.wantTargetNotFound)
			}
			if summary.HasScopeUnsatisfiable != tt.wantScopeUnsatisfiable {
				t.Fatalf("HasScopeUnsatisfiable = %v, want %v", summary.HasScopeUnsatisfiable, tt.wantScopeUnsatisfiable)
			}
			if got := len(summary.ExplainedEmptyScopes); got != tt.wantExplainedScopeCount {
				t.Fatalf("explained scope count = %d, want %d", got, tt.wantExplainedScopeCount)
			}
		})
	}
}

func TestInvocationWarningDoesNotExplainScopeZero(t *testing.T) {
	summary := summarizeDiagnostics([]discovery.Diagnostic{{
		Message:             "invocation warning",
		ExplainsEmptyResult: true,
		ScopeIndex:          -1,
	}}, false)

	if summary.AllEmptyScopesExplained(1) {
		t.Fatal("an invocation-wide warning must not explain scope zero")
	}
	if len(summary.ExplainedEmptyScopes) != 0 {
		t.Fatalf("invocation-wide warning populated explained scopes: %#v", summary.ExplainedEmptyScopes)
	}
}

func TestWriteDiscoveryDiagnosticsDedupesExactMessages(t *testing.T) {
	diagnostics := []discovery.Diagnostic{
		{Message: "first"},
		{Message: "second"},
		{Message: "first", IsScopeUnsatisfiable: true},
	}

	var stderr bytes.Buffer
	if err := writeDiscoveryDiagnostics(diagnostics, &stderr); err != nil {
		t.Fatalf("writeDiscoveryDiagnostics: %v", err)
	}
	if got, want := stderr.String(), "first\nsecond\n"; got != want {
		t.Fatalf("diagnostic output mismatch\nwant: %q\ngot:  %q", want, got)
	}
}

type diagnosticFailWriter struct {
	err error
}

func (w diagnosticFailWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWriteDiscoveryDiagnosticsPropagatesWriterFailure(t *testing.T) {
	want := errors.New("stderr unavailable")
	err := writeDiscoveryDiagnostics([]discovery.Diagnostic{{Message: "warning"}}, diagnosticFailWriter{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("write error = %v, want %v", err, want)
	}
}

func TestExecutePlanOutputStopsBeforePayloadWhenDiagnosticWriteFails(t *testing.T) {
	want := errors.New("stderr unavailable")
	var stdout bytes.Buffer
	err := executePlanOutput(outputExecutionContext{
		Stdout: &stdout,
		Stderr: diagnosticFailWriter{err: want},
	}, outputExecutionState{
		Diagnostics: []discovery.Diagnostic{{Message: "warning"}},
	})
	if !errors.Is(err, want) {
		t.Fatalf("executePlanOutput error = %v, want %v", err, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("diagnostic write failure emitted payload: %q", stdout.String())
	}
}

func TestNeverEmitWritesSelectionReportButStopsBeforePayload(t *testing.T) {
	project := t.TempDir()
	absPath := filepath.Join(project, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "PAYLOAD_MUST_NOT_BE_EMITTED\n"
	if err := os.WriteFile(absPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := discovery.Entry{
		AbsPath:    absPath,
		RelPath:    "src/main.go",
		TargetRoot: project,
		Mode:       command.EntryModeFull,
	}
	plan := output.BuildPlan([]output.PreparedFileUnit{{Entry: entry, BodyBytes: int64(len(body))}})
	report := output.Report{
		Sizes:     map[string]int64{"src/main.go": int64(len(body))},
		Statuses:  map[string]string{},
		ModeTags:  map[string]string{},
		HumanSize: "28.00B",
		Tokens:    7,
		CountWord: "file",
	}
	var stdout, stderr bytes.Buffer
	err := executeNormalOutput(outputExecutionContext{
		Invocation: command.Invocation{EmissionPolicy: command.EmissionNever},
		Render:     ui.RenderConfig{},
		Emit:       output.EmitConfig{OutputMode: command.OutputModeStdout},
		Stdout:     &stdout,
		Stderr:     &stderr,
	}, outputExecutionState{Plan: plan}, report)
	if err != nil {
		t.Fatalf("executeNormalOutput() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "src/") || !strings.Contains(got, "main.go") || !strings.Contains(got, "Count:") {
		t.Fatalf("stdout is missing selection report:\n%s", got)
	} else if strings.Contains(got, "PAYLOAD_MUST_NOT_BE_EMITTED") {
		t.Fatalf("stdout contains final payload:\n%s", got)
	} else if strings.Contains(got, "Proceed?") || strings.Contains(got, "Aborted.") {
		t.Fatalf("--no must not prompt or describe the run as aborted:\n%s", got)
	}
	if got := stderr.String(); !strings.Contains(got, "Not a git repo") {
		t.Fatalf("diagnostics should retain stderr for --no, stderr=%q", got)
	} else if strings.Contains(got, "src/") || strings.Contains(got, "Count:") {
		t.Fatalf("tree/summary leaked to stderr for --no, stderr=%q", got)
	}
}

func TestInternalTreePreviewDoesNotEmitAccumulatedDiagnostics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := executeTreePreview(outputExecutionContext{
		Stdout: &stdout,
		Stderr: &stderr,
	}, outputExecutionState{
		Diagnostics: []discovery.Diagnostic{{Message: "parent-only warning"}},
		Summary:     DiagnosticSummary{HasError: true},
	})
	if err == nil {
		t.Fatal("expected internal error summary to stop tree preview")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("internal tree preview leaked accumulated diagnostics: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
