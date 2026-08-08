package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

// CheckpointData is the in-memory payload written to and read from a
// prediscovered checkpoint file. The picker writes one on open; fzf
// refreshes call the same binary back with --internal-prediscovered to
// read it. Was root prediscoveredCheckpointData before the v0.6.0
// discovery extraction.
type CheckpointData struct {
	GitContext git.Context
	GitStatus  map[string]string
	Entries    []Entry
	// NoIgnore signals that the parent scope was discovered with ignore rules
	// bypassed (via --no-ignore or a selective --include). Picker subprocesses that
	// run direct rg over the same scope must also bypass gitignore;
	// otherwise authorized-ignored files vanish from the picker.
	// See docs/versions/v0.6.4/reports/ACTIVE_PLAN_picker_no_ignore_for_include.md.
	NoIgnore bool
}

const checkpointVersion = 1

type checkpointDocument struct {
	Version    int               `json:"version"`
	GitContext checkpointGit     `json:"git_context"`
	GitStatus  map[string]string `json:"git_status"`
	Entries    []CheckpointEntry `json:"entries"`
	NoIgnore   bool              `json:"no_ignore,omitempty"`
}

type checkpointGit struct {
	Enabled    bool   `json:"enabled"`
	Root       string `json:"root"`
	WorkPrefix string `json:"work_prefix"`
	HasHead    bool   `json:"has_head"`
}

// The checkpoint is rel-only by contract: AbsPath is NOT serialized. It is a
// runtime-derived filesystem handle (workingDir + RelPath) that
// ApplyPrediscoveredScopeTail re-derives via EnsureEntryAbsPaths before any
// disk-backed stage, so storing it duplicates RelPath and bloats the decode
// that profiling showed dominates preview cost
// (docs/versions/v0.5.5/reports/ACTIVE_PLAN_preview_refresh_cost.md).
//
// json tags also use omitempty on the fields a typical full-file visible entry
// leaves at their zero value (target_root, snippet/lines/diff fields,
// allowed_by_include, block_source, …); for such entries this drops ~9 fields
// from the serialized form. Zero values round-trip transparently (absent →
// decoded as zero). RelPath, ModTime, and Mode are kept unconditional.
type CheckpointEntry struct {
	RelPath             string            `json:"rel"`
	ModTime             time.Time         `json:"mod_time"`
	SizeBytes           int64             `json:"size_bytes,omitempty"`
	SizeKnown           bool              `json:"size_known,omitempty"`
	TargetRoot          string            `json:"target_root,omitempty"`
	GitVisible          bool              `json:"git_visible,omitempty"`
	Mode                command.EntryMode `json:"mode"`
	SnippetPattern      string            `json:"snippet_pattern,omitempty"`
	SnippetContextSet   bool              `json:"snippet_context_set,omitempty"`
	SnippetContextLines int               `json:"snippet_context_lines,omitempty"`
	// SnippetMatchLines carries the snippet stage's pinned match-line
	// numbers across the checkpoint boundary so sink-open plan builds
	// reuse them instead of re-running rg (filter-attribute
	// persistence). Old checkpoints lack the field → nil → the
	// BatchSnippetMatches fallback recomputes.
	SnippetMatchLines []int  `json:"snippet_match_lines,omitempty"`
	Lines             bool   `json:"lines,omitempty"`
	LinesStart        int    `json:"lines_start,omitempty"`
	LinesEnd          int    `json:"lines_end,omitempty"`
	DiffWantStaged    bool   `json:"diff_want_staged,omitempty"`
	DiffWantUnstaged  bool   `json:"diff_want_unstaged,omitempty"`
	AllowedByInclude  bool   `json:"allowed_by_include,omitempty"`
	BlockSource       string `json:"block_source,omitempty"`
}

