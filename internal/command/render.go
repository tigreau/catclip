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
// new StageKind needs an entry in stageFlags (stage_info.go) plus a
// payload case in CanonicalScopeArgs if it carries values; any new
// execution-affecting flag on Invocation / RenderFlags needs a branch
// in CanonicalGlobalArgs. TestCanonicalScopeArgsCoversAllStageKinds
// fails if a flag from scopeModifierFlagSpecs doesn't round-trip.
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
// When adding a new StageKind, register it in stageFlags
// (stage_info.go); add a payload case below only if the stage carries
// values — TestCanonicalScopeArgsCoversAllStageKinds fails if a flag
// from scopeModifierFlagSpecs doesn't round-trip.
func CanonicalScopeArgs(s ExecutionScope) []string {
	parts := make([]string, 0, len(s.Targets)+len(s.Stages)*2)
	for _, target := range s.Targets {
		parts = append(parts, shellQuoteArg(target))
	}
	for _, stage := range s.Stages {
		flag, ok := StageFlag(stage.Kind)
		if !ok {
			// Unknown kinds were silently skipped by the old switch
			// too; the cli spec builder and the totality tests make
			// this unreachable for declared kinds.
			continue
		}
		parts = append(parts, flag)
		switch stage.Kind {
		case StageInclude, StageOnly, StageExclude:
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case StageRecent, StageDepth:
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case StageSize:
			for _, n := range stage.Nums {
				parts = append(parts, shellQuoteArg(strconv.Itoa(n)))
			}
		case StageContains, StageNotContains:
			for _, value := range stage.Values {
				parts = append(parts, shellEnforceSingleQuote(value))
			}
		case StageSnippet:
			for _, value := range stage.Values {
				parts = append(parts, shellEnforceSingleQuote(value))
			}
			if s.SnippetContextSet {
				parts = append(parts, strconv.Itoa(s.SnippetContextLines))
			}
		case StageLines:
			if s.LinesStart > 0 {
				parts = append(parts, shellQuoteArg(strconv.Itoa(s.LinesStart)))
				if s.LinesEnd > 0 {
					parts = append(parts, shellQuoteArg(strconv.Itoa(s.LinesEnd)))
				}
			}
		}
	}
	return parts
}
