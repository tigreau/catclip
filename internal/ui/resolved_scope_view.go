package ui

import (
	"io"
	"os"
	"strings"
	"sync"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

type resolvedScopeView struct {
	Invocation command.Invocation
	Render     RenderConfig
	GitContext git.Context
	Scopes     []command.ExecutionScope
	ScopeIndex int
	Scope      command.ExecutionScope
	Entries    []discovery.Entry
}

// scopeViewMemo caches the last derived view by exact argv. Rule-20
// clean: the view is a pure function of args over the session's frozen
// filesystem snapshot (the same freeze assumption checkpoints make), so
// identical args ⇒ identical view, and any args change invalidates by
// construction. This is the cross-picker threading from
// RESOLVED_PLAN_cross_picker_scope_view_thread.md Item 4: the target
// picker derives the view once; the modifier menu, the value pickers,
// narrow-confirm, and the checkpoint reload builder all reuse it
// instead of re-running discovery (~144 ms of redundant rg walks per
// frame on Windows). Single entry: picker frames alternate between at
// most one live argv, and a committed modifier changes the key.
var (
	scopeViewMemoMu  sync.Mutex
	scopeViewMemoSet bool
	scopeViewMemoKey string
	scopeViewMemoVal resolvedScopeView
)

func scopeViewMemoLookup(key string) (resolvedScopeView, bool) {
	scopeViewMemoMu.Lock()
	defer scopeViewMemoMu.Unlock()
	if !scopeViewMemoSet || scopeViewMemoKey != key {
		return resolvedScopeView{}, false
	}
	view := scopeViewMemoVal
	// Clone the entry slice so callers may sort/filter their copy
	// without corrupting the memo (several pickers reorder entries).
	view.Entries = append([]discovery.Entry(nil), view.Entries...)
	return view, true
}

func scopeViewMemoStore(key string, view resolvedScopeView) {
	stored := view
	stored.Entries = append([]discovery.Entry(nil), view.Entries...)
	scopeViewMemoMu.Lock()
	scopeViewMemoSet = true
	scopeViewMemoKey = key
	scopeViewMemoVal = stored
	scopeViewMemoMu.Unlock()
}

func resolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, error) {
	// The working directory joins the key: args alone are ambiguous
	// across cwds (a production process never chdirs, but the test
	// binary does, and correctness shouldn't rely on that).
	wd, wdErr := os.Getwd()
	if wdErr != nil {
		wd = ""
	}
	key := wd + "\x00\x00" + strings.Join(args, "\x00")
	if view, ok := scopeViewMemoLookup(key); ok {
		finishBench := platform.InternalBenchSpan("ui.resolved_scope_view.memo_hit",
			"entries", platform.InternalBenchInt(len(view.Entries)),
		)
		finishBench("err", "false")
		return view, nil
	}
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, err
	}
	invocation := resolvedInvocationFromParsedCommand(cfg)
	view, err := resolvedCurrentScopeView(invocation, RenderConfigFromParsedCommand(cfg))
	if err != nil {
		return view, err
	}
	scopeViewMemoStore(key, view)
	return view, nil
}

// ScopeViewForDiscoveryArgs adapts resolvedCurrentScopeViewForArgs into
// the (ScopeView, bool) shape discovery.SetScopeViewResolver expects.
// Registered from Main() so the resolver's fzf checkpoint-preview path
// can drive args -> entries without root needing to import directly.
func ScopeViewForDiscoveryArgs(args []string) (discovery.ScopeView, bool) {
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		return discovery.ScopeView{}, false
	}
	return discovery.ScopeView{
		WorkingDir: view.Invocation.WorkingDir,
		GitContext: view.GitContext,
		Entries:    view.Entries,
		Targets:    view.Scope.Targets,
	}, true
}

func resolvedCurrentScopeView(invocation command.Resolved, renderCfg RenderConfig) (resolvedScopeView, error) {
	if len(invocation.Scopes) == 0 {
		return resolvedScopeView{}, nil
	}

	finishBench := platform.InternalBenchSpan("ui.resolved_scope_view",
		"scopes", platform.InternalBenchInt(len(invocation.Scopes)),
	)
	invocationCfg := invocation.Config
	resolvedScopes := append([]command.ExecutionScope(nil), invocation.Scopes...)
	finishGitBench := platform.InternalBenchSpan("ui.resolved_scope_view.git_detect")
	gitCtx := git.Detect(invocationCfg.WorkingDir)
	finishGitBench("enabled", platform.InternalBenchBool(gitCtx.Enabled))
	scopeIndex := len(resolvedScopes) - 1
	currentScope := resolvedScopes[scopeIndex]
	finishEvalBench := platform.InternalBenchSpan("ui.resolved_scope_view.evaluate_scope",
		"scope_index", platform.InternalBenchInt(scopeIndex),
	)
	discovered, err := discovery.EvaluateScope(invocationCfg, gitCtx, scopeIndex, currentScope, io.Discard, platform.Palette{})
	finishEvalBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(discovered.Entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return resolvedScopeView{}, err
	}
	entries := discovered.Entries

	finishBench(
		"err", "false",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	return resolvedScopeView{
		Invocation: invocationCfg,
		Render:     renderCfg,
		GitContext: gitCtx,
		Scopes:     resolvedScopes,
		ScopeIndex: scopeIndex,
		Scope:      currentScope,
		Entries:    entries,
	}, nil
}

func startupResolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, bool, error) {
	if startupHasUnresolvedScope(args) {
		return resolvedScopeView{}, false, nil
	}
	if _, action, ok := detectStartupTrailingAction(args); ok && action != startupTrailingActionNone {
		return resolvedScopeView{}, false, nil
	}

	// Route through the memoized deriver: this is the target-picker-phase
	// derivation, and storing it here is what lets the modifier menu's
	// identical-args lookup hit — the exact hop the cross-picker plan
	// measured at ~144 ms of redundant rg walks.
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	if len(view.Scopes) == 0 {
		return resolvedScopeView{}, false, nil
	}
	return view, true, nil
}
