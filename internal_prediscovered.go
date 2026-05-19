package catclip

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const prediscoveredCheckpointVersion = 1

type prediscoveredCheckpointData struct {
	GitContext gitContext
	GitStatus  map[string]string
	Entries    []fileEntry
}

type prediscoveredCommandConfig struct {
	CheckpointPath string
	Invocation     invocationConfig
	Render         renderConfig
	Scopes         []executionScope
}

func prediscoveredCommandConfigFromParsedCommand(cfg parsedCommand) prediscoveredCommandConfig {
	return prediscoveredCommandConfig{
		CheckpointPath: cfg.PrediscoveredPath,
		Invocation:     invocationConfigFromParsedCommand(cfg),
		Render:         renderConfigFromParsedCommand(cfg),
		Scopes:         executionScopesFromCommandSpec(cfg.Command),
	}
}

type prediscoveredCheckpointDocument struct {
	Version    int                            `json:"version"`
	GitContext prediscoveredCheckpointGit     `json:"git_context"`
	GitStatus  map[string]string              `json:"git_status"`
	Entries    []prediscoveredCheckpointEntry `json:"entries"`
}

type prediscoveredCheckpointGit struct {
	Enabled    bool   `json:"enabled"`
	Root       string `json:"root"`
	WorkPrefix string `json:"work_prefix"`
	HasHead    bool   `json:"has_head"`
}

type prediscoveredCheckpointEntry struct {
	AbsPath          string    `json:"abs"`
	RelPath          string    `json:"rel"`
	ModTime          time.Time `json:"mod_time"`
	SizeBytes        int64     `json:"size_bytes"`
	SizeKnown        bool      `json:"size_known"`
	TargetRoot       string    `json:"target_root"`
	GitVisible       bool      `json:"git_visible"`
	Mode             entryMode `json:"mode"`
	SnippetPattern   string    `json:"snippet_pattern"`
	Lines            bool      `json:"lines"`
	LinesStart       int       `json:"lines_start"`
	LinesEnd         int       `json:"lines_end"`
	DiffWantStaged   bool      `json:"diff_want_staged"`
	DiffWantUnstaged bool      `json:"diff_want_unstaged"`
	AllowedByInclude bool      `json:"allowed_by_include"`
	BlockSource      string    `json:"block_source"`
}

