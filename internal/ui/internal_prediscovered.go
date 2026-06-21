package ui

import (
	"errors"
	"io"
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
	CheckpointPath string
	Invocation     command.Invocation
	Render         RenderConfig
	Scopes         []command.ExecutionScope
}

func PrediscoveredCommandConfigFromParsedCommand(cfg command.Parsed) prediscoveredCommandConfig {
	return prediscoveredCommandConfig{
		CheckpointPath: cfg.PrediscoveredPath,
		Invocation:     invocationConfigFromParsedCommand(cfg),
		Render:         RenderConfigFromParsedCommand(cfg),
		Scopes:         command.ExecutionScopesFromSpec(cfg.Command),
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
	finishWriteBench := platform.InternalBenchSpan("ui.internal.lines_preview.write_payload",
		"items", platform.InternalBenchInt(plan.Len()),
	)
	err = output.WriteOutputPlanPayloadWithoutPrefetch(stdout, emitCfg, plan)
	finishWriteBench("err", platform.InternalBenchError(err))
	finishBench("err", platform.InternalBenchError(err))
	return err
}

func RunInternalPrediscoveredContentMatchList(cfg prediscoveredCommandConfig, stdout io.Writer) error {
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
	finishRowsBench := platform.InternalBenchSpan("ui.internal.content_match_list.first_match_rows",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	rows, err := contentMatchRowsWithFirstMatchLines(entries, contentMatchScopePattern(scope))
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
	err = writeContentMatchRows(stdout, rows)
	finishBench(
		"err", platform.InternalBenchError(err),
		"rows", platform.InternalBenchInt(len(rows)),
		"double_pass", "false",
	)
	return err
}
