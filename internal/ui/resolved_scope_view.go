package ui

import (
	"io"

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

func resolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, error) {
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, err
	}
	invocation := resolvedInvocationFromParsedCommand(cfg)
	return resolvedCurrentScopeView(invocation, RenderConfigFromParsedCommand(cfg))
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

	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	view, err := resolvedCurrentScopeView(resolvedInvocationFromParsedCommand(cfg), RenderConfigFromParsedCommand(cfg))
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	if len(view.Scopes) == 0 {
		return resolvedScopeView{}, false, nil
	}
	return view, true, nil
}