func marshalPrediscoveredCheckpoint(data prediscoveredCheckpointData) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodePrediscoveredCheckpoint(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unmarshalPrediscoveredCheckpoint(raw []byte) (prediscoveredCheckpointData, error) {
	return decodePrediscoveredCheckpoint(bytes.NewReader(raw))
}

func writePrediscoveredCheckpoint(path string, data prediscoveredCheckpointData) error {
	raw, err := marshalPrediscoveredCheckpoint(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func readPrediscoveredCheckpoint(path string) (prediscoveredCheckpointData, error) {
	f, err := os.Open(path)
	if err != nil {
		return prediscoveredCheckpointData{}, err
	}
	defer f.Close()
	return decodePrediscoveredCheckpoint(f)
}

func runInternalPrediscoveredTreePayload(cfg prediscoveredCommandConfig, stdout io.Writer) error {
	checkpoint, err := readPrediscoveredCheckpoint(cfg.CheckpointPath)
	if err != nil {
		return err
	}

	if len(cfg.Scopes) > 1 {
		return newUsageError("Error: --internal-prediscovered accepts one preview scope.")
	}
	var scope executionScope
	if len(cfg.Scopes) == 1 {
		scope = cfg.Scopes[0]
	}

	entries, err := applyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	if err != nil {
		return err
	}
	evaluatedScopes := []evaluatedOutputScope{{
		Paths:   scope.Paths,
		Entries: append([]fileEntry(nil), entries...),
	}}
	plan, err := buildOutputPlanForResolvedScopes(checkpoint.GitContext, []executionScope{scope}, evaluatedScopes, entries)
	if err != nil {
		return err
	}
	return encodeTreePayloadFromPlan(stdout, cfg.Render, checkpoint.GitContext, plan, nil, checkpoint.GitStatus)
}

// runInternalLinesPreview emits byte-faithful file content for the lines
// picker preview pane. It loads the prediscovered checkpoint, applies the
// scope tail (which includes the --lines stage chosen by the picker's
// hovered values), builds the output plan, and writes the actual emit
// payload — the same bytes the sink would paste — directly to stdout.
//
// Unlike runInternalPrediscoveredTreePayload (which emits a tree-only
// metadata document), this path includes file bodies because the picker
// is slicing those bodies; seeing the slice is the point of the preview.
func runInternalLinesPreview(cfg prediscoveredCommandConfig, emitCfg emitConfig, stdout io.Writer) error {
	checkpoint, err := readPrediscoveredCheckpoint(cfg.CheckpointPath)
	if err != nil {
		return err
	}
	if len(cfg.Scopes) > 1 {
		return newUsageError("Error: --internal-lines-preview accepts one preview scope.")
	}
	var scope executionScope
	if len(cfg.Scopes) == 1 {
		scope = cfg.Scopes[0]
	}
	entries, err := applyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	if err != nil {
		return err
	}
	evaluatedScopes := []evaluatedOutputScope{{
		Paths:   scope.Paths,
		Entries: append([]fileEntry(nil), entries...),
	}}
	plan, err := buildOutputPlanForResolvedScopes(checkpoint.GitContext, []executionScope{scope}, evaluatedScopes, entries)
	if err != nil {
		return err
	}
	// Prefetch disabled: the preview pane only renders a screenful of
	// output, so we never need every file body queued ahead of time.
	return writeOutputPlanPayloadWithoutPrefetch(stdout, emitCfg, plan)
}

func runInternalPrediscoveredContentMatchList(cfg prediscoveredCommandConfig, stdout io.Writer) error {
	checkpoint, err := readPrediscoveredCheckpoint(cfg.CheckpointPath)
	if err != nil {
		return err
	}

	if len(cfg.Scopes) > 1 {
		return newUsageError("Error: --internal-prediscovered accepts one preview scope.")
	}
	if len(cfg.Scopes) == 0 {
		return nil
	}
	scope := cfg.Scopes[0]
	// The picker runs this preview command on every keystroke, including
	// the initial frame where the user hasn't typed anything yet — fzf
	// substitutes `{q}` as an empty string. An empty regex would fail
	// validation inside applyScopeStages and surface as
	// "Command failed: ..." in the fzf preview pane. Short-circuit so the
	// preview shows an empty list while the input is empty (matches the
	// behavior of the legacy contentMatchRowsForScope path).
	if strings.TrimSpace(contentMatchScopePattern(scope)) == "" {
		return nil
	}
	entries, err := applyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	if err != nil {
		if errors.Is(err, errRipgrepBadPattern) {
			return nil
		}
		return err
	}
	rows := contentMatchRowsFromEntries(entries)
	rows = attachFirstMatchLines(rows, entries, contentMatchScopePattern(scope))
	return writeContentMatchRows(stdout, rows)
}

func applyPrediscoveredScopeTail(cfg invocationConfig, gitCtx gitContext, scope executionScope, entries []fileEntry) ([]fileEntry, error) {
	resolver := scopeResolver{
		cfg:               cfg,
		gitCtx:            gitCtx,
		allowFileSymlinks: false,
		withBinaries:      cfg.WithBinaries,
		includedTargets:   buildIncludedTargetSet(cfg.WorkingDir, scope.IncludedTargets),
		wantedBasenames:   collectWantedBasenames(scope.Targets),
		scopeTargets:      append([]string(nil), scope.Targets...),
	}

	entries = append([]fileEntry(nil), entries...)
	var err error
	entries, err = applyScopeStages(&resolver, gitCtx, scope, entries)
	if err != nil {
		return nil, err
	}
	if executionScopeHasEntryOutputMode(scope) {
		stampEntriesWithScopeOutputMode(entries, executionScopeOutputMode(scope), scope)
	}
	return ensureEntryAbsPaths(entries, cfg.WorkingDir), nil
}

func encodePrediscoveredCheckpoint(w io.Writer, data prediscoveredCheckpointData) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(newPrediscoveredCheckpointDocument(data))
}

func decodePrediscoveredCheckpoint(r io.Reader) (prediscoveredCheckpointData, error) {
	var doc prediscoveredCheckpointDocument
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return prediscoveredCheckpointData{}, err
	}
	if doc.Version != prediscoveredCheckpointVersion {
		return prediscoveredCheckpointData{}, fmt.Errorf("unsupported prediscovered checkpoint version %d", doc.Version)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return prediscoveredCheckpointData{}, fmt.Errorf("prediscovered checkpoint contains trailing JSON data")
		}
		return prediscoveredCheckpointData{}, err
	}
	return prediscoveredCheckpointData{
		GitContext: doc.GitContext.toGitContext(),
		GitStatus:  cloneStringMapOrEmpty(doc.GitStatus),
		Entries:    checkpointEntriesToFileEntries(doc.Entries),
	}, nil
}

