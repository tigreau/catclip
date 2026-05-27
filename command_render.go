package catclip

import (
	"strconv"
	"strings"
)

// Canonical command rendering — turning the resolved/parsed command model
// back into argv. Used to print "Resolved command:" headers, to compose
// suggestion strings inside validation errors, and to build the
// `--internal-prediscovered`-style sub-invocations the picker chains rely
// on.
//
// Invariant: the rendered command must reproduce the executed run. Any
// new scopeStageKind (or any new execution-affecting flag on
// invocationConfig / emitConfig) needs a case in canonicalScopeArgs or a
// branch in canonicalGlobalArgsFromConfig. TestCanonicalScopeArgsCoversAllStageKinds
// fails if a flag from scopeModifierFlagSpecs has no case below.
//
// These functions previously lived in main.go; they were moved here in
// the v0.5.2 "tighten command model" pass so the rendering responsibility
// has a discoverable home next to command_spec.go.

func formatResolvedStartupCommand(args []string) string {
	if cfg, err := parseArgsAllowImplicitDot(args); err == nil {
		return formatCanonicalResolvedInvocationCommand(
			resolvedInvocationFromParsedCommand(cfg),
			emitConfigFromParsedCommand(cfg),
			cfg.Yes,
			cfg.NoTree,
			cfg.Preview,
		)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "catclip")
	for _, arg := range args {
		parts = append(parts, shellQuoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func formatCanonicalResolvedInvocationCommand(invocation resolvedInvocation, emitCfg emitConfig, yes, noTree, preview bool) string {
	globalArgs := canonicalGlobalArgsFromConfig(invocation.Config, emitCfg, yes, noTree, preview)
	parts := make([]string, 0, len(globalArgs)+len(invocation.Scopes)*4+1)
	parts = append(parts, "catclip")
	for _, arg := range globalArgs {
		parts = append(parts, shellQuoteArg(arg))
	}
	for i, s := range invocation.Scopes {
		if i > 0 {
			parts = append(parts, "--then")
		}
		parts = append(parts, canonicalScopeArgs(s)...)
	}
	return strings.Join(parts, " ")
}

func canonicalGlobalArgsFromConfig(invocationCfg invocationConfig, emitCfg emitConfig, yes, noTree, preview bool) []string {
	out := make([]string, 0, 10)
	if invocationCfg.Verbose {
		out = append(out, "--verbose")
	}
	if invocationCfg.Quiet && !invocationCfg.Headless {
		out = append(out, "--quiet")
	}
	if yes {
		out = append(out, "--yes")
	}
	if emitCfg.OutputMode == outputModeStdout && !invocationCfg.Headless {
		out = append(out, "--print")
	}
	if emitCfg.Raw {
		out = append(out, "--raw")
	}
	if noTree {
		out = append(out, "--no-tree")
	}
	if emitCfg.NoBundle {
		out = append(out, "--no-bundle")
	}
	if preview {
		out = append(out, "--preview")
	}
	if invocationCfg.Headless {
		out = append(out, "--headless")
	}
	if invocationCfg.WithBinaries {
		out = append(out, "--with-binaries")
	}
	return out
}

// canonicalScopeArgs renders an executionScope back into the argv form
// that produces it. Used to build the "Resolved command:" header.
//
// Invariant: the resolved command must equal the executed command.
// Anything catclip applies to a run must be representable here, and
// copy/pasting the rendered command must produce the same output.
// When adding a new scopeStageKind (or execution-affecting field on
// executionScope), add a case below — TestCanonicalScopeArgsCoversAllStageKinds
// fails if a flag from scopeModifierFlagSpecs has no case here.
func canonicalScopeArgs(s executionScope) []string {
	parts := make([]string, 0, len(s.Targets)+len(s.Stages)*2)
	for _, target := range s.Targets {
		parts = append(parts, shellQuoteArg(target))
	}
	for _, stage := range s.Stages {
		switch stage.Kind {
		case scopeStageInclude:
			parts = append(parts, "--include")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageOnly:
			parts = append(parts, "--only")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageExclude:
			parts = append(parts, "--exclude")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageRecent:
			parts = append(parts, "--recent")
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case scopeStageDepth:
			parts = append(parts, "--depth")
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case scopeStageContains:
			parts = append(parts, "--contains")
			for _, value := range stage.Values {
				parts = append(parts, shellEnforceSingleQuote(value))
			}
		case scopeStagePaths:
			parts = append(parts, "--paths")
		case scopeStageSnippet:
			parts = append(parts, "--snippet")
			for _, value := range stage.Values {
				parts = append(parts, shellEnforceSingleQuote(value))
			}
			if s.SnippetContextSet {
				parts = append(parts, strconv.Itoa(s.SnippetContextLines))
			}
		case scopeStageLines:
			parts = append(parts, "--lines")
			if s.LinesStart > 0 {
				parts = append(parts, shellQuoteArg(strconv.Itoa(s.LinesStart)))
				if s.LinesEnd > 0 {
					parts = append(parts, shellQuoteArg(strconv.Itoa(s.LinesEnd)))
				}
			}
		case scopeStageChanged:
			parts = append(parts, "--changed")
		case scopeStageStaged:
			parts = append(parts, "--staged")
		case scopeStageUnstaged:
			parts = append(parts, "--unstaged")
		case scopeStageUntracked:
			parts = append(parts, "--untracked")
		case scopeStageDiff:
			parts = append(parts, "--diff")
		case scopeStageChangedDiff:
			parts = append(parts, "--changed-diff")
		case scopeStageStagedDiff:
			parts = append(parts, "--staged-diff")
		case scopeStageUnstagedDiff:
			parts = append(parts, "--unstaged-diff")
		}
	}
	return parts
}
