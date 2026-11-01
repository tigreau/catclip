package ui

import (
	"io"
	"time"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func (r *MetadataReport) WriteMetadata(w io.Writer, colors platform.Palette) (int64, error) {
	return WriteMetadataReportWithPalette(w, r, colors)
}

// PrepareScopeMetadata reuses the union's Git status and the existing metadata
// builder. Only metadata scopes contribute rows/ignore summaries; source scopes
// retain their independently prepared content. No full text is buffered here.
func PrepareScopeMetadata(wd string, gitCtx git.Context, scopes []discovery.Scope, plan output.Plan, report output.Report) (output.Plan, output.Report, error) {
	if plan.MetadataPrepared() {
		return plan, report, nil
	}
	now := time.Now()
	prepared, err := plan.PrepareMetadata(func(indices []int, section output.Plan) (output.MetadataPayload, error) {
		selected := make([]discovery.Scope, 0, len(indices))
		for _, i := range indices {
			selected = append(selected, scopes[i])
		}
		return BuildMetadataReport(wd, gitCtx, selected, section, report, false, now)
	})
	if err != nil {
		return output.Plan{}, output.Report{}, err
	}
	// Recompute summary bytes from the prepared section sizes without a second
	// Git query. Even a nil status map is an explicitly completed observation.
	statuses := report.Statuses
	if statuses == nil {
		statuses = map[string]string{}
	}
	updated, err := output.BuildReportForPlan(gitCtx, prepared, output.ReportOptions{
		IncludeTreeMetadata: true, PrecomputedGitStatuses: statuses, Notices: report.Notices,
	})
	return prepared, updated, err
}
