package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// prediscoveredCommandConfig is the runner-side config for the
// --internal-prediscovered family of subcommands. The checkpoint
// serialization and the scope-tail re-application logic moved into
// internal/discovery; this struct is just the runtime wrapper that
// glues a parsed command to a checkpoint path.
type prediscoveredCommandConfig struct {
	CheckpointPath        string
	FileSetSelectionPath  string
	FileSetSelectionStage string
	Invocation            command.Invocation
	Render                RenderConfig
	Scopes                []command.ExecutionScope
}

func PrediscoveredCommandConfigFromParsedCommand(cfg command.Parsed) prediscoveredCommandConfig {
	return prediscoveredCommandConfig{
		CheckpointPath:        cfg.PrediscoveredPath,
		FileSetSelectionPath:  cfg.FileSetSelectionPath,
		FileSetSelectionStage: cfg.FileSetSelectionStage,
		Invocation:            command.InvocationFromParsed(cfg),
		Render:                RenderConfigFromParsedCommand(cfg),
		Scopes:                command.ExecutionScopesFromSpec(cfg.Command),
	}
}

// RunInternalPrediscoveredTreePreview renders the checkpoint-backed filter
// preview directly from heavy catclip. This preserves the same tree text the
// old producer-to-renderer pipe produced, but removes one process spawn plus
// the intermediate payload JSON encode/decode on every fzf refresh.
func RunInternalPrediscoveredTreePreview(cfg prediscoveredCommandConfig, stdout io.Writer) error {
	finishBench := platform.InternalBenchSpan("ui.internal.tree_preview",
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
	)
	plan, checkpoint, err := buildPrediscoveredTreePlan(cfg)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	err = RenderTreePreviewFromPlan(stdout, cfg.Render, checkpoint.GitContext, plan, nil, FzfFilterTreeRenderOptions(), checkpoint.GitStatus)
	finishBench(
		"err", platform.InternalBenchError(err),
		"items", platform.InternalBenchInt(plan.Len()),
	)
	return err
}

func buildPrediscoveredTreePlan(cfg prediscoveredCommandConfig) (output.Plan, discovery.CheckpointData, error) {
	finishCheckpointBench := platform.InternalBenchSpan("ui.internal.prediscovered.read_checkpoint")
	checkpoint, err := discovery.ReadCheckpoint(cfg.CheckpointPath)
	finishCheckpointBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(checkpoint.Entries)),
	)
	if err != nil {
		return output.Plan{}, discovery.CheckpointData{}, err
	}

	if len(cfg.Scopes) > 1 {
		return output.Plan{}, discovery.CheckpointData{}, newUsageError("Error: --internal-prediscovered accepts one preview scope.")
	}
	var scope command.ExecutionScope
	if len(cfg.Scopes) == 1 {
		scope = cfg.Scopes[0]
	}
	if cfg.FileSetSelectionPath != "" {
		values, err := readFzfFileSetSelection(cfg.FileSetSelectionPath)
		if err != nil {
			return output.Plan{}, discovery.CheckpointData{}, err
		}
		kind := command.StageOnly
		if cfg.FileSetSelectionStage == "exclude" {
			kind = command.StageExclude
		}
		scope.Stages = append(scope.Stages, command.Stage{Kind: kind, Values: values})
	}

	finishTailBench := platform.InternalBenchSpan("ui.internal.prediscovered.apply_tail",
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
		"checkpoint_entries", platform.InternalBenchInt(len(checkpoint.Entries)),
	)
	entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	finishTailBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(entries)),
	)
	if err != nil {
		return output.Plan{}, discovery.CheckpointData{}, err
	}
	evaluatedScopes := []output.EvaluatedScope{{
		Paths:   scope.Paths,
		Entries: append([]discovery.Entry(nil), entries...),
	}}
	finishPlanBench := platform.InternalBenchSpan("ui.internal.prediscovered.build_plan",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	plan, err := output.BuildPlanForResolvedScopes(checkpoint.GitContext, []command.ExecutionScope{scope}, evaluatedScopes, entries)
	finishPlanBench(
		"err", platform.InternalBenchError(err),
		"items", platform.InternalBenchInt(plan.Len()),
	)
	if err != nil {
		return output.Plan{}, discovery.CheckpointData{}, err
	}
	return plan, checkpoint, nil
}

