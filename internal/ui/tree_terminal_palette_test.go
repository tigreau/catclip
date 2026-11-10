package ui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/theme"
)

func TestTreePreviewUsesSavedThemeWithoutChangingCLI(t *testing.T) {
	if id := os.Getenv("CATCLIP_TEST_TREE_THEME"); id != "" {
		if err := theme.Save(id); err != nil {
			t.Fatal(err)
		}
		selected := theme.Current()
		doc := treeDocument{Entries: []treeDocumentEntry{{Path: "src/example.go", GitStatus: "M"}}}
		colors := platform.ANSIPalette()
		opts := treeRenderOptions{ShowGitStatus: true}
		var final, expected, preview, disabled bytes.Buffer
		if err := renderpkg.RenderDocument(&expected, doc, opts, treePalette(colors)); err != nil {
			t.Fatal(err)
		}
		if err := renderTreeDocument(&final, doc, opts, colors); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(final.Bytes(), expected.Bytes()) {
			t.Fatal("saved preview theme changed CLI tree bytes")
		}
		opts.PreviewTheme = fzfTreePreviewTheme
		if err := renderTreeDocument(&preview, doc, opts, colors); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{selected.Palette.Dir + "src/", selected.Palette.Warn + "[M]"} {
			if !bytes.Contains(preview.Bytes(), []byte(want)) {
				t.Fatalf("%s tree preview missing %q: %q", id, want, preview.String())
			}
		}
		if err := renderTreeDocument(&disabled, doc, opts, platform.Palette{}); err != nil {
			t.Fatal(err)
		}
		if bytes.ContainsRune(disabled.Bytes(), '\x1b') {
			t.Fatal("disabled colors regained a theme")
		}
		t.Setenv("NO_COLOR", "1")
		var noColor bytes.Buffer
		if err := renderTreeDocument(&noColor, doc, opts, colors); err != nil {
			t.Fatal(err)
		}
		if bytes.ContainsRune(noColor.Bytes(), '\x1b') {
			t.Fatal("NO_COLOR tree preview gained color")
		}
		t.Setenv("NO_COLOR", "")

		project := setupTestProject(t, map[string]string{"src/example.go": "package main\n"})
		plan := testSinkOutputPlan(t, project, "src/example.go")
		report := output.Report{Sizes: map[string]int64{"src/example.go": 13}, Statuses: map[string]string{"src/example.go": "M"}}
		sink, err := renderSinkTreeReportPreview(StartupSinkPickerContext{Plan: plan, Report: report}, 128*1024)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(sink.Body, []byte(selected.Palette.Dir+"src/")) {
			t.Fatalf("output-picker tree did not use %s: %q", id, sink.Body)
		}
		return
	}
	for _, id := range []string{theme.TerminalID, "xcode-dark", "github", "solarized-dark", "solarized-light"} {
		t.Run(id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTreePreviewUsesSavedThemeWithoutChangingCLI$")
			cmd.Env = append(os.Environ(), "CATCLIP_TEST_TREE_THEME="+id, "XDG_CONFIG_HOME="+t.TempDir(), "NO_COLOR=", "COLORTERM=", "TERM=xterm-256color", "FZF_PREVIEW_COLUMNS=")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		})
	}
}