// MarshalCheckpoint encodes a CheckpointData as the JSON document
// format. Exposed so callers that need the bytes (e.g. for tests) can
// reuse the encoder.
func MarshalCheckpoint(data CheckpointData) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCheckpoint(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalCheckpoint is the byte-buffer counterpart to MarshalCheckpoint.
func UnmarshalCheckpoint(raw []byte) (CheckpointData, error) {
	return decodeCheckpoint(bytes.NewReader(raw))
}

// WriteCheckpoint serializes data to path as a prediscovered
// checkpoint. Captures missing file sizes before write so per-refresh
// preview plan rebuilds don't re-Lstat every file. Was root
// writePrediscoveredCheckpoint.
func WriteCheckpoint(path, workingDir string, data CheckpointData) error {
	data.Entries = FillEntrySizes(workingDir, data.Entries)
	raw, err := MarshalCheckpoint(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// ReadCheckpoint loads a prediscovered checkpoint from disk. Was root
// readPrediscoveredCheckpoint.
func ReadCheckpoint(path string) (CheckpointData, error) {
	f, err := os.Open(path)
	if err != nil {
		return CheckpointData{}, err
	}
	defer f.Close()
	return decodeCheckpoint(f)
}

// FillEntrySizes captures file sizes once, at checkpoint creation, so that
// per-refresh preview plan rebuilds read entry.SizeBytes (fileBodySize's fast
// path) instead of re-Lstat'ing every file on every fzf refresh. rg discovery
// yields paths only (SizeKnown=false), so without this each refresh re-stats
// the scope. See docs/versions/v0.5.5/reports/ACTIVE_PLAN_checkpoint_size_capture.md.
//
// Continue-on-error contract: a per-file Lstat failure leaves that entry
// SizeKnown=false; it falls back to the prior per-refresh behavior for
// that one file; the checkpoint is never failed. Different from
// EnsureEntrySizes / EnsureEntryModTimes, which are fail-fast because
// they run behind an explicit --size / --recent filter — here the size
// capture is opportunistic optimization for the checkpoint layer, so a
// single unreadable file must not block picker open.
//
// Lstat (not Stat) matches fileBodySize and keeps symlinks excluded by
// policy. Sizes freeze at this point; previews are transient and the final
// emit re-reads bodies, so a file resized mid-session is never copied stale.
//
// Exported for callers like lines_picker.go that write a checkpoint by
// hand without going through WriteCheckpoint.
func FillEntrySizes(workingDir string, entries []Entry) []Entry {
	paths := make([]string, len(entries))
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
		paths[i] = abs
	}
	infos, errs := parallelStat(paths, os.Lstat)
	for i := range entries {
		if paths[i] == "" || errs[i] != nil {
			continue
		}
		entries[i].SizeBytes = infos[i].Size()
		entries[i].SizeKnown = true
	}
	return entries
}

func encodeCheckpoint(w io.Writer, data CheckpointData) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(newCheckpointDocument(data))
}

func decodeCheckpoint(r io.Reader) (CheckpointData, error) {
	var doc checkpointDocument
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return CheckpointData{}, err
	}
	if doc.Version != checkpointVersion {
		return CheckpointData{}, fmt.Errorf("unsupported prediscovered checkpoint version %d", doc.Version)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return CheckpointData{}, fmt.Errorf("prediscovered checkpoint contains trailing JSON data")
		}
		return CheckpointData{}, err
	}
	return CheckpointData{
		GitContext: doc.GitContext.toGitContext(),
		GitStatus:  cloneStringMapOrEmpty(doc.GitStatus),
		Entries:    checkpointToEntries(doc.Entries),
		NoIgnore:   doc.NoIgnore,
	}, nil
}

func newCheckpointDocument(data CheckpointData) checkpointDocument {
	return checkpointDocument{
		Version:    checkpointVersion,
		GitContext: newCheckpointGit(data.GitContext),
		GitStatus:  cloneStringMapOrEmpty(data.GitStatus),
		Entries:    entriesToCheckpoint(data.Entries),
		NoIgnore:   data.NoIgnore,
	}
}

func newCheckpointGit(gitCtx git.Context) checkpointGit {
	return checkpointGit{
		Enabled:    gitCtx.Enabled,
		Root:       gitCtx.Root,
		WorkPrefix: gitCtx.WorkPrefix,
		HasHead:    gitCtx.HasHead,
	}
}

func (g checkpointGit) toGitContext() git.Context {
	return git.Context{
		Enabled:    g.Enabled,
		Root:       g.Root,
		WorkPrefix: g.WorkPrefix,
		HasHead:    g.HasHead,
	}
}

