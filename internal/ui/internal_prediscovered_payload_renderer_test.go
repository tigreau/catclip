package ui

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/render"
)

// TestRunInternalPrediscoveredTreePreviewMatchesPayloadRenderer used to
// live at the root in internal_prediscovered_test.go. It compared the
// handler-dispatch path (run() routing to RunInternalPrediscoveredTreePreview)
// against the in-process chain (buildPrediscoveredTreePlan → encode →
// decode → renderTreeDocument). Since run() ultimately calls the same
// ui handler — see cli.go's --internal-tree-preview branch — the test
// moves into ui and calls the handler directly, which lets the chain
// helpers stay package-private.
func TestRunInternalPrediscoveredTreePreviewMatchesPayloadRenderer(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":  "package main\n",
		"src/b.go":  "package b\n",
		"src/c.txt": "notes\n",
	})
	parentCfg := parseInProject(t, project, []string{"src"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(command.InvocationFromParsed(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus: map[string]string{
			"src/a.go": "M",
		},
		Entries: discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	commonArgs := []string{
		"--internal-prediscovered", checkpointPath,
		"--only", "*.go",
		"--internal-tree-target", "src",
		"--internal-tree-kind", render.TargetKindDir,
		"--internal-tree-state", render.TargetStateOK,
	}
	previewCfg, err := cli.ParseArgs(append([]string{"--quiet", "--internal-tree-preview"}, commonArgs...))
	if err != nil {
		t.Fatalf("parseArgs preview returned error: %v", err)
	}

	prediscoveredCfg := PrediscoveredCommandConfigFromParsedCommand(previewCfg)
	plan, checkpoint, err := buildPrediscoveredTreePlan(prediscoveredCfg)
	if err != nil {
		t.Fatalf("buildPrediscoveredTreePlan returned error: %v", err)
	}
	var payload bytes.Buffer
	if err := EncodeTreePayloadFromPlan(&payload, TreeDocumentRenderConfig(RenderConfigFromParsedCommand(previewCfg)), checkpoint.GitContext, plan, nil, checkpoint.GitStatus); err != nil {
		t.Fatalf("EncodeTreePayloadFromPlan returned error: %v", err)
	}
	doc, err := decodeTreePayload(bytes.NewReader(payload.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	var renderedPayload bytes.Buffer
	if err := renderTreeDocument(&renderedPayload, doc, FzfFilterTreeRenderOptions(), platform.ANSIPalette()); err != nil {
		t.Fatalf("renderTreeDocument returned error: %v", err)
	}

	var previewStdout bytes.Buffer
	if err := RunInternalPrediscoveredTreePreview(prediscoveredCfg, &previewStdout); err != nil {
		t.Fatalf("RunInternalPrediscoveredTreePreview returned error: %v", err)
	}
	if !bytes.Equal(previewStdout.Bytes(), renderedPayload.Bytes()) {
		t.Fatalf("direct tree preview differs from payload renderer\npayload-rendered:\n%s\npreview:\n%s", renderedPayload.String(), previewStdout.String())
	}
}

func parsedExecutionScope(t *testing.T, cfg command.Parsed) command.ExecutionScope {
	t.Helper()
	scopes := command.ExecutionScopesFromSpec(cfg.Command)
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(scopes))
	}
	return scopes[0]
}
