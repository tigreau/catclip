package catclip

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// The checkpoint is rel-only by contract: AbsPath is NOT serialized. It is a
// runtime-derived filesystem handle (workingDir + RelPath) that
// applyPrediscoveredScopeTail re-derives via ensureEntryAbsPaths before any
// disk-backed stage, so storing it duplicates RelPath and bloats the decode
// that profiling showed dominates preview cost
// (docs/versions/v0.5.5/reports/ACTIVE_PLAN_preview_refresh_cost.md).
//
// json tags also use omitempty on the fields a typical full-file visible entry
// leaves at their zero value (target_root, snippet/lines/diff fields,
// allowed_by_include, block_source, …); for such entries this drops ~9 fields
// from the serialized form. Zero values round-trip transparently (absent →
// decoded as zero). RelPath, ModTime, and Mode are kept unconditional.
type prediscoveredCheckpointEntry struct {
	RelPath          string    `json:"rel"`
	ModTime          time.Time `json:"mod_time"`
	SizeBytes        int64     `json:"size_bytes,omitempty"`
	SizeKnown        bool      `json:"size_known,omitempty"`
	TargetRoot       string    `json:"target_root,omitempty"`
	GitVisible       bool      `json:"git_visible,omitempty"`
	Mode             entryMode `json:"mode"`
	SnippetPattern   string    `json:"snippet_pattern,omitempty"`
	Lines            bool      `json:"lines,omitempty"`
	LinesStart       int       `json:"lines_start,omitempty"`
	LinesEnd         int       `json:"lines_end,omitempty"`
	DiffWantStaged   bool      `json:"diff_want_staged,omitempty"`
	DiffWantUnstaged bool      `json:"diff_want_unstaged,omitempty"`
	AllowedByInclude bool      `json:"allowed_by_include,omitempty"`
	BlockSource      string    `json:"block_source,omitempty"`
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

func writePrediscoveredCheckpoint(path, workingDir string, data prediscoveredCheckpointData) error {
	data.Entries = fillEntrySizes(workingDir, data.Entries)
	raw, err := marshalPrediscoveredCheckpoint(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// fillEntrySizes captures file sizes once, at checkpoint creation, so that
// per-refresh preview plan rebuilds read entry.SizeBytes (fileBodySize's fast
// path) instead of re-Lstat'ing every file on every fzf refresh. rg discovery
// yields paths only (SizeKnown=false), so without this each refresh re-stats
// the scope. See docs/versions/v0.5.5/reports/ACTIVE_PLAN_checkpoint_size_capture.md.
//
// Serial by decision: the one-time open cost is amortized over the picker
// session and small next to the rg discovery picker-open already runs. A
// per-file Lstat failure leaves that entry SizeKnown=false; it falls back to
// the prior per-refresh behavior for that one file; the checkpoint is never
// failed. Lstat (not Stat) matches fileBodySize and keeps symlinks excluded by
// policy. Sizes freeze at this point; previews are transient and the final
// emit re-reads bodies, so a file resized mid-session is never copied stale.
func fillEntrySizes(workingDir string, entries []fileEntry) []fileEntry {
	for i := range entries {
		if entries[i].SizeKnown {
			continue
		}
		abs := entries[i].AbsPath
		if abs == "" {
			if strings.TrimSpace(entries[i].RelPath) == "" {
				continue
			}
			abs = filepath.Join(workingDir, filepath.FromSlash(entries[i].RelPath))
		}
		info, err := os.Lstat(abs)
		if err != nil {
			continue
		}
		entries[i].SizeBytes = info.Size()
		entries[i].SizeKnown = true
	}
	return entries
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
	plan, checkpoint, err := buildPrediscoveredTreePlan(cfg)
	if err != nil {
		return err
	}
	return encodeTreePayloadFromPlan(stdout, cfg.Render, checkpoint.GitContext, plan, nil, checkpoint.GitStatus)
}

// runInternalPrediscoveredTreePreview renders the checkpoint-backed filter
// preview directly from heavy catclip. This preserves the same tree text the
// old `--internal-tree-payload | catclip-tree` pipe produced, but removes one
// process spawn plus the intermediate payload JSON encode/decode on every fzf
// refresh. It is deliberately limited to prediscovered checkpoints; target-tier
// previews still use the payload renderer until they have a settled scope.
func runInternalPrediscoveredTreePreview(cfg prediscoveredCommandConfig, stdout io.Writer) error {
	plan, checkpoint, err := buildPrediscoveredTreePlan(cfg)
	if err != nil {
		return err
	}
	renderCfg := cfg.Render
	renderCfg.TreePayload = true
	if len(plan.items) == 0 {
		doc, ok := buildEmptyTreeDocument(renderCfg)
		if !ok {
			return errTreePayloadEmptyNoTarget
		}
		return renderTreeDocument(stdout, doc, fzfFilterTreeRenderOptions(), ansiColorPalette())
	}
	report, err := buildOutputReportForPlan(renderCfg, checkpoint.GitContext, plan, nil, checkpoint.GitStatus)
	if err != nil {
		return err
	}
	return renderTreeDocument(stdout, buildTreeDocumentFromPreview(renderCfg, plan, report), fzfFilterTreeRenderOptions(), ansiColorPalette())
}

func buildPrediscoveredTreePlan(cfg prediscoveredCommandConfig) (outputPlan, prediscoveredCheckpointData, error) {
	checkpoint, err := readPrediscoveredCheckpoint(cfg.CheckpointPath)
	if err != nil {
		return outputPlan{}, prediscoveredCheckpointData{}, err
	}

	if len(cfg.Scopes) > 1 {
		return outputPlan{}, prediscoveredCheckpointData{}, newUsageError("Error: --internal-prediscovered accepts one preview scope.")
	}
	var scope executionScope
	if len(cfg.Scopes) == 1 {
		scope = cfg.Scopes[0]
	}

	entries, err := applyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	if err != nil {
		return outputPlan{}, prediscoveredCheckpointData{}, err
	}
	evaluatedScopes := []evaluatedOutputScope{{
		Paths:   scope.Paths,
		Entries: append([]fileEntry(nil), entries...),
	}}
	plan, err := buildOutputPlanForResolvedScopes(checkpoint.GitContext, []executionScope{scope}, evaluatedScopes, entries)
	if err != nil {
		return outputPlan{}, prediscoveredCheckpointData{}, err
	}
	return plan, checkpoint, nil
}

func fzfFilterTreeRenderOptions() treeRenderOptions {
	return treeRenderOptions{
		Bare:          true,
		ShowModeTags:  true,
		ShowSizes:     true,
		ShowGitStatus: true,
		ShowSummary:   true,
		ShowTokens:    true,
		PreviewTheme:  fzfTreePreviewTheme,
	}
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
			// AbsPath intentionally not serialized — re-derived at read.
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
			// AbsPath left empty — ensureEntryAbsPaths re-derives it from
			// workingDir + RelPath before any disk-backed stage.
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
