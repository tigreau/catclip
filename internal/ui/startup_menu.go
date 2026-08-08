package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

var startupModifierMetaChoices = []StartupModifierChoice{
	// Input order is bottom-to-top under fzf's default layout. These rows are
	// appended after the filter choices so they appear above --then on screen:
	// [finish early], [extras], then the existing filter list.
	{
		Key:         "extras",
		Label:       "[extras]",
		Description: "raw/quiet/binaries/etc.",
		Mode:        startupModifierModeExtras,
	},
	{
		Key:         "finish",
		Label:       "[finish early]",
		Description: "skip remaining -- menus and choose output",
		Mode:        startupModifierModeFinish,
	},
}

var startupModifierChoices = []StartupModifierChoice{
	{
		Key:         "only",
		Label:       "--only",
		Description: "Keep only matching files",
		Args:        []string{"--only"},
		Mode:        startupModifierModeOnly,
	},
	{
		Key:         "exclude",
		Label:       "--exclude",
		Description: "Remove matching files",
		Args:        []string{"--exclude"},
		Mode:        startupModifierModeExclude,
	},
	{
		Key:         "recent",
		Label:       "--recent",
		Description: "Keep the most recently modified files",
		Args:        []string{"--recent"},
		Mode:        startupModifierModeRecent,
	},
	{
		Key:         "size",
		Label:       "--size",
		Description: "Sort or filter files by size",
		Args:        []string{"--size"},
		Mode:        startupModifierModeSize,
	},
	{
		Key:         "depth",
		Label:       "--depth",
		Description: "Keep only files up to a path depth",
		Args:        []string{"--depth"},
		Mode:        startupModifierModeDepth,
	},
	{
		Key:         "paths",
		Label:       "--paths",
		Description: "Emit bare relative paths for this scope",
		Args:        []string{"--paths"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "contains",
		Label:       "--contains",
		Description: "Keep files whose contents match a regex",
		Args:        []string{"--contains"},
		Mode:        startupModifierModeContains,
	},
	{
		Key:         "not-contains",
		Label:       "--not-contains",
		Description: "Drop files whose contents match a regex",
		Args:        []string{"--not-contains"},
		Mode:        startupModifierModeNotContains,
	},
	{
		Key:         "snippet",
		Label:       "--snippet",
		Description: "Keep matching regex snippets from file contents",
		Args:        []string{"--snippet"},
		Mode:        startupModifierModeSnippet,
	},
	{
		Key:         "lines",
		Label:       "--lines",
		Description: "Slice files by line range",
		Args:        []string{"--lines"},
		Mode:        startupModifierModeLines,
	},
	{
		Key:         "include",
		Label:       "--include",
		Description: "Allow ignored files or folders",
		Args:        []string{"--include"},
		Mode:        startupModifierModeInclude,
	},
	{
		Key:         "changed",
		Label:       "--changed",
		Description: "Keep only git-changed files",
		Args:        []string{"--changed"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "staged",
		Label:       "--staged",
		Description: "Keep only staged git files",
		Args:        []string{"--staged"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "unstaged",
		Label:       "--unstaged",
		Description: "Keep only unstaged git files",
		Args:        []string{"--unstaged"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "untracked",
		Label:       "--untracked",
		Description: "Keep only untracked git files",
		Args:        []string{"--untracked"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "changed-diff",
		Label:       "--changed-diff",
		Description: "Show patches for git-changed files",
		Args:        []string{"--changed-diff"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "staged-diff",
		Label:       "--staged-diff",
		Description: "Show patches for staged git files",
		Args:        []string{"--staged-diff"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "unstaged-diff",
		Label:       "--unstaged-diff",
		Description: "Show patches for unstaged git files",
		Args:        []string{"--unstaged-diff"},
		Mode:        startupModifierModeGit,
	},
	// Keep --then last in this slice so it appears at the top of the default
	// bottom-stacked filter menu. It teaches scope chaining and should stay
	// above newly added rows in the on-screen order.
	{
		Key:         "then",
		Label:       "--then",
		Description: "Chain a new scope with its own targets and filters",
		Args:        []string{"--then"},
		Mode:        startupModifierModeThen,
	},
}

var startupExtrasChoices = []StartupModifierChoice{
	// Input order is bottom-to-top under fzf's default layout, so this slice is
	// the reverse of the visual extras> menu documented in the plan.
	{
		Key:         "verbose",
		Label:       "--verbose",
		Description: "debug timings",
		Args:        []string{"--verbose"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "quiet",
		Label:       "--quiet",
		Description: "suppress prompts/decorations",
		Args:        []string{"--quiet"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "yes",
		Label:       "--yes",
		Description: "skip confirmation",
		Args:        []string{"--yes"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "no-tree",
		Label:       "--no-tree",
		Description: "skip tree preview",
		Args:        []string{"--no-tree"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "with-binaries",
		Label:       "--with-binaries",
		Description: "include binary files",
		Args:        []string{"--with-binaries"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "raw",
		Label:       "--raw",
		Description: "bare file bodies",
		Args:        []string{"--raw"},
		Mode:        startupModifierModeFlags,
	},
}

// startupSnippetBoundaryChoices lists the smart block plus every legal fixed
// context. Listing every value keeps the
// picker's contract identical to the parser's and gives each value a live
// preview; fzf filtering makes the long list instantly navigable (typing 42
// narrows to it).
var startupSnippetBoundaryChoices = buildStartupSnippetBoundaryChoices()

func buildStartupSnippetBoundaryChoices() []startupSnippetBoundaryChoice {
	const maxContext = cli.SnippetContextMax
	choices := make([]startupSnippetBoundaryChoice, 0, maxContext+2)
	choices = append(choices, startupSnippetBoundaryChoice{
		Key:         "block",
		Label:       "smart block",
		Description: "the whole function, class, or element around the match",
	})
	for n := 0; n <= maxContext; n++ {
		desc := fmt.Sprintf("match +/- %d lines", n)
		switch {
		case n == 0:
			desc = "matching lines only"
		case n == 1:
			desc = "match +/- 1 line"
		case n == maxContext:
			desc = fmt.Sprintf("match +/- %d lines (max)", n)
		}
		choices = append(choices, startupSnippetBoundaryChoice{
			Key:                 strconv.Itoa(n),
			Label:               strconv.Itoa(n),
			Description:         desc,
			SnippetContextSet:   true,
			SnippetContextLines: n,
		})
	}
	return choices
}

func startupAvailableModifierChoices(currentArgs []string) []StartupModifierChoice {
	state, _, err := startupCurrentScopeStateForArgs(currentArgs)
	if err != nil {
		state = startupCurrentScopeState{}
	}
	return startupAvailableModifierChoicesWithState(currentArgs, state)
}

func startupAvailableModifierChoicesWithState(currentArgs []string, state startupCurrentScopeState) []StartupModifierChoice {
	if state.Known && state.Empty {
		if !state.NeedsInclude {
			return nil
		}
		return []StartupModifierChoice{
			{
				Key:         "include",
				Label:       "--include",
				Description: "Allow ignored files or folders",
				Args:        []string{"--include"},
				Mode:        startupModifierModeInclude,
			},
		}
	}
	choices := make([]StartupModifierChoice, 0, len(startupModifierChoices)+len(startupModifierMetaChoices))
	for _, choice := range startupModifierChoices {
		if startupModifierChoiceAllowed(currentArgs, choice, state) {
			choices = append(choices, choice)
		}
	}
	if len(choices) > 0 {
		choices = append(choices, startupModifierMetaChoices...)
	}
	return choices
}

func startupModifierChoiceAllowed(currentArgs []string, choice StartupModifierChoice, state startupCurrentScopeState) bool {
	if cli.ValidateCurrentScopeFlagSequence(currentArgs, choice.Args) != nil {
		return false
	}
	if currentScopeHasNarrowGitChangeFilter(currentArgs) {
		if startupChoiceIsGitChangeFilter(choice) {
			return false
		}
		if startupChoiceIsDiffModifier(choice) && !startupDiffModifierMatchesActiveFilter(currentArgs, choice) {
			return false
		}
	}
	return startupModifierChoiceMeaningful(choice, state)
}

// currentScopeHasNarrowGitChangeFilter returns true when the current scope
// already has a specific git change filter (--staged, --unstaged, --untracked).
// After a narrow filter, broader (--changed) and sibling filters are hidden
// because they would either widen the scope or produce an empty intersection.
// --changed is NOT narrow — it's the superset, and narrowing after it is valid.
func currentScopeHasNarrowGitChangeFilter(args []string) bool {
	return currentScopeHasFlag(args, "--staged") ||
		currentScopeHasFlag(args, "--unstaged") ||
		currentScopeHasFlag(args, "--untracked")
}

func startupChoiceIsGitChangeFilter(choice StartupModifierChoice) bool {
	switch choice.Args[0] {
	case "--changed", "--staged", "--unstaged", "--untracked":
		return true
	}
	return false
}

func startupChoiceIsDiffModifier(choice StartupModifierChoice) bool {
	switch choice.Args[0] {
	case "--changed-diff", "--staged-diff", "--unstaged-diff":
		return true
	}
	return false
}

// startupDiffModifierMatchesActiveFilter returns true when the diff modifier
// corresponds to the active narrow git filter. E.g. --unstaged-diff matches
// --unstaged, --staged-diff matches --staged. --changed-diff matches --changed
// but --changed is not a narrow filter so this is only called for narrow ones.
// --untracked has no matching diff (untracked files have no diff).
func startupDiffModifierMatchesActiveFilter(args []string, choice StartupModifierChoice) bool {
	switch choice.Args[0] {
	case "--staged-diff":
		return currentScopeHasFlag(args, "--staged")
	case "--unstaged-diff":
		return currentScopeHasFlag(args, "--unstaged")
	case "--changed-diff":
		return currentScopeHasFlag(args, "--changed")
	}
	return false
}

func startupValidateModifierChoice(currentArgs []string, choice StartupModifierChoice) error {
	switch choice.Mode {
	case startupModifierModeFinish, startupModifierModeExtras:
		return nil
	}
	return cli.ValidateCurrentScopeFlagSequence(currentArgs, choice.Args)
}

func startupModifierChoiceMeaningful(choice StartupModifierChoice, state startupCurrentScopeState) bool {
	if choice.Mode == startupModifierModeFinish || choice.Mode == startupModifierModeExtras {
		return true
	}
	if len(choice.Args) == 0 {
		return false
	}
	if !state.Known {
		return true
	}

	switch choice.Args[0] {
	case "--depth":
		return true
	case "--changed":
		if !state.GitKnown {
			return false
		}
		return state.AnyChanged && !state.AllChanged
	case "--staged":
		if !state.GitKnown {
			return false
		}
		return state.AnyStaged && !state.AllStaged
	case "--unstaged":
		if !state.GitKnown {
			return false
		}
		return state.AnyUnstaged && !state.AllUnstaged
	case "--untracked":
		if !state.GitKnown {
			return false
		}
		return state.AnyUntracked && !state.AllUntracked
	case "--changed-diff":
		if !state.GitKnown {
			return false
		}
		return state.AnyChanged
	case "--staged-diff":
		if !state.GitKnown {
			return false
		}
		return state.AnyStaged
	case "--unstaged-diff":
		if !state.GitKnown {
			return false
		}
		return state.AnyUnstaged
	case "--include":
		return state.HasScopedIgnoredTargets
	default:
		return true
	}
}

func startupModifierChoiceLines(choices []StartupModifierChoice) ([]string, map[string]StartupModifierChoice) {
	lines := make([]string, 0, len(choices))
	index := make(map[string]StartupModifierChoice, len(choices))
	labelWidth := 0
	for _, choice := range choices {
		if len(choice.Label) > labelWidth {
			labelWidth = len(choice.Label)
		}
	}
	for _, choice := range choices {
		label := fmt.Sprintf("%-*s", labelWidth, choice.Label)
		lines = append(lines, strings.Join([]string{label, choice.Key, choice.Description}, "\t"))
		index[choice.Key] = choice
	}
	return lines, index
}

func startupSnippetBoundaryChoiceLines() ([]string, map[string]startupSnippetBoundaryChoice) {
	lines := make([]string, 0, len(startupSnippetBoundaryChoices))
	index := make(map[string]startupSnippetBoundaryChoice, len(startupSnippetBoundaryChoices))
	labelWidth := 0
	for _, choice := range startupSnippetBoundaryChoices {
		if len(choice.Label) > labelWidth {
			labelWidth = len(choice.Label)
		}
	}
	for _, choice := range startupSnippetBoundaryChoices {
		label := fmt.Sprintf("%-*s", labelWidth, choice.Label)
		lines = append(lines, strings.Join([]string{label, choice.Key, choice.Description}, "\t"))
		index[choice.Key] = choice
	}
	return lines, index
}

func startupModifierPickerHeader() string {
	return startupModifierPickerHeaderWithEscHint("")
}

func startupModifierPickerHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Choose what to do next.",
		"Preview shows the current files.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

func startupExtrasPickerHeader() string {
	return startupExtrasPickerHeaderWithEscHint("")
}

func startupExtrasPickerHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Pick extra global options.",
		"Tab marks multiple options; Enter confirms.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  %s", platform.MultiSelectToggleAllKey(), startupEscLabel(escHint)),
	)
}

func snippetBoundaryPickerHeader() string {
	return snippetBoundaryPickerHeaderWithEscHint("")
}

func snippetBoundaryPickerHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Choose snippet boundaries.",
		"Smart block is the default; numbers give fixed context.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}