func newPrediscoveredCheckpointDocument(data prediscoveredCheckpointData) prediscoveredCheckpointDocument {
	return prediscoveredCheckpointDocument{
		Version:    prediscoveredCheckpointVersion,
		GitContext: newPrediscoveredCheckpointGit(data.GitContext),
		GitStatus:  cloneStringMapOrEmpty(data.GitStatus),
		Entries:    fileEntriesToCheckpointEntries(data.Entries),
	}
}

func newPrediscoveredCheckpointGit(gitCtx gitContext) prediscoveredCheckpointGit {
	return prediscoveredCheckpointGit{
		Enabled:    gitCtx.Enabled,
		Root:       gitCtx.Root,
		WorkPrefix: gitCtx.WorkPrefix,
		HasHead:    gitCtx.HasHead,
	}
}

func (g prediscoveredCheckpointGit) toGitContext() gitContext {
	return gitContext{
		Enabled:    g.Enabled,
		Root:       g.Root,
		WorkPrefix: g.WorkPrefix,
		HasHead:    g.HasHead,
	}
}

func fileEntriesToCheckpointEntries(entries []fileEntry) []prediscoveredCheckpointEntry {
	out := make([]prediscoveredCheckpointEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, prediscoveredCheckpointEntry{
			AbsPath:          entry.AbsPath,
			RelPath:          entry.RelPath,
			ModTime:          entry.ModTime,
			SizeBytes:        entry.SizeBytes,
			SizeKnown:        entry.SizeKnown,
			TargetRoot:       entry.TargetRoot,
			GitVisible:       entry.GitVisible,
			Mode:             entry.Mode,
			SnippetPattern:   entry.SnippetPattern,
			Lines:            entry.Lines,
			LinesStart:       entry.LinesStart,
			LinesEnd:         entry.LinesEnd,
			DiffWantStaged:   entry.DiffWantStaged,
			DiffWantUnstaged: entry.DiffWantUnstaged,
			AllowedByInclude: entry.AllowedByInclude,
			BlockSource:      entry.BlockSource,
		})
	}
	return out
}

func checkpointEntriesToFileEntries(entries []prediscoveredCheckpointEntry) []fileEntry {
	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, fileEntry{
			AbsPath:          entry.AbsPath,
			RelPath:          entry.RelPath,
			ModTime:          entry.ModTime,
			SizeBytes:        entry.SizeBytes,
			SizeKnown:        entry.SizeKnown,
			TargetRoot:       entry.TargetRoot,
			GitVisible:       entry.GitVisible,
			Mode:             entry.Mode,
			SnippetPattern:   entry.SnippetPattern,
			Lines:            entry.Lines,
			LinesStart:       entry.LinesStart,
			LinesEnd:         entry.LinesEnd,
			DiffWantStaged:   entry.DiffWantStaged,
			DiffWantUnstaged: entry.DiffWantUnstaged,
			AllowedByInclude: entry.AllowedByInclude,
			BlockSource:      entry.BlockSource,
		})
	}
	return out
}

func cloneStringMapOrEmpty(src map[string]string) map[string]string {
	if src == nil {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