func entriesToCheckpoint(entries []Entry) []CheckpointEntry {
	out := make([]CheckpointEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, CheckpointEntry{
			// AbsPath intentionally not serialized — re-derived at read.
			RelPath:             entry.RelPath,
			ModTime:             entry.ModTime,
			SizeBytes:           entry.SizeBytes,
			SizeKnown:           entry.SizeKnown,
			TargetRoot:          entry.TargetRoot,
			GitVisible:          entry.GitVisible,
			Mode:                entry.Mode,
			SnippetPattern:      entry.SnippetPattern,
			SnippetContextSet:   entry.SnippetContextSet,
			SnippetContextLines: entry.SnippetContextLines,
			SnippetMatchLines:   append([]int(nil), entry.SnippetMatchLines...),
			Lines:               entry.Lines,
			LinesStart:          entry.LinesStart,
			LinesEnd:            entry.LinesEnd,
			DiffWantStaged:      entry.DiffWantStaged,
			DiffWantUnstaged:    entry.DiffWantUnstaged,
			AllowedByInclude:    entry.AllowedByInclude,
			BlockSource:         entry.BlockSource,
		})
	}
	return out
}

func checkpointToEntries(entries []CheckpointEntry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, Entry{
			// AbsPath left empty — EnsureEntryAbsPaths re-derives it from
			// workingDir + RelPath before any disk-backed stage.
			RelPath:             entry.RelPath,
			ModTime:             entry.ModTime,
			SizeBytes:           entry.SizeBytes,
			SizeKnown:           entry.SizeKnown,
			TargetRoot:          entry.TargetRoot,
			GitVisible:          entry.GitVisible,
			Mode:                entry.Mode,
			SnippetPattern:      entry.SnippetPattern,
			SnippetContextSet:   entry.SnippetContextSet,
			SnippetContextLines: entry.SnippetContextLines,
			SnippetMatchLines:   append([]int(nil), entry.SnippetMatchLines...),
			Lines:               entry.Lines,
			LinesStart:          entry.LinesStart,
			LinesEnd:            entry.LinesEnd,
			DiffWantStaged:      entry.DiffWantStaged,
			DiffWantUnstaged:    entry.DiffWantUnstaged,
			AllowedByInclude:    entry.AllowedByInclude,
			BlockSource:         entry.BlockSource,
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

// ApplyPrediscoveredScopeTail re-runs the scope-stages tail against a
// checkpoint's entries. Used by the prediscovered runners at root
// (tree-preview / lines-preview / content-match-list) and by preview
// refresh benches. The function owns Resolver construction so callers
// don't need to know the resolver's internal field shape.
func ApplyPrediscoveredScopeTail(cfg command.Invocation, gitCtx git.Context, scope command.ExecutionScope, entries []Entry) ([]Entry, error) {
	resolver := Resolver{
		Cfg:               cfg,
		GitCtx:            gitCtx,
		AllowFileSymlinks: false,
		WithBinaries:      cfg.WithBinaries,
		NoIgnore:          scope.NoIgnore || scope.HasStage(command.StageNoIgnore),
		IncludedTargets:   BuildIncludedTargetSet(cfg.WorkingDir, scope.IncludedTargets),
		WantedBasenames:   CollectWantedBasenames(scope.Targets),
		ScopeTargets:      append([]string(nil), scope.Targets...),
	}

	entries = append([]Entry(nil), entries...)
	var err error
	// Normal evaluation may clear entries after a hard diagnostic; its stage
	// runner must not let a later additive include resurrect a partial result.
	// A checkpoint has no such diagnostic state: an empty base can legitimately
	// mean the selected target contains only ignored files. Expand its leading
	// include once before the shared stage runner's empty-input guard.
	if len(entries) == 0 && len(scope.Stages) > 0 {
		switch scope.Stages[0].Kind {
		case command.StageInclude:
			entries, err = applyIncludeStage(&resolver, scope, entries, scope.Stages[0].Values, scope.Stages[0].ExactValues)
		case command.StageNoIgnore:
			entries, err = applyNoIgnoreStage(&resolver, scope, entries)
		}
		if err != nil {
			return nil, err
		}
	}
	entries, err = applyScopeStages(&resolver, gitCtx, scope, entries)
	if err != nil {
		return nil, err
	}
	if executionScopeHasEntryOutputMode(scope) {
		StampEntriesWithScopeOutputMode(entries, scope.OutputMode(), scope)
	}
	return EnsureEntryAbsPaths(entries, cfg.WorkingDir), nil
}
