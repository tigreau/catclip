package catclip

import (
	"errors"
	"io"
)

// errTreePayloadEmptyNoTarget signals that the caller asked for a tree payload
// but the plan was empty and no implicit tree target was available, so there is
// nothing to encode. The CLI surfaces a "no files matched" message for this
// case; pickers ignore it (they should never hit it because their caller has
// already validated the scope).
var errTreePayloadEmptyNoTarget = errors.New("tree payload: empty plan and no implicit target")

func buildOutputPlanForResolvedScopes(
	gitCtx gitContext,
	scopes []executionScope,
	evaluatedScopes []evaluatedOutputScope,
	allEntries []fileEntry,
) (outputPlan, error) {
	if executionScopesUsePathsStage(scopes) {
		return prepareSectionedOutputPlan(gitCtx, evaluatedScopes, executionScopesUseRecentStage(scopes))
	}
	entries := allEntries
	if executionScopesUseRecentStage(scopes) {
		entries = dedupeEntriesByPathPreserveOrder(entries)
	} else {
		entries = dedupeEntriesByPath(entries)
	}
	return prepareOutputPlan(gitCtx, entries)
}

func buildLinesPreviewPlanForResolvedScopes(scopes []executionScope, allEntries []fileEntry) outputPlan {
	entries := allEntries
	if executionScopesUseRecentStage(scopes) {
		entries = dedupeEntriesByPathPreserveOrder(entries)
	} else {
		entries = dedupeEntriesByPath(entries)
	}
	return buildLinesPreviewPlan(entries)
}

func buildOutputPlanForDiscoveredInvocation(gitCtx gitContext, inv discoveredInvocation) (outputPlan, error) {
	evaluatedScopes := make([]evaluatedOutputScope, 0, len(inv.Scopes))
	var allEntries []fileEntry
	resolvedScopes := make([]executionScope, 0, len(inv.Scopes))
	for _, scope := range inv.Scopes {
		resolvedScopes = append(resolvedScopes, scope.Scope)
		entries := append([]fileEntry(nil), scope.Entries...)
		allEntries = append(allEntries, entries...)
		evaluatedScopes = append(evaluatedScopes, evaluatedOutputScope{
			Paths:   scope.Scope.Paths,
			Entries: entries,
		})
	}
	return buildOutputPlanForResolvedScopes(gitCtx, resolvedScopes, evaluatedScopes, allEntries)
}

func executionScopesUseRecentStage(scopes []executionScope) bool {
	for _, s := range scopes {
		if executionScopeHasStage(s, scopeStageRecent) {
			return true
		}
	}
	return false
}

func executionScopesUsePathsStage(scopes []executionScope) bool {
	for _, s := range scopes {
		if s.Paths || executionScopeHasStage(s, scopeStagePaths) {
			return true
		}
	}
	return false
}

// encodeTreePayloadFromPlan serializes an already-prepared outputPlan as a
// tree payload. For an empty plan with no implicit tree target, returns
// errTreePayloadEmptyNoTarget instead of writing a payload.
func encodeTreePayloadFromPlan(
	w io.Writer,
	cfg renderConfig,
	gitCtx gitContext,
	plan outputPlan,
	notices []string,
	precomputedGitStatuses ...map[string]string,
) error {
	if len(plan.items) == 0 {
		doc, ok := buildEmptyTreeDocument(cfg)
		if !ok {
			return errTreePayloadEmptyNoTarget
		}
		return encodeTreePayload(w, doc)
	}
	report, err := buildOutputReportForPlan(cfg, gitCtx, plan, dedupePreserveOrder(notices), precomputedGitStatuses...)
	if err != nil {
		return err
	}
	return encodeTreePayload(w, buildTreeDocumentFromPreview(cfg, plan, report))
}
