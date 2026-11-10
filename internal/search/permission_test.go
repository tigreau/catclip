package search

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPermissionEnumerationPreservesOtherFailures(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires non-root Unix permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	paths, err := RunRipgrepFiles(root, RipgrepFileOptions{Paths: []string{"locked", "missing"}})
	if err == nil || paths != nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("mixed failure was swallowed: %v, %v", paths, err)
	}
}

func TestPermissionDiagnosticTraceDoesNotRecordOmissions(t *testing.T) {
	root := t.TempDir()
	d := fileErrorDiagnostics{workingDir: root, goos: "linux", diagnosticOnly: true}
	_, _ = d.Write([]byte("rg: ./private: Permission denied (os error 13)\n"))
	d.finishLine()
	if d.parsedPermissions != 1 || len(AccessFailures(root)) != 0 {
		t.Fatal("diagnostic-only walk changed export omissions")
	}
}

func TestKnownTextClassificationDoesNotProbeAccess(t *testing.T) {
	// A filename-only classification must work even without a file to stat or
	// open. Required preview/export operations establish access later.
	root := t.TempDir()
	paths := []string{"main.go", "notes.txt", "config.json"}
	classified, err := ClassifyTextPaths(root, paths)
	if err != nil || len(classified) != len(paths) || len(AccessFailures(root)) != 0 {
		t.Fatalf("known-extension fast path performed access checks: %v %v %+v", classified, err, AccessFailures(root))
	}
}

func TestPermissionContentFiltersRecordSkippedInputs(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permissions")
	}
	root := t.TempDir()
	good, bad := filepath.Join(root, "good.txt"), filepath.Join(root, "pcre2.txt")
	if err := os.WriteFile(good, []byte("needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("needle\n"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0600) })
	for _, lines := range []bool{false, true} {
		accessFailures.Delete(bad)
		if lines {
			matches, err := RunRipgrepMatchLines("needle", []string{good, bad})
			if err != nil || len(matches) != 1 {
				t.Fatalf("lines: %v %v", matches, err)
			}
		} else {
			matches, err := RunRipgrepMatches("needle", []string{good, bad})
			if err != nil || len(matches) != 1 {
				t.Fatalf("matches: %v %v", matches, err)
			}
		}
		issues := AccessFailures(root)
		if len(issues) != 1 || issues[0].Path != "pcre2.txt" || issues[0].Metadata {
			t.Fatalf("wrong body failure: %+v", issues)
		}
	}
}
