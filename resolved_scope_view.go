package catclip

import "io"

type resolvedScopeView struct {
	Config     runConfig
	GitContext gitContext
	ScopeIndex int
	Scope      executionScope
	Entries    []fileEntry
}

func resolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, error) {
	cfg, err := parseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, err
	}
	return resolvedCurrentScopeViewForConfig(cfg)
}

func resolvedCurrentScopeViewForConfig(cfg runConfig) (resolvedScopeView, error) {
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return resolvedScopeView{}, nil
	}

	gitCtx := detectGitContext(cfg.WorkingDir)
	scopeIndex := len(scopeSpecs) - 1
	currentScope := executionScopeFromCommandScopeSpec(scopeSpecs[scopeIndex])
	discovered, err := evaluateScope(cfg, gitCtx, scopeIndex, currentScope, io.Discard, colorPalette{})
	if err != nil {
		return resolvedScopeView{}, err
	}
	entries := discovered.Entries

	return resolvedScopeView{
		Config:     cfg,
		GitContext: gitCtx,
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
	view, err := resolvedCurrentScopeViewForConfig(cfg)
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	if len(configCommandScopes(view.Config)) == 0 {
		return resolvedScopeView{}, false, nil
	}
	return view, true, nil
}
