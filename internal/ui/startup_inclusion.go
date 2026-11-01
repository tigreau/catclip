package ui

import (
	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

var startupInclusionChoices = []StartupModifierChoice{
	{Key: "with-binaries", Label: "-b, --with-binaries", Description: "Include binary files (images, PDFs, etc.)", Args: []string{"--with-binaries"}},
	{Key: "no-ignore", Label: "--no-ignore", Description: "Include files hidden by ignore rules", Args: []string{"--no-ignore"}},
}

func startupAvailableInclusionChoices(args []string) []StartupModifierChoice {
	var choices []StartupModifierChoice
	for _, choice := range startupInclusionChoices {
		if cli.ValidateCurrentScopeFlagSequence(args, choice.Args) == nil {
			choices = append(choices, choice)
		}
	}
	return choices
}

func chooseStartupInclusion(args []string, escHint string) ([]string, error) {
	return chooseStartupMultipleOptions(startupAvailableInclusionChoices(args), "include> ", discovery.PickerHeader(
		"Choose which files the target picker includes.",
		"Mark one or both options.",
		"Enter reopens target selection.",
		"[Enter] confirm  [Tab] mark  [Esc] back"))
}

// Only a target-generation boundary may change inclusion. Keep previous scopes
// and global presentation flags, replace this scope's target selection, then
// return to its modifier menu. Esc restores the untouched previous frame.
func startupInclusionTargetFrame(frame startupInteractiveFrame, flags []string) startupInteractiveFrame {
	start := 0
	for i := 0; i < len(frame.StartArgs); i++ {
		if frame.StartArgs[i] == "--then" {
			start = i + 1
		}
		i += cli.FixedValueCount(frame.StartArgs[i])
	}
	prefix := cloneStringSlice(frame.StartArgs[:start])
	var inclusion []string
	for _, arg := range frame.StartArgs[start:] {
		switch arg {
		case "--no-ignore", "--with-binaries", "-b":
			inclusion = append(inclusion, arg)
		default:
			if len(arg) > 0 && arg[0] == '-' {
				prefix = append(prefix, arg)
			}
		}
	}
	inclusion = append(inclusion, flags...)
	pending := append(inclusion, "--")
	pending = append(pending, frame.PendingArgs...)
	return startupInteractiveFrame{Kind: startupInteractiveFrameTarget, StartArgs: prefix, PendingArgs: pending, TargetPrompt: startupFrameTargetPrompt(prefix)}
}

func startupTargetFrameUsesBinaries(frame startupInteractiveFrame) bool {
	args := append(cloneStringSlice(frame.StartArgs), frame.TargetTokens...)
	at := len(args)
	args = append(args, frame.PendingArgs...)
	value := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--then":
			if i >= at {
				return value
			}
			value = false
		case "--with-binaries", "-b":
			value = true
		}
		i += cli.FixedValueCount(args[i])
	}
	return value
}

func inclusionOnlyScope(s command.ExecutionScope) bool {
	for _, stage := range s.Stages {
		if stage.Kind != command.StageNoIgnore && stage.Kind != command.StageWithBinaries {
			return false
		}
	}
	return true
}

func startupInclusionGlobalFlag(arg string) bool {
	switch arg {
	case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "--no", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--metadata", "--headless":
		return true
	}
	return false
}
