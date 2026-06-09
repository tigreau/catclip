package output

import (
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

// Plan-builder entry points used by root dispatchers (cli.go),
// prediscovered runners (internal_prediscovered.go), and the startup
// sink picker. Was tree_payload_emit.go's plan-builder half at root;
// the render-bound functions (encodeTreePayloadFromPlan,
// renderTreePreviewFromPlan, runInternalTreePayloadFilePreview) stay
// at root in tree_payload_emit.go because they touch treeDocument /
// renderpkg.

// BuildPlanForResolvedScopes constructs the output plan for an
// already-evaluated invocation: per-scope entries plus the original
// command-side scopes (needed to inspect --paths and --recent stage
// presence).
func BuildPlanForResolvedScopes(
	gitCtx git.Context,
	scopes []command.ExecutionScope,
	evaluatedScopes []EvaluatedScope,
	allEntries []discovery.Entry,
) (Plan, error) {
	if ExecutionScopesUsePathsStage(scopes) {
		return prepareSectionedOutputPlan(gitCtx, evaluatedScopes, ExecutionScopesUseRecentStage(scopes))
	}
	entries := allEntries
	if ExecutionScopesUseRecentStage(scopes) {
		entries = discovery.DedupeEntriesByPathPreserveOrder(entries)
	} else {
		entries = discovery.DedupeEntriesByPath(entries)
	}
	return PreparePlan(gitCtx, entries)
}

// BuildLinesPreviewPlanForResolvedScopes is the lighter-weight planner
// used by the prediscovered --lines preview path. Skips sectioning and
// git-status work because the preview only renders the chosen line
// slice from each file body, not the full plan-state mode tags.
func BuildLinesPreviewPlanForResolvedScopes(scopes []command.ExecutionScope, allEntries []discovery.Entry) Plan {
	entries := allEntries
	if ExecutionScopesUseRecentStage(scopes) {
		entries = discovery.DedupeEntriesByPathPreserveOrder(entries)
	} else {
		entries = discovery.DedupeEntriesByPath(entries)
	}
	return buildLinesPreviewPlan(entries)
}

// BuildPlanForDiscoveredInvocation is the dispatcher's entry point.
// Walks the per-scope discovery output, builds the matching
// EvaluatedScope list, and produces the merged plan. cli.go's run()
// calls this once per invocation.
func BuildPlanForDiscoveredInvocation(gitCtx git.Context, inv discovery.Discovered) (Plan, error) {
	evaluatedScopes := make([]EvaluatedScope, 0, len(inv.Scopes))
	var allEntries []discovery.Entry
	resolvedScopes := make([]command.ExecutionScope, 0, len(inv.Scopes))
	for _, scope := range inv.Scopes {
		resolvedScopes = append(resolvedScopes, scope.Scope)
		entries := append([]discovery.Entry(nil), scope.Entries...)
		allEntries = append(allEntries, entries...)
		evaluatedScopes = append(evaluatedScopes, EvaluatedScope{
			Paths:   scope.Scope.Paths,
			Entries: entries,
		})
	}
	return BuildPlanForResolvedScopes(gitCtx, resolvedScopes, evaluatedScopes, allEntries)
}

// ExecutionScopesUseRecentStage reports whether any scope in the slice
// has a --recent stage. Discovery-side predicates can't help here
// because --recent shapes plan dedup behavior (preserve-order vs.
// merge-by-priority). Exposed for root callers that need the same
// gate for picker-side preview planning.
func ExecutionScopesUseRecentStage(scopes []command.ExecutionScope) bool {
	for _, s := range scopes {
		if s.HasStage(command.StageRecent) {
			return true
		}
	}
	return false
}

// ExecutionScopesUsePathsStage reports whether any scope uses --paths
// (either as a top-level flag or as a stage). The plan sectioning is
// keyed on this — --paths produces a separate "paths" section.
func ExecutionScopesUsePathsStage(scopes []command.ExecutionScope) bool {
	for _, s := range scopes {
		if s.Paths || s.HasStage(command.StagePaths) {
			return true
		}
	}
	return false
}
