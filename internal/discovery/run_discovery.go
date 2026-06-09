package discovery

import (
	"io"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

type ScopeStat struct {
	Index    int
	Count    int
	Duration time.Duration
}

type Result struct {
	Invocation         Discovered
	Diagnostics        []Diagnostic
	Notices            []string
	HadSelectionCancel bool
	ScopeStats         []ScopeStat
}

func DiscoverInvocation(inv command.Resolved, gitCtx git.Context, stderr io.Writer, colors platform.Palette) (Result, error) {
	result := Result{
		Invocation: Discovered{
			Config: inv.Config,
			Scopes: make([]Scope, 0, len(inv.Scopes)),
		},
		ScopeStats: make([]ScopeStat, 0, len(inv.Scopes)),
	}
	for i, scope := range inv.Scopes {
		started := time.Now()
		discovered, err := EvaluateScope(inv.Config, gitCtx, i, scope, stderr, colors)
		if err != nil {
			return Result{}, err
		}
		result.Invocation.Scopes = append(result.Invocation.Scopes, discovered)
		result.Diagnostics = append(result.Diagnostics, discovered.Diagnostics...)
		result.Notices = append(result.Notices, discovered.Notices...)
		result.HadSelectionCancel = result.HadSelectionCancel || discovered.SelectionCancel
		result.ScopeStats = append(result.ScopeStats, ScopeStat{
			Index:    i,
			Count:    len(discovered.Entries),
			Duration: time.Since(started),
		})
	}
	return result, nil
}
