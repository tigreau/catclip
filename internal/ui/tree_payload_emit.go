package ui

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

// ErrTreePayloadEmptyNoTarget signals that the caller asked for a tree payload
// but the plan was empty and no implicit tree target was available, so there is
// nothing to encode. The CLI surfaces a "no files matched" message for this
// case; pickers ignore it (they should never hit it because their caller has
// already validated the scope).
var ErrTreePayloadEmptyNoTarget = errors.New("tree payload: empty plan and no implicit target")

// EncodeTreePayloadFromPlan serializes an already-prepared output.Plan as a
// tree payload. For an empty plan with no implicit tree target, returns
// ErrTreePayloadEmptyNoTarget instead of writing a payload.
func EncodeTreePayloadFromPlan(
	w io.Writer,
	cfg RenderConfig,
	gitCtx git.Context,
	plan output.Plan,
	notices []string,
	precomputedGitStatuses ...map[string]string,
) error {
	if plan.IsEmpty() {
		doc, ok := buildEmptyTreeDocument(cfg)
		if !ok {
			return ErrTreePayloadEmptyNoTarget
		}
		return encodeTreePayload(w, doc)
	}
	report, err := output.BuildReportForPlan(gitCtx, plan, output.ReportOptions{
		IncludeTreeMetadata:    NeedsTreeRender(cfg),
		Notices:                discovery.DedupePreserveOrder(notices),
		PrecomputedGitStatuses: firstPrecomputedGitStatusMap(precomputedGitStatuses),
	})
	if err != nil {
		return err
	}
	return encodeTreePayload(w, buildTreeDocumentFromPreview(cfg, plan, report))
}

// firstPrecomputedGitStatusMap collapses the variadic
// PrecomputedGitStatuses argument (kept for backward compat at root
// callers' EncodeTreePayloadFromPlan / RenderTreePreviewFromPlan
// signatures) into a single optional map. The output package's
// output.ReportOptions takes one optional map directly.
func firstPrecomputedGitStatusMap(statuses []map[string]string) map[string]string {
	if len(statuses) == 0 {
		return nil
	}
	return statuses[0]
}

func RenderTreePreviewFromPlan(
	w io.Writer,
	cfg RenderConfig,
	gitCtx git.Context,
	plan output.Plan,
	notices []string,
	opts treeRenderOptions,
	precomputedGitStatuses ...map[string]string,
) error {
	renderCfg := TreeDocumentRenderConfig(cfg)
	if plan.IsEmpty() {
		doc, ok := buildEmptyTreeDocument(renderCfg)
		if !ok {
			return ErrTreePayloadEmptyNoTarget
		}
		return renderTreeDocument(w, doc, opts, platform.ANSIPalette())
	}
	report, err := output.BuildReportForPlan(gitCtx, plan, output.ReportOptions{
		IncludeTreeMetadata:    NeedsTreeRender(renderCfg),
		Notices:                discovery.DedupePreserveOrder(notices),
		PrecomputedGitStatuses: firstPrecomputedGitStatusMap(precomputedGitStatuses),
	})
	if err != nil {
		return err
	}
	return renderTreeDocument(w, buildTreeDocumentFromPreview(renderCfg, plan, report), opts, platform.ANSIPalette())
}

func RunInternalTreePayloadFilePreview(inputDir, inputStem string, stdout io.Writer) error {
	f, err := os.Open(filepath.Join(inputDir, inputStem+".json"))
	if err != nil {
		return err
	}
	defer f.Close()
	doc, err := decodeTreePayload(f)
	if err != nil {
		return err
	}
	return renderTreeDocument(stdout, doc, FzfFilterTreeRenderOptions(), platform.ANSIPalette())
}
