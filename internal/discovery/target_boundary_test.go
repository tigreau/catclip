package discovery

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateTargetBoundary(t *testing.T) {
	abs := filepath.Join(string(filepath.Separator), "tmp", "project")
	if runtime.GOOS == "windows" {
		abs = `C:\project`
	}
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{name: "relative", target: "src/components"},
		{name: "absolute", target: abs, wantErr: "Error: Absolute paths not allowed: " + SingleQuoted(abs) + "\n  Use a relative path from your project root instead."},
		{name: "parent traversal", target: "src/../vendor", wantErr: "Error: Cannot traverse above working directory: 'src/../vendor'\n  catclip only operates within the current directory tree.\n  Use a relative path from your project root instead.\n  Example: catclip config/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTargetBoundary(tt.target)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateTargetBoundary returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("ValidateTargetBoundary succeeded")
			}
			if got := err.Error(); got != tt.wantErr {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tt.wantErr)
			}
		})
	}
}

func TestOneTargetWithMultipleDiagnosticsCountsAsOneExplainedTarget(t *testing.T) {
	diagnostics := []Diagnostic{
		{Message: "primary", IsTargetNotFound: true},
		{Message: "supporting detail", ExplainsEmptyResult: true},
	}
	if !diagnosticsExplainTargetRequest(diagnostics) {
		t.Fatal("one conclusively failed target with multiple diagnostics was not recognized as explained")
	}
}
