package catclip

import (
	"io"
	"time"
)

type ScopeDiscoveryStat struct {
	Index    int
	Count    int
	Duration time.Duration
}

type DiscoveryResult struct {
	Invocation         discoveredInvocation
	Diagnostics        []diagnostic
	Notices            []string
	HadSelectionCancel bool
	ScopeStats         []ScopeDiscoveryStat
}

func discoverInvocation(inv resolvedInvocation, gitCtx gitContext, stderr io.Writer, colors colorPalette) (DiscoveryResult, error) {
	result := DiscoveryResult{
		Invocation: discoveredInvocation{
			Config: inv.Config,
			Scopes: make([]discoveredScope, 0, len(inv.Scopes)),
		},
		ScopeStats: make([]ScopeDiscoveryStat, 0, len(inv.Scopes)),
	}
	for i, scope := range inv.Scopes {
		started := time.Now()
		discovered, err := evaluateScope(inv.Config, gitCtx, i, scope, stderr, colors)
		if err != nil {
			return DiscoveryResult{}, err
		}
		result.Invocation.Scopes = append(result.Invocation.Scopes, discovered)
		result.Diagnostics = append(result.Diagnostics, discovered.Diagnostics...)
		result.Notices = append(result.Notices, discovered.Notices...)
		result.HadSelectionCancel = result.HadSelectionCancel || discovered.SelectionCancel
		result.ScopeStats = append(result.ScopeStats, ScopeDiscoveryStat{
			Index:    i,
			Count:    len(discovered.Entries),
			Duration: time.Since(started),
		})
	}
	return result, nil
}
