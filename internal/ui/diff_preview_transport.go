package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

// This is rendering state, not a membership checkpoint. In particular an empty
// focused path must retain the old single-target fallback, not show a tree.
type diffPreviewState struct {
	Version int                    `json:"version"`
	Scope   command.ExecutionScope `json:"scope"`
}

func prepareDiffPreview(currentArgs []string) (string, func()) {
	noop := func() {}
	cfg, err := cli.ParseArgsAllowImplicitDot(currentArgs)
	if err != nil {
		return "", noop
	}
	scopes := command.ExecutionScopesFromSpec(cfg.Command)
	if len(scopes) == 0 || !scopes[len(scopes)-1].Diff {
		return "", noop
	}
	scope := scopes[len(scopes)-1]
	// Only one original scope with one target has a fallback. Focused rows
	// supply their own path; retaining thousands of unused roots is unnecessary.
	scope.Targets = nil
	if len(scopes) == 1 && len(scopes[0].Targets) == 1 {
		scope.Targets = append([]string(nil), scopes[0].Targets...)
	}
	data, err := json.Marshal(diffPreviewState{Version: 1, Scope: scope})
	if err != nil {
		return "", noop
	}
	dir, err := os.MkdirTemp("", "catclip-diff-preview-*")
	if err != nil {
		return "", noop
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		cleanup()
		return "", noop
	}
	cmd := discovery.FzfDiffFilePreviewCommand(path)
	if cmd == "" {
		cleanup()
		return "", noop
	}
	return cmd, cleanup
}

func filePreviewWithDiffState(cfg filePreviewConfig) (filePreviewConfig, error) {
	if cfg.CheckpointPath != "" || cfg.SearchingHint || len(cfg.Scopes) != 1 ||
		len(cfg.Scopes[0].Targets) != 1 || cfg.Scopes[0].Targets[0] != "." ||
		cfg.Scopes[0].NoIgnore || cfg.Scopes[0].Paths || len(cfg.Scopes[0].Stages) != 0 {
		return cfg, fmt.Errorf("diff preview state does not accept a live scope or content checkpoint")
	}
	f, err := os.Open(cfg.DiffStatePath)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var state diffPreviewState
	if err := dec.Decode(&state); err != nil {
		return cfg, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return cfg, fmt.Errorf("diff preview state contains trailing data")
	}
	if state.Version != 1 || !state.Scope.Diff || len(state.Scope.Targets) > 1 {
		return cfg, fmt.Errorf("invalid diff preview state")
	}
	for _, target := range state.Scope.Targets {
		if target == "" {
			return cfg, fmt.Errorf("empty diff preview fallback target")
		}
		if err := discovery.ValidateTargetBoundary(target); err != nil {
			return cfg, err
		}
	}
	cfg.Scopes = []command.ExecutionScope{state.Scope}
	return cfg, nil
}
