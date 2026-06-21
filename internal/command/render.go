package command

import (
	"strconv"
	"strings"
)

// Canonical command rendering — turning the resolved command model back
// into argv. Used to print "Resolved command:" headers, to compose
// suggestion strings inside validation errors, and to build the
// `--internal-prediscovered`-style sub-invocations the picker chains
// rely on.
//
// Invariant: the rendered command must reproduce the executed run. Any
// new StageKind (or any new execution-affecting flag on Invocation /
// RenderFlags) needs a case in CanonicalScopeArgs or a branch in
// CanonicalGlobalArgs. TestCanonicalScopeArgsCoversAllStageKinds fails
// if a flag from scopeModifierFlagSpecs has no case here.
//
// These functions previously lived at root in command_render.go; they
// were moved here in the v0.6.0 cli/ extraction so canonical rendering
// can stay command-local. The parser-side adapter
// (cli.FormatResolvedStartupCommand) lives in internal/cli/parse.go.

// CanonicalResolvedInvocationCommand renders a Resolved into a single
// shell-pasteable command string. The leading "catclip" word and any
// --then separators are inserted between scopes.
func CanonicalResolvedInvocationCommand(invocation Resolved, flags RenderFlags) string {
	globalArgs := CanonicalGlobalArgs(invocation.Config, flags)
	parts := make([]string, 0, len(globalArgs)+len(invocation.Scopes)*4+1)
	parts = append(parts, "catclip")
	for _, arg := range globalArgs {
		parts = append(parts, shellQuoteArg(arg))
	}
	for i, s := range invocation.Scopes {
		if i > 0 {
			parts = append(parts, "--then")
		}
		parts = append(parts, CanonicalScopeArgs(s)...)
	}
	return strings.Join(parts, " ")
}

// CanonicalGlobalArgs renders the invocation-wide flags (--verbose,
// --quiet, --print, --raw, --no-tree, --no-bundle, --preview,
// --headless, --with-binaries) that prefix every canonical command.
func CanonicalGlobalArgs(invocationCfg Invocation, flags RenderFlags) []string {
	out := make([]string, 0, 10)
	if invocationCfg.Verbose {
		out = append(out, "--verbose")
	}
	if invocationCfg.Quiet && !invocationCfg.Headless {
		out = append(out, "--quiet")
	}
	if flags.Yes {
		out = append(out, "--yes")
	}
	if flags.OutputMode == OutputModeStdout && !invocationCfg.Headless {
		out = append(out, "--print")
	}
	if flags.Raw {
		out = append(out, "--raw")
	}
	if flags.NoTree {
		out = append(out, "--no-tree")
	}
	if flags.NoBundle {
		out = append(out, "--no-bundle")
	}
	if flags.Preview {
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

// CanonicalScopeArgs renders an ExecutionScope back into the argv form
// that produces it. Used to build the "Resolved command:" header.
//
// Invariant: the resolved command must equal the executed command.
// Anything catclip applies to a run must be representable here, and
// copy/pasting the rendered command must produce the same output.
// When adding a new StageKind (or execution-affecting field on
// ExecutionScope), add a case below — TestCanonicalScopeArgsCoversAllStageKinds
// fails if a flag from scopeModifierFlagSpecs has no case here.
func CanonicalScopeArgs(s ExecutionScope) []string {
	parts := make([]string, 0, len(s.Targets)+len(s.Stages)*2)
	for _, target := range s.Targets {
		parts = append(parts, shellQuoteArg(target))
	}
	for _, stage := range s.Stages {
		switch stage.Kind {
		case StageInclude:
			parts = append(parts, "--include")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case StageOnly:
			parts = append(parts, "--only")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case StageExclude:
			parts = append(parts, "--exclude")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case StageRecent:
			parts = append(parts, "--recent")
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case StageSize:
			parts = append(parts, "--size")
			for _, n := range stage.Nums {
				parts = append(parts, shellQuoteArg(strconv.Itoa(n)))
			}
		case StageDepth:
			parts = append(parts, "--depth")
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case StageContains:
			parts = append(parts, "--contains")
			for _, value := range stage.Values {
				parts = append(parts, shellEnforceSingleQuote(value))
			}
		case StagePaths:
			parts = append(parts, "--paths")
		case StageSnippet:
			parts = append(parts, "--snippet")
			for _, value := range stage.Values {
				parts = append(parts, shellEnforceSingleQuote(value))
			}
			if s.SnippetContextSet {
				parts = append(parts, strconv.Itoa(s.SnippetContextLines))
			}
		case StageLines:
			parts = append(parts, "--lines")
			if s.LinesStart > 0 {
				parts = append(parts, shellQuoteArg(strconv.Itoa(s.LinesStart)))
				if s.LinesEnd > 0 {
					parts = append(parts, shellQuoteArg(strconv.Itoa(s.LinesEnd)))
				}
			}
		case StageChanged:
			parts = append(parts, "--changed")
		case StageStaged:
			parts = append(parts, "--staged")
		case StageUnstaged:
			parts = append(parts, "--unstaged")
		case StageUntracked:
			parts = append(parts, "--untracked")
		case StageDiff:
			parts = append(parts, "--diff")
		case StageChangedDiff:
			parts = append(parts, "--changed-diff")
		case StageStagedDiff:
			parts = append(parts, "--staged-diff")
		case StageUnstagedDiff:
			parts = append(parts, "--unstaged-diff")
		}
	}
	return parts
}
