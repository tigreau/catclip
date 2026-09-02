package catclip

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/ui"
)

// TestSinkPreviewTogglePersistsAcrossPreviewReruns stays at root
// because it re-execs the test binary with CATCLIP_TEST_RUN_MAIN=1
// to exercise --internal-sink-preview. That re-exec hits root TestMain's
// Main() branch — only the root test binary has it. ui.test doesn't
// know how to dispatch --internal-* flags, so the test must run from
// the root package.
//
// Uses the public ui.PrepareStartupSinkPreviewFiles +
// ui.StartupSinkPickerContext surface (re-exported after the 3A
// cleanup because the re-exec design forces them public).
func TestSinkPreviewTogglePersistsAcrossPreviewReruns(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	plan := testSinkOutputPlanRoot(t, project, "src/main.go")
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatalf("output.BuildReportForPlan returned error: %v", err)
	}
	files, err := ui.PrepareStartupSinkPreviewFiles(ui.StartupSinkPickerContext{
		Emit:   output.EmitConfig{},
		Render: ui.RenderConfig{ForceTreeMetadata: true},
		Plan:   plan,
		Report: report,
	})
	if err != nil {
		t.Fatalf("ui.PrepareStartupSinkPreviewFiles returned error: %v", err)
	}
	defer files.Cleanup()

	first := runSinkPreviewCommandRoot(t, files.PreviewCommand)
	if strings.Contains(first, "[preview mode: tree/report") {
		t.Fatalf("expected output-text mode first, got %q", first)
	}

	toggle := sinkPreviewToggleCommandForTestRoot(t, files.ToggleBinding)
	runShellCommandRoot(t, toggle)

	second := runSinkPreviewCommandRoot(t, files.PreviewCommand)
	if !strings.Contains(second, "[preview mode: tree/report") {
		t.Fatalf("expected tree/report mode after toggle, got %q", second)
	}

	third := runSinkPreviewCommandRoot(t, files.PreviewCommand)
	if !strings.Contains(third, "[preview mode: tree/report") {
		t.Fatalf("expected tree/report mode to persist across preview reruns, got %q", third)
	}
}

func testSinkOutputPlanRoot(t *testing.T, project, relPath string) output.Plan {
	t.Helper()
	absPath := filepath.Join(project, filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)
	if err != nil {
		t.Fatalf("stat %s: %v", relPath, err)
	}
	plan, err := output.PreparePlan(git.Context{}, []discovery.Entry{{
		AbsPath:    absPath,
		RelPath:    relPath,
		Mode:       command.EntryModeFull,
		SizeBytes:  info.Size(),
		SizeKnown:  true,
		GitVisible: true,
	}})
	if err != nil {
		t.Fatalf("output.PreparePlan returned error: %v", err)
	}
	if plan.IsEmpty() {
		t.Fatal("expected non-empty output plan")
	}
	return plan
}

func runSinkPreviewCommandRoot(t *testing.T, command string) string {
	t.Helper()
	return runShellCommandRoot(t, command)
}

func runShellCommandRoot(t *testing.T, command string) string {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		batPath := filepath.Join(t.TempDir(), "run.bat")
		if err := os.WriteFile(batPath, []byte("@echo off\r\n"+command+"\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("cmd", "/c", batPath)
	} else {
		cmd = exec.Command("/bin/sh", "-c", command)
	}
	cmd.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %q failed: %v\n%s", command, err, string(out))
	}
	return string(out)
}

func sinkPreviewToggleCommandForTestRoot(t *testing.T, binding string) string {
	t.Helper()
	const prefix = "ctrl-t:execute-silent("
	const suffix = ")+refresh-preview"
	firstBinding := strings.Split(binding, ",")[0]
	if !strings.HasPrefix(firstBinding, prefix) || !strings.HasSuffix(firstBinding, suffix) {
		t.Fatalf("unexpected toggle binding %q", binding)
	}
	return strings.TrimSuffix(strings.TrimPrefix(firstBinding, prefix), suffix)
}
