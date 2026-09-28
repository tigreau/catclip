package ui

import (
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

// PreparedPresentation belongs to one prepared output plan, not a global memo.
// Reports crossing the API are detached; tree preparation stays private and
// lazy so suppressed trees do not incur row/sort/landmark work.
type PreparedPresentation struct {
	git    git.Context
	plan   output.Plan
	report output.Report
	once   sync.Once
	tree   *renderpkg.PreparedTree
}

func PreparePresentation(gitCtx git.Context, plan output.Plan, report output.Report) *PreparedPresentation {
	return &PreparedPresentation{git: gitCtx, plan: plan, report: report.Clone()}
}

func (p *PreparedPresentation) ReportForPlan(gitCtx git.Context, plan output.Plan, notices []string) (output.Report, bool) {
	if p == nil || p.git != gitCtx || !p.plan.SameInstance(plan) || !slices.Equal(p.report.Notices, notices) {
		return output.Report{}, false
	}
	return p.report.Clone(), true
}

func (cfg RenderConfig) WithPreparedPresentation(p *PreparedPresentation) RenderConfig {
	cfg.prepared = p
	return cfg
}

func (p *PreparedPresentation) matches(plan output.Plan, report output.Report) bool {
	return p != nil && p.plan.SameInstance(plan) &&
		p.report.HumanSize == report.HumanSize && p.report.Tokens == report.Tokens && p.report.CountWord == report.CountWord &&
		slices.Equal(p.report.Notices, report.Notices) && maps.Equal(p.report.Sizes, report.Sizes) &&
		maps.Equal(p.report.Statuses, report.Statuses) && maps.Equal(p.report.ModeTags, report.ModeTags)
}

func printConfiguredPreviewTree(cfg RenderConfig, w io.Writer, plan output.Plan, report output.Report, colors platform.Palette) error {
	p := cfg.prepared
	if !p.matches(plan, report) {
		return printPreviewTree(w, plan, report, colors)
	}
	p.once.Do(func() {
		p.tree = renderpkg.PrepareTree(treeEntriesFromPlan(p.plan, p.report))
	})
	return p.tree.Render(w, treeRenderOptions{ShowModeTags: true, ShowSizes: true, ShowGitStatus: true}, treePalette(colors))
}
