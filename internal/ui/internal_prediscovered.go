package ui

import (
	"errors"
	"io"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
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
	plan, checkpoint, err := buildPrediscoveredTreePlan(cfg)
	if err != nil {
		return err
	}
	return RenderTreePreviewFromPlan(stdout, cfg.Render, checkpoint.GitContext, plan, nil, FzfFilterTreeRenderOptions(), checkpoint.GitStatus)
}

func buildPrediscoveredTreePlan(cfg prediscoveredCommandConfig) (output.Plan, discovery.CheckpointData, error) {
	checkpoint, err := discovery.ReadCheckpoint(cfg.CheckpointPath)
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

	entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	if err != nil {
		return output.Plan{}, discovery.CheckpointData{}, err
	}
	evaluatedScopes := []output.EvaluatedScope{{
		Paths:   scope.Paths,
		Entries: append([]discovery.Entry(nil), entries...),
	}}
	plan, err := output.BuildPlanForResolvedScopes(checkpoint.GitContext, []command.ExecutionScope{scope}, evaluatedScopes, entries)
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
	checkpoint, err := discovery.ReadCheckpoint(cfg.CheckpointPath)
	if err != nil {
		return err
	}
	if len(cfg.Scopes) > 1 {
		return newUsageError("Error: --internal-lines-preview accepts one preview scope.")
	}
	var scope command.ExecutionScope
	if len(cfg.Scopes) == 1 {
		scope = cfg.Scopes[0]
	}
	entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
	if err != nil {
		return err
	}
	// Hovered --lines preview renders bodies only; it does not need the exact
	// size/count summary work the normal plan builder performs for ranged lines.
	// Build the same deduped file list, then let emit stream the slices directly.
	plan := output.BuildLinesPreviewPlanForResolvedScopes([]command.ExecutionScope{scope}, entries)
	// Prefetch disabled: the preview pane only renders a screenful of
	// output, so we never need every file body queued ahead of time.
	return output.WriteOutputPlanPayloadWithoutPrefetch(stdout, emitCfg, plan)
}

func RunInternalPrediscoveredContentMatchList(cfg prediscoveredCommandConfig, stdout io.Writer) error {
	checkpoint, err := discovery.ReadCheckpoint(cfg.CheckpointPath)
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
	candidateScope, ok := scopeWithoutTerminalLiveContentMatchStage(scope)
	if !ok {
		entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, scope, checkpoint.Entries)
		if err != nil {
			if errors.Is(err, search.ErrRipgrepBadPattern) {
				return nil
			}
			return err
		}
		rows := contentMatchRowsFromEntries(entries)
		rows = attachFirstMatchLines(rows, entries, contentMatchScopePattern(scope))
		return writeContentMatchRows(stdout, rows)
	}

	entries, err := discovery.ApplyPrediscoveredScopeTail(cfg.Invocation, checkpoint.GitContext, candidateScope, checkpoint.Entries)
	if err != nil {
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			return nil
		}
		return err
	}
	rows, err := contentMatchRowsWithFirstMatchLines(entries, contentMatchScopePattern(scope))
	if err != nil {
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			return nil
		}
		return err
	}
	return writeContentMatchRows(stdout, rows)
}
