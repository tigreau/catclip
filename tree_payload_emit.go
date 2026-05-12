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

// buildOutputPlanForCfg picks between sectioned and non-sectioned plan
// preparation using the same logic as cli.go. Returns the prepared plan.
// allEntries is the union of per-scope entries (already accumulated by the
// caller) and is used only when the cfg's command scopes do not require paths
// sectioning.
func buildOutputPlanForCfg(
	cfg runConfig,
	gitCtx gitContext,
	evaluatedScopes []evaluatedOutputScope,
	allEntries []fileEntry,
) (outputPlan, error) {
	commandScopes := configCommandScopes(cfg)
	if commandScopesUsePathsStage(commandScopes) {
		return prepareSectionedOutputPlan(gitCtx, evaluatedScopes, commandScopesUseRecentStage(commandScopes))
	}
	entries := allEntries
	if commandScopesUseRecentStage(commandScopes) {
		entries = dedupeEntriesByPathPreserveOrder(entries)
	} else {
		entries = dedupeEntriesByPath(entries)
	}
	return prepareOutputPlan(gitCtx, entries)
}

// encodeTreePayloadFromEntries runs the doc-building + encoding pipeline that
// the top-level CLI uses for `--internal-tree-payload`, with no diagnostic
// printing and no exit-code selection. It is the rule-19 entry point for any
// caller that has resolved entries in-process and wants to serialize a tree
// payload to a writer (file, buffer, network, etc.) without spawning a catclip
// subprocess.
//
// For an empty plan with no implicit tree target, returns
// errTreePayloadEmptyNoTarget instead of writing a payload. Callers that need
// the "no files matched" CLI behavior handle that sentinel; pickers treat it
// as a normal failure to render (no payload written).
func encodeTreePayloadFromEntries(
	w io.Writer,
	cfg runConfig,
	gitCtx gitContext,
	evaluatedScopes []evaluatedOutputScope,
	allEntries []fileEntry,
	notices []string,
) error {
	plan, err := buildOutputPlanForCfg(cfg, gitCtx, evaluatedScopes, allEntries)
	if err != nil {
		return err
	}
	return encodeTreePayloadFromPlan(w, cfg, gitCtx, plan, notices)
}

// encodeTreePayloadFromPlan is the inner half of encodeTreePayloadFromEntries:
// given an already-prepared outputPlan, build the tree document and write the
// encoded payload. Exposed separately because cli.go has already-prepared
// plans on hand and can avoid re-running plan preparation.
func encodeTreePayloadFromPlan(
	w io.Writer,
	cfg runConfig,
	gitCtx gitContext,
	plan outputPlan,
	notices []string,
) error {
	if len(plan.items) == 0 {
		doc, ok := buildEmptyTreeDocument(cfg)
		if !ok {
			return errTreePayloadEmptyNoTarget
		}
		return encodeTreePayload(w, doc)
	}
	report, err := buildOutputReportForPlan(cfg, gitCtx, plan, dedupePreserveOrder(notices))
	if err != nil {
		return err
	}
	return encodeTreePayload(w, buildTreeDocumentFromPreview(cfg, plan, report))
}