func readFzfFileSetSelection(selectionPath string) ([]string, error) {
	f, err := os.Open(selectionPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	values := make([]string, 0, 16)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 || fields[1] == "" {
			return nil, fmt.Errorf("invalid fzf file-set selection row")
		}
		if _, ok := seen[fields[1]]; ok {
			continue
		}
		seen[fields[1]] = struct{}{}
		values = append(values, fields[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("empty fzf file-set selection")
	}
	return values, nil
}

func FzfFilterTreeRenderOptions() treeRenderOptions {
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

// RunInternalLinesPreview emits byte-faithful file content for the lines
// picker preview pane. It loads the prediscovered checkpoint, applies the
// scope tail (which includes the --lines stage chosen by the picker's
// hovered values), builds the output plan, and writes the actual emit
// payload — the same bytes the sink would paste — directly to stdout.
//
// Unlike the tree-document preview, this path includes file bodies because the
// picker is slicing those bodies; seeing the slice is the point of the preview.
//
// The stdout writer is wrapped in output.PreviewCapWriter with the
// reload-cancellation context. Two short-circuit conditions matter on
// large corpora:
//
//  1. Byte cap (output.PreviewByteLimit) bounds per-focus I/O. fzf's
//     preview pane renders a constant number of lines; emitting the
//     full plan (1.6–3.7 MiB on vscode-main) is pure waste, and on
//     Windows + Defender every per-file os.Open is intercepted.
//  2. Cancellation: when fzf moves focus, it SIGTERMs the previous
//     preview child. Honoring search.ReloadCancelContext lets the
//     write loop bail mid-emit instead of finishing a giant file scan
//     and delaying the next focus's preview.
//
// Either condition surfaces as output.ErrPreviewLimitReached, which we
// treat as success-with-(truncation|cancellation), not failure.
func RunInternalLinesPreview(cfg prediscoveredCommandConfig, emitCfg output.EmitConfig, stdout io.Writer) error {
	finishBench := platform.InternalBenchSpan("ui.internal.lines_preview",
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
	)
	finishCheckpointBench := platform.InternalBenchSpan("ui.internal.lines_preview.read_checkpoint")
	checkpoint, err := discovery.ReadCheckpoint(cfg.CheckpointPath)
	finishCheckpointBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(checkpoint.Entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	if len(cfg.Scopes) > 1 {
		err := newUsageError("Error: --internal-lines-preview accepts one preview scope.")
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	var scope command.ExecutionScope
	if len(cfg.Scopes) == 1 {
		scope = cfg.Scopes[0]
	}
	finishTailBench := platform.InternalBenchSpan("ui.internal.lines_preview.apply_tail",
		"checkpoint_entries", platform.InternalBenchInt(len(checkpoint.Entries)),
	)
	entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	finishTailBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	// Hovered --lines preview renders bodies only; it does not need the exact
	// size/count summary work the normal plan builder performs for ranged lines.
	// Build the same deduped file list, then let emit stream the slices directly.
	finishPlanBench := platform.InternalBenchSpan("ui.internal.lines_preview.build_plan",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	plan := output.BuildLinesPreviewPlanForResolvedScopes([]command.ExecutionScope{scope}, entries)
	finishPlanBench("items", platform.InternalBenchInt(plan.Len()))
	// Prefetch disabled: the preview pane only renders a screenful of
	// output, so we never need every file body queued ahead of time.
	cap := output.NewPreviewCapWriter(stdout, search.ReloadCancelContext(), output.PreviewByteLimit)
	finishWriteBench := platform.InternalBenchSpan("ui.internal.lines_preview.write_payload",
		"items", platform.InternalBenchInt(plan.Len()),
	)
	err = output.WriteOutputPlanPayloadWithoutPrefetch(cap, emitCfg, plan)
	if errors.Is(err, output.ErrPreviewLimitReached) {
		err = nil
	}
	finishWriteBench(
		"err", platform.InternalBenchError(err),
		"bytes", platform.InternalBenchInt(int(cap.BytesWritten())),
		"truncated", platform.InternalBenchBool(cap.Truncated()),
		"cancelled", platform.InternalBenchBool(cap.Cancelled()),
	)
	if err == nil && cap.Truncated() {
		// Footer goes to the raw stdout, not the cap (cap is full).
		_, _ = io.WriteString(stdout, "\n\n[lines preview truncated at 128 KiB — full content is in the file]\n")
	}
	finishBench("err", platform.InternalBenchError(err))
	return err
}

func RunInternalPrediscoveredContentMatchList(cfg prediscoveredCommandConfig, stdout io.Writer) error {
	// Before any work: terminate the prior keystroke's child if it's
	// still alive. On Windows fzf does not SIGTERM superseded reload
	// children, so without this they pile up — four typed characters
	// = four parallel 6.5 s rg scans fighting for CPU and Defender.
	// See killSupersededPredecessor doc for the file-coordination
	// design. No-op on POSIX (fzf already killed prior).
	killSupersededPredecessor(cfg.CheckpointPath, predecessorBucketContentMatch)
	finishBench := platform.InternalBenchSpan("ui.internal.content_match_list",
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
	)
	finishCheckpointBench := platform.InternalBenchSpan("ui.internal.content_match_list.read_checkpoint")
	checkpoint, err := discovery.ReadCheckpoint(cfg.CheckpointPath)
	finishCheckpointBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(checkpoint.Entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}

	if len(cfg.Scopes) > 1 {
		err := newUsageError("Error: --internal-prediscovered accepts one preview scope.")
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	if len(cfg.Scopes) == 0 {
		finishBench("err", "false", "rows", "0")
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
		finishBench("err", "false", "empty_pattern", "true", "rows", "0")
		return nil
	}
	// Direct-rg shortcut: when the scope is a bare target plus a live
	// content match (no narrowing filters that rg can't natively
	// express), one rg call against the scope target produces both the
	// file set AND first-match line numbers, replacing the chunked
	// per-file rg fan-out below.
	//
	// CORRECTNESS: the prediscovered checkpoint may carry an upstream-
	// filtered subset of the scope's filesystem files (e.g. parent ran
	// `catclip src --only "*.go"`, picker only sees the .go entries).
	// The live picker argv doesn't carry the parent's filters, so an
	// unconstrained `rg PATTERN <target>` would silently widen the
	// result set to include files the parent filtered out. To stay
	// correct, the direct path intersects rg's result with the
	// checkpoint entry set — direct rg gives the {relPath → line}
	// shape in one call, and the intersection enforces the upstream
	// filter invariant.
	//
	// See docs/versions/v0.6.3/reports/ACTIVE_PLAN_direct_rg_contains_snippet.md
	// for the eligibility analysis. Falls back to the chunked path
	// when any live-scope modifier (--only / --exclude / --recent /
	// git filters / etc.) is active.
	// The runDirectContentMatch helper only handles the positive
	// content predicate (--contains / --snippet). Pure-negative or
	// mixed-with-negative scopes fall back to the chunked path until
	// the picker dispatch grows a per-stage direct-mode pipeline
	// (v0.6.4 follow-up — tracked in
	// ACTIVE_PLAN_not_contains_modifier.md). The general eligibility
	// predicate permits --not-contains scopes, so we gate the picker
	// dispatch on the additional "no NotContains" condition.
	if command.IsDirectModeEligible(cfg.Invocation, scope) && len(scope.NotContains) == 0 {
		err := runDirectContentMatch(cfg, scope, checkpoint.Entries, checkpoint.NoIgnore, stdout)
		if err == nil {
			finishBench("err", "false", "route", "direct")
			return nil
		}
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			finishBench("err", "false", "bad_pattern", "true", "rows", "0", "route", "direct")
			return nil
		}
		finishBench("err", platform.InternalBenchError(err), "route", "direct")
		return err
	}
	candidateScope, ok := scopeWithoutTerminalLiveContentMatchStage(scope)
	if !ok {
		finishTailBench := platform.InternalBenchSpan("ui.internal.content_match_list.apply_tail_double_pass",
			"checkpoint_entries", platform.InternalBenchInt(len(checkpoint.Entries)),
		)
		entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
		finishTailBench(
			"err", platform.InternalBenchError(err),
			"entries", platform.InternalBenchInt(len(entries)),
		)
		if err != nil {
			if errors.Is(err, search.ErrRipgrepBadPattern) {
				finishBench("err", "false", "bad_pattern", "true", "rows", "0")
				return nil
			}
			finishBench("err", platform.InternalBenchError(err))
			return err
		}
		rows := contentMatchRowsFromEntries(entries)
		finishAttachBench := platform.InternalBenchSpan("ui.internal.content_match_list.attach_first_lines",
			"rows", platform.InternalBenchInt(len(rows)),
		)
		rows = attachFirstMatchLines(rows, entries, contentMatchScopePattern(scope))
		finishAttachBench("rows", platform.InternalBenchInt(len(rows)))
		err = writeContentMatchRows(stdout, rows)
		finishBench(
			"err", platform.InternalBenchError(err),
			"rows", platform.InternalBenchInt(len(rows)),
			"double_pass", "true",
		)
		return err
	}

	finishTailBench := platform.InternalBenchSpan("ui.internal.content_match_list.apply_tail_candidate",
		"checkpoint_entries", platform.InternalBenchInt(len(checkpoint.Entries)),
	)
	entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, candidateScope, checkpoint.Entries)
	finishTailBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(entries)),
	)
	if err != nil {
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			finishBench("err", "false", "bad_pattern", "true", "rows", "0")
			return nil
		}
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	pattern := contentMatchScopePattern(scope)
	liveKind, _ := liveContentMatchKind(scope)

	// --not-contains live: prune entries matching the pattern; no
	// first-match line (there's no match to center the preview on).
	// The memo plumbing is contains-shaped (prefix-extension narrows
	// the match set) and doesn't transfer cleanly to prunes, so skip
	// it for this kind.
	if liveKind == command.StageNotContains {
		finishRowsBench := platform.InternalBenchSpan("ui.internal.content_match_list.not_contains_rows",
			"entries", platform.InternalBenchInt(len(entries)),
		)
		pruned, err := discovery.FilterEntriesByNotContent(discovery.EnsureEntryAbsPaths(entries, cfg.Invocation.WorkingDir), pattern)
		finishRowsBench(
			"err", platform.InternalBenchError(err),
			"rows", platform.InternalBenchInt(len(pruned)),
		)
		if err != nil {
			if errors.Is(err, search.ErrRipgrepBadPattern) {
				finishBench("err", "false", "bad_pattern", "true", "rows", "0")
				return nil
			}
			finishBench("err", platform.InternalBenchError(err))
			return err
		}
		rows := contentMatchRowsFromEntries(pruned)
		err = writeContentMatchRows(stdout, rows)
		finishBench(
			"err", platform.InternalBenchError(err),
			"rows", platform.InternalBenchInt(len(rows)),
			"live_kind", "not-contains",
		)
		return err
	}

	// Prefix-extension memo: if the previous keystroke ran and cached
	// its matched paths, AND the new pattern is a prefix-extension of
	// the previous one, restrict the rg scan to that prior set. The
	// new matches are necessarily a subset, so the result is identical
	// but the per-keystroke cost drops from full-corpus scan to a
	// re-scan of the prior matches only. See contentMatchMemo doc.
	memoPath := contentMatchMemoPath(cfg.CheckpointPath)
	memoHit := false
	entriesForScan := entries
	if memo, ok := readContentMatchMemo(memoPath); ok {
		if restricted, hit := restrictEntriesByMemo(entries, memo, pattern); hit {
			memoHit = true
			entriesForScan = restricted
		}
	}
	finishRowsBench := platform.InternalBenchSpan("ui.internal.content_match_list.first_match_rows",
		"entries", platform.InternalBenchInt(len(entriesForScan)),
		"candidate_entries", platform.InternalBenchInt(len(entries)),
		"memo_hit", platform.InternalBenchBool(memoHit),
	)
	rows, err := contentMatchRowsWithFirstMatchLines(entriesForScan, pattern)
	finishRowsBench(
		"err", platform.InternalBenchError(err),
		"rows", platform.InternalBenchInt(len(rows)),
	)
	if err != nil {
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			finishBench("err", "false", "bad_pattern", "true", "rows", "0")
			return nil
		}
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	// Write the new memo for the next keystroke. Best-effort: a failed
	// write costs the next keystroke a full scan, not correctness.
	writeContentMatchMemo(memoPath, pattern, matchedAbsPathsFromRows(rows, entriesForScan))
	err = writeContentMatchRows(stdout, rows)
	finishBench(
		"err", platform.InternalBenchError(err),
		"rows", platform.InternalBenchInt(len(rows)),
		"double_pass", "false",
		"memo_hit", platform.InternalBenchBool(memoHit),
	)
	return err
}

// runDirectContentMatch handles the direct-rg eligible path: one rg
// call against the scope target returns {relPath → first-match line}
// in a single walk, replacing the chunked rg fan-out.
//
// Caller must verify command.IsDirectModeEligible(cfg.Invocation,
// scope) before invoking. The .hiss overlay is best-effort: if
// ReadableHissPath() fails (file unreadable / broken), the helper
// falls back to no overlay rather than aborting the picker reload —
// matching how RunRipgrepFiles handles the same case.
//
// allowedEntries is the prediscovered checkpoint's filtered entry
// set. The picker's argv-derived scope can't see the parent's
// upstream filters, so rg's raw walk may include files the parent
// filtered out. Intersecting against allowedEntries enforces the
// upstream filter invariant. Pass nil to skip intersection (e.g.
// the headless/fallback path where no prior filtering occurred).
//
// The memo (used by the chunked path for prefix-extension restriction)
// is intentionally not touched here. Direct mode's per-call cost is
// low enough that the memo's savings are minor, and skipping the
// write avoids cross-route contamination if eligibility flips mid-
// session.
func runDirectContentMatch(cfg prediscoveredCommandConfig, scope command.ExecutionScope, allowedEntries []discovery.Entry, noIgnore bool, stdout io.Writer) error {
	pattern := contentMatchScopePattern(scope)
	target := scope.Targets[0]
	hissPath, hissErr := discovery.ReadableHissPath()
	if hissErr != nil {
		hissPath = ""
	}
	finishDirectBench := platform.InternalBenchSpan("ui.internal.content_match_list.direct",
		"target", target,
		"pattern_len", platform.InternalBenchInt(len(pattern)),
	)
	var rgOpts []search.DirectOption
	if noIgnore {
		rgOpts = append(rgOpts, search.DirectNoIgnore())
	}
	firstLines, err := search.RunRipgrepDirectMatchLines(cfg.Invocation.WorkingDir, target, pattern, hissPath, rgOpts...)
	finishDirectBench(
		"err", platform.InternalBenchError(err),
		"matches", platform.InternalBenchInt(len(firstLines)),
		"bad_pattern", platform.InternalBenchCancelled(err, search.ErrRipgrepBadPattern),
	)
	if err != nil {
		return err
	}
	var allowed map[string]struct{}
	if allowedEntries != nil {
		allowed = make(map[string]struct{}, len(allowedEntries))
		for _, entry := range allowedEntries {
			rel := normalizeRelPath(entry.RelPath)
			if rel == "" {
				continue
			}
			allowed[rel] = struct{}{}
		}
	}
	rows := make([]contentMatchRow, 0, len(firstLines))
	for relPath, line := range firstLines {
		if relPath == "" || line < 1 {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[relPath]; !ok {
				continue
			}
		}
		rows = append(rows, contentMatchRow{RelPath: relPath, FirstMatchLine: line})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].RelPath < rows[j].RelPath
	})
	return writeContentMatchRows(stdout, rows)
}
