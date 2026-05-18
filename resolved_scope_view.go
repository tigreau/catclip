package catclip

import "io"

type resolvedScopeView struct {
	Invocation invocationConfig
	Render     renderConfig
	GitContext gitContext
	Scopes     []executionScope
	ScopeIndex int
	Scope      executionScope
	Entries    []fileEntry
}

func resolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, error) {
	cfg, err := parseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, err
	}
	invocation := resolvedInvocationFromParsedCommand(cfg)
	return resolvedCurrentScopeView(invocation, renderConfigFromParsedCommand(cfg))
}

func resolvedCurrentScopeView(invocation resolvedInvocation, renderCfg renderConfig) (resolvedScopeView, error) {
	if len(invocation.Scopes) == 0 {
		return resolvedScopeView{}, nil
	}

	invocationCfg := invocation.Config
	resolvedScopes := append([]executionScope(nil), invocation.Scopes...)
	gitCtx := detectGitContext(invocationCfg.WorkingDir)
	scopeIndex := len(resolvedScopes) - 1
	currentScope := resolvedScopes[scopeIndex]
	discovered, err := evaluateScope(invocationCfg, gitCtx, scopeIndex, currentScope, io.Discard, colorPalette{})
	if err != nil {
		return resolvedScopeView{}, err
	}
	entries := discovered.Entries

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

	cfg, err := parseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	view, err := resolvedCurrentScopeView(resolvedInvocationFromParsedCommand(cfg), renderConfigFromParsedCommand(cfg))
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	if len(view.Scopes) == 0 {
		return resolvedScopeView{}, false, nil
	}
	return view, true, nil
}
