package output

import (
	"fmt"

	"github.com/tigreau/catclip/internal/git"
)

// ReportOptions is the input shape for BuildReportForPlan. Replaces
// the renderConfig-bound signature so output never imports render.
// Root callers compute IncludeTreeMetadata from needsTreeRender at
// the call site and pass it; PrecomputedGitStatuses is an optional
// callsite-supplied status map (e.g. when the caller already collected
// statuses for its own preview pane and wants the report to reuse them
// instead of re-running git status).
type ReportOptions struct {
	IncludeTreeMetadata    bool
	Notices                []string
	PrecomputedGitStatuses map[string]string
}

// BuildReportForPlan aggregates the per-path mode tags, git statuses,
// and size/token summary for a plan. Used by preview, tree-payload
// emission, and the startup sink picker. Was root preview.go's
// buildOutputReportForPlan before the v0.6.0 output extraction.
func BuildReportForPlan(gitCtx git.Context, plan Plan, opts ReportOptions) (Report, error) {
	sizes, totalBytes := plan.PayloadSizes()
	count, word := plan.SummaryCountWord()
	report := Report{
		Sizes:   sizes,
		Notices: append([]string(nil), opts.Notices...),
	}

	if opts.IncludeTreeMetadata {
		if opts.PrecomputedGitStatuses != nil {
			report.Statuses = cloneStringMap(opts.PrecomputedGitStatuses)
		} else if gitCtx.Enabled {
			statuses, err := git.StatusMapForPathspecs(gitCtx, plan.GitStatusPathspecs(gitCtx))
			if err != nil {
				return Report{}, err
			}
			report.Statuses = statuses
		}
		report.ModeTags = plan.PreviewModeTags(report.Statuses)
	}

	report.HumanSize, report.Tokens = formatSizeAndTokens(totalBytes, count)
	report.CountWord = word
	return report, nil
}

// formatSizeAndTokens duplicates internal/render/model.go's
// FormatSizeAndTokens. Local so output never imports render. The math
// is stable — sync if either side changes (no churn expected).
func formatSizeAndTokens(totalBytes int64, fileCount int) (string, int64) {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	_ = fileCount
	tokens := totalBytes / 4

	switch {
	case totalBytes < kb:
		return formatSizeFloat(float64(totalBytes), "B"), tokens
	case totalBytes < mb:
		return formatSizeFloat(float64(totalBytes)/kb, "KB"), tokens
	case totalBytes < gb:
		return formatSizeFloat(float64(totalBytes)/mb, "MB"), tokens
	default:
		return formatSizeFloat(float64(totalBytes)/gb, "GB"), tokens
	}
}

func formatSizeFloat(value float64, unit string) string {
	return fmt.Sprintf("%.2f%s", value, unit)
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
