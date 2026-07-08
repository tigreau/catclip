package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

// LoadVersion is provided by root via SetVersionLoader so the parser can
// stamp command.Parsed.Version. Default returns "dev" until set.
var loadVersionFn = func() string { return "dev" }

// SetVersionLoader wires a version-resolution function in (root provides
// it via loadVersion in path_helpers.go). Called once at process start.
func SetVersionLoader(fn func() string) {
	if fn != nil {
		loadVersionFn = fn
	}
}

// stdinPathCache memoizes the contents of os.Stdin so a multi-flag
// pipeline like `... | catclip src --include - --exclude -` reads it
// once and lets both modifiers consume the same paths.
type stdinPathCache struct {
	loaded   bool
	paths    []string
	rawPaths []string
}

// executionScopeBuilder is the parser-time accumulator for an
// ExecutionScope. Embeds command.ExecutionScope so the parser writes
// fields directly (current.Targets = …). explicitTargets is parser
// bookkeeping — counts positional targets so "catclip ." vs "catclip"
// stays distinguishable after normalization.
type executionScopeBuilder struct {
	command.ExecutionScope
	explicitTargets int
}

func (b executionScopeBuilder) hasContent() bool {
	return len(b.Targets) > 0 || len(b.IncludedTargets) > 0 || len(b.Only) > 0 || len(b.Exclude) > 0 ||
		len(b.Stages) > 0 ||
		b.Contains != "" || b.Paths || b.SnippetPattern != "" || b.Snippet || b.Changed || b.Staged || b.Unstaged ||
		b.Untracked || b.Diff
}

// ParseArgs is the strict CLI entry point: positional targets must be
// explicit. Use ParseArgsAllowImplicitDot for inspection paths (startup
// pickers, etc.) where defaulting to "." is appropriate.
func ParseArgs(args []string) (command.Parsed, error) {
	return parseArgsWithMode(args, false)
}

// ParseArgsAllowImplicitDot is the inspection parser: when no target is
// provided the scope defaults to ".".
func ParseArgsAllowImplicitDot(args []string) (command.Parsed, error) {
	return parseArgsWithMode(args, true)
}

func parseArgsWithMode(args []string, allowImplicitDotScope bool) (command.Parsed, error) {
	wd, err := os.Getwd()
	if err != nil {
		return command.Parsed{}, err
	}

	cfg := command.Parsed{
		Action:     command.ActionRun,
		Version:    loadVersionFn(),
		Platform:   platform.Detect(),
		WorkingDir: wd,
		OutputMode: command.OutputModeClipboard,
	}

	executionScopes := make([]command.ExecutionScope, 0, 2)
	var current executionScopeBuilder
	inModifierMode := false
	lastNoValueModifier := ""
	var stdinCache stdinPathCache

	finalize := func() error {
		if !current.hasContent() {
			current = executionScopeBuilder{}
			return nil
		}

		s := current.ExecutionScope
		mode := s.OutputMode()
		if s.Staged || s.Unstaged || s.Untracked {
			s.Changed = true
		}
		hasChangeSelector := s.HasGitSelection()

		if mode == command.EntryModeDiff && s.Untracked {
			return UntrackedDiffError()
		}
		if mode == command.EntryModeSnippet && s.SnippetPattern == "" && !cfg.FilePreview && !cfg.ContentMatchList {
			return RequiredStageValueError("--snippet")
		}
		if mode == command.EntryModeDiff && !hasChangeSelector {
			return MissingDiffSelectorError()
		}
		if err := ValidateScopeStageOrder(current.Stages); err != nil {
			return err
		}
		if cfg.WithBinaries {
			if err := validateWithBinariesCompatibility(s); err != nil {
				return err
			}
		}
		if len(s.Targets) == 0 {
			// Effect 5: --include with no positional target. Refuse to
			// insert the "." default when --include is set. Two shapes
			// the user might have meant — "just docs" (walk docs only)
			// vs "everything + docs" (walk repo, authorize docs) — have
			// different perf and output. Silently picking one hides the
			// ambiguity. Error with a canonical-form hint pointing at
			// the double-syntax shape ignoredDirMessage suggests. Gated
			// on !allowImplicitDotScope so inspection parsers used by
			// the startup picker / preview probes still fall through to
			// the "." default (the picker's resolved argv always
			// includes a positional, so the error surfaces to
			// strict-runtime entry only).
			if len(s.IncludedTargets) > 0 && !allowImplicitDotScope {
				return BareIncludeMissingTargetError(s.IncludedTargets[0])
			}
			if cfg.Headless {
				return newUsageError("Error: --headless requires explicit targets.\n  Pass a path, --include, or another scope-defining flag.\n  Example: catclip . --headless")
			}
			if cfg.IsInternalKind() || cfg.Action != command.ActionRun || allowImplicitDotScope {
				s.Targets = []string{"."}
			} else {
				return newUsageError("Error: no target specified.\n  Pass an explicit target — use '.' for the current directory.\n  Example: catclip . --paths")
			}
		}

		executionScopes = append(executionScopes, s)
		current = executionScopeBuilder{}
		inModifierMode = false
		lastNoValueModifier = ""
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			cfg.Action = command.ActionHelp
			return cfg, nil
		case "--help-all":
			cfg.Action = command.ActionHelpAll
			return cfg, nil
		case "--version", "-V":
			cfg.Action = command.ActionVersion
			return cfg, nil
		case "--hiss":
			cfg.Action = command.ActionEditHiss
		case "--hiss-reset":
			cfg.Action = command.ActionResetHiss
		case "--all-ignore-rules":
			cfg.Action = command.ActionListIgnoreRules
		case "-v", "--verbose":
			cfg.Verbose = true
		case "-q", "--quiet":
			cfg.Quiet = true
		case "-y", "--yes":
			cfg.Yes = true
		case "-p", "--print":
			cfg.OutputMode = command.OutputModeStdout
		case "--headless":
			cfg.Headless = true
		case "-r", "--raw":
			cfg.Raw = true
		case "--with-binaries":
			cfg.WithBinaries = true
		case "-t", "--no-tree":
			cfg.NoTree = true
		case "--no-bundle":
			cfg.NoBundle = true
		case "--preview":
			cfg.Preview = true
		case "--internal-tree-preview":
			cfg.TreePreview = true
		case "--input-dir":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --input-dir requires a directory path.")
			}
			i++
			cfg.TreeInputDir = args[i]
		case "--input-stem":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --input-stem requires a payload stem.")
			}
			i++
			cfg.TreeInputStem = args[i]
		case "--internal-prediscovered":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-prediscovered requires a checkpoint path.")
			}
			i++
			cfg.PrediscoveredPath = args[i]
		case "--internal-tree-target":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-tree-target requires a path.")
			}
			i++
			cfg.TreeTarget = args[i]
		case "--internal-tree-kind":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-tree-kind requires a value.")
			}
			i++
			cfg.TreeKind = args[i]
		case "--internal-tree-state":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-tree-state requires a value.")
			}
			i++
			cfg.TreeState = args[i]
		case "--internal-file-preview":
			cfg.FilePreview = true
		case "--internal-searching-preview":
			cfg.FilePreview = true
			cfg.FileSearchingPreview = true
		case "--internal-file-path":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-file-path requires a path.")
			}
			i++
			cfg.FilePath = args[i]
		case "--internal-content-match-list":
			cfg.ContentMatchList = true
		case "--internal-snippet-boundary-preview":
			cfg.SnippetBoundaryPreview = true
		case "--internal-boundary-source":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-boundary-source requires a path.")
			}
			i++
			cfg.BoundarySourcePath = args[i]
		case "--internal-boundary-key":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-boundary-key requires a value.")
			}
			i++
			cfg.BoundaryKey = args[i]
		case "--internal-lines-preview":
			cfg.LinesPreview = true
		case "--internal-recent-preview":
			cfg.RecentPreview = true
		case "--internal-recent-data":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-recent-data requires a path.")
			}
			i++
			cfg.RecentData = args[i]
		case "--internal-sink-toggle":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-sink-toggle requires a path.")
			}
			i++
			cfg.SinkTogglePath = args[i]
		case "--internal-sink-preview":
			if i+3 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-sink-preview requires mode, output, and tree paths.")
			}
			cfg.SinkPreviewModePath = args[i+1]
			cfg.SinkPreviewOutputPath = args[i+2]
			cfg.SinkPreviewTreePath = args[i+3]
			i += 3
		case "--internal-recent-selection":
			if i+1 >= len(args) {
				return command.Parsed{}, newUsageError("Error: --internal-recent-selection requires a value.")
			}
			i++
			cfg.RecentSelect = args[i]
		case "--changed":
			inModifierMode = true
			current.Changed = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageChanged})
			lastNoValueModifier = arg
		case "--staged":
			inModifierMode = true
			current.Staged = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageStaged})
			lastNoValueModifier = arg
		case "--unstaged":
			inModifierMode = true
			current.Unstaged = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageUnstaged})
			lastNoValueModifier = arg
		case "--untracked":
			inModifierMode = true
			current.Untracked = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageUntracked})
			lastNoValueModifier = arg
		case "--changed-diff":
			inModifierMode = true
			current.Changed = true
			current.Diff = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageChangedDiff})
			lastNoValueModifier = arg
		case "--staged-diff":
			inModifierMode = true
			current.Staged = true
			current.Diff = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageStagedDiff})
			lastNoValueModifier = arg
		case "--unstaged-diff":
			inModifierMode = true
			current.Unstaged = true
			current.Diff = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageUnstagedDiff})
			lastNoValueModifier = arg
		case "--lines":
			inModifierMode = true
			current.Lines = true
			var start, end int
			if i+1 < len(args) {
				next := args[i+1]
				if n, err := strconv.Atoi(next); err == nil {
					if n < 1 {
						return command.Parsed{}, newUsageError("Error: --lines start must be >= 1 (got %d).\n  Line numbers are 1-based, matching editors and compiler output.", n)
					}
					start = n
					i++
					if i+1 < len(args) {
						next2 := args[i+1]
						if n2, err := strconv.Atoi(next2); err == nil {
							if n2 < start {
								return command.Parsed{}, newUsageError("Error: --lines end (%d) must be >= start (%d).\n  Use: --lines START END where END >= START.", n2, start)
							}
							end = n2
							i++
						} else if next2 == "EOF" {
							i++
						} else if !strings.HasPrefix(next2, "-") {
							return command.Parsed{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next2)
						}
					}
				} else if !strings.HasPrefix(next, "-") {
					return command.Parsed{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next)
				}
			}
			current.LinesStart = start
			current.LinesEnd = end
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageLines})
			lastNoValueModifier = arg
		case "--snippet":
			inModifierMode = true
			if i+1 >= len(args) {
				return command.Parsed{}, RequiredStageValueError("--snippet")
			}
			i++
			regex := args[i]
			current.SnippetPattern = regex
			current.Snippet = true
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					if n < 0 || n > snippetContextMax {
						return command.Parsed{}, newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", snippetContextMax, n)
					}
					i++
					current.SnippetContextSet = true
					current.SnippetContextLines = n
				}
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return command.Parsed{}, RegexModifierExtraValueError("--snippet", regex, args[i+1])
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageSnippet, Values: []string{regex}})
			lastNoValueModifier = ""
		case "--then":
			if err := finalize(); err != nil {
				return command.Parsed{}, err
			}
		case "--":
			if i != len(args)-1 {
				return command.Parsed{}, BareModifierPlaceholderOrderError()
			}
			if cfg.Headless {
				return command.Parsed{}, BareModifierPlaceholderHeadlessModeError()
			}
			return command.Parsed{}, BareModifierPlaceholderInteractiveOnlyError()
		case "--only":
			inModifierMode = true
			values, next, exactValues, err := consumePathModifierValues(args, i+1, "--only", &stdinCache)
			if err != nil {
				return command.Parsed{}, err
			}
			if len(values) == 0 {
				return command.Parsed{}, RequiredStageValueError("--only")
			}
			current.Only = append(current.Only, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageOnly, Values: append([]string(nil), values...), ExactValues: exactValues})
			i = next - 1
			lastNoValueModifier = ""
		case "--include":
			inModifierMode = true
			values, next, exactValues, err := consumePathModifierValues(args, i+1, "--include", &stdinCache)
			if err != nil {
				return command.Parsed{}, err
			}
			if len(values) == 0 {
				return command.Parsed{}, RequiredStageValueError("--include")
			}
			if err := ValidateIncludeValues(values); err != nil {
				return command.Parsed{}, err
			}
			current.IncludedTargets = append(current.IncludedTargets, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageInclude, Values: append([]string(nil), values...), ExactValues: exactValues})
			i = next - 1
			lastNoValueModifier = ""
		case "--exclude":
			inModifierMode = true
			values, next, exactValues, err := consumePathModifierValues(args, i+1, "--exclude", &stdinCache)
			if err != nil {
				return command.Parsed{}, err
			}
			if len(values) == 0 {
				return command.Parsed{}, RequiredStageValueError("--exclude")
			}
			current.Exclude = append(current.Exclude, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageExclude, Values: append([]string(nil), values...), ExactValues: exactValues})
			i = next - 1
			lastNoValueModifier = ""
		case "--recent":
			inModifierMode = true
			limit, next, err := consumeOptionalRecentLimit(args, i+1)
			if err != nil {
				return command.Parsed{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageRecent, Limit: limit})
			i = next - 1
			lastNoValueModifier = ""
		case "--size":
			inModifierMode = true
			nums, next, err := consumeOptionalSizeBounds(args, i+1)
			if err != nil {
				return command.Parsed{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageSize, Nums: nums})
			i = next - 1
			lastNoValueModifier = ""
		case "--depth":
			inModifierMode = true
			limit, next, err := consumeDepthLimit(args, i+1)
			if err != nil {
				return command.Parsed{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageDepth, Limit: limit})
			i = next - 1
			lastNoValueModifier = ""
		case "--contains":
			inModifierMode = true
			if i+1 >= len(args) {
				return command.Parsed{}, ContainsMissingPatternError(args, i)
			}
			i++
			current.Contains = args[i]
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return command.Parsed{}, RegexModifierExtraValueError("--contains", current.Contains, args[i+1])
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageContains, Values: []string{args[i]}})
			if looksLikeGlobConfusion(current.Contains) {
				cfg.Warnings = append(cfg.Warnings, "Warning: --contains uses regex, not globs. Did you mean '.*' instead of '*'?\n  Example: --contains 'use.*Context' (not 'use*Context')")
			}
			lastNoValueModifier = ""
		case "--not-contains":
			inModifierMode = true
			if i+1 >= len(args) {
				return command.Parsed{}, NotContainsMissingPatternError(args, i)
			}
			i++
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return command.Parsed{}, RegexModifierExtraValueError("--not-contains", args[i], args[i+1])
			}
			current.NotContains = append(current.NotContains, args[i])
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageNotContains, Values: []string{args[i]}})
			if looksLikeGlobConfusion(args[i]) {
				cfg.Warnings = append(cfg.Warnings, "Warning: --not-contains uses regex, not globs. Did you mean '.*' instead of '*'?\n  Example: --not-contains 'use.*Context' (not 'use*Context')")
			}
			lastNoValueModifier = ""
		case "--paths":
			inModifierMode = true
			current.Paths = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StagePaths})
			lastNoValueModifier = arg
		default:
			if err := EqualsFormRejectionError(arg); err != nil {
				return command.Parsed{}, err
			}
			switch {
			case arg == "--diff":
				return command.Parsed{}, DiffStandaloneError()
			case strings.HasPrefix(arg, "--"):
				return command.Parsed{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return command.Parsed{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case arg == "-":
				return command.Parsed{}, newUsageError("Error: '-' is not a valid target path.\n  To read file paths from stdin, use it with a modifier:\n    catclip src --exclude -\n    catclip src --only -")
			default:
				if inModifierMode {
					if lastNoValueModifier != "" {
						return command.Parsed{}, NoValueModifierError(lastNoValueModifier)
					}
					return command.Parsed{}, PositionalAfterModifierError()
				}
				current.Targets = append(current.Targets, arg)
				current.explicitTargets++
				lastNoValueModifier = ""
			}
		}
	}

	if err := finalize(); err != nil {
		return command.Parsed{}, err
	}

	if cfg.Headless || cfg.IsInternalKind() {
		cfg.OutputMode = command.OutputModeStdout
		cfg.Quiet = true
	}

	if len(executionScopes) == 0 {
		if cfg.Headless {
			return command.Parsed{}, newUsageError("Error: --headless requires explicit targets.\n  Pass a path, --include, or another scope-defining flag.\n  Example: catclip . --headless")
		}
		if cfg.IsInternalKind() || cfg.Action != command.ActionRun || allowImplicitDotScope {
			executionScopes = append(executionScopes, command.ExecutionScope{Targets: []string{"."}})
		} else {
			return command.Parsed{}, newUsageError("Error: no target specified.\n  Run catclip from an interactive terminal to pick targets, or pass an explicit target — use '.' for the current directory.\n  Example: catclip .")
		}
	}

	cfg.Command = command.FinalizedSpecFromExecutionScopes(executionScopes)

	return cfg, nil
}

// Parser helpers

func consumeModifierValues(args []string, start int) ([]string, int) {
	values := make([]string, 0, len(args)-start)
	i := start
	for i < len(args) {
		if IsModifierBoundaryToken(args[i]) {
			break
		}
		values = append(values, args[i])
		i++
	}
	return values, i
}

func consumePathModifierValues(args []string, start int, flag string, cache *stdinPathCache) ([]string, int, bool, error) {
	values, next := consumeModifierValues(args, start)
	if len(values) == 1 && values[0] == "-" {
		paths, err := readModifierPathsFromStdin(flag, cache)
		if err != nil {
			return nil, 0, false, err
		}
		return paths, next, true, nil
	}
	return values, next, false, nil
}

func ValidateIncludeValues(values []string) error {
	for _, value := range values {
		if value == "*" {
			continue
		}
		if isAbsolutePathForValidation(value) {
			return newUsageError("Error: --include does not accept absolute paths: %s\n  Use a relative path inside the current target scope.", singleQuoted(value))
		}
		if containsParentTraversal(value) {
			return newUsageError("Error: --include cannot traverse above the current target scope: %s\n  Use a relative path inside the current target scope.", singleQuoted(value))
		}
		if hasGlobChars(value) {
			return newUsageError("Error: --include does not accept glob patterns. Use literal paths or '*' to include all.")
		}
	}
	return nil
}

func isAbsolutePathForValidation(value string) bool {
	if path.IsAbs(value) || filepath.IsAbs(value) {
		return true
	}
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	if len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		drive := value[0]
		return (drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')
	}
	return false
}

func readModifierPathsFromStdin(flag string, cache *stdinPathCache) ([]string, error) {
	if cache.loaded {
		if flag == "--include" {
			if err := ValidateIncludeValues(cache.rawPaths); err != nil {
				return nil, err
			}
		}
		return append([]string(nil), cache.paths...), nil
	}
	if platform.IsTerminalFile(os.Stdin) {
		return nil, newUsageError("Error: %s - reads paths from stdin, but no input is being piped.\n  Pipe paths from another command:\n    catclip src --contains \"pattern\" --paths | catclip src %s -", flag, flag)
	}
	rawPaths, paths, err := readStdinPathValues(os.Stdin)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, newUsageError("Error: %s - received no paths from stdin.\n  The piped command produced no output.", flag)
	}
	if flag == "--include" {
		if err := ValidateIncludeValues(rawPaths); err != nil {
			return nil, err
		}
	}
	cache.loaded = true
	cache.paths = append([]string(nil), paths...)
	cache.rawPaths = append([]string(nil), rawPaths...)
	return append([]string(nil), cache.paths...), nil
}

func readStdinPathValues(stdin io.Reader) ([]string, []string, error) {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	rawPaths := make([]string, 0, 64)
	paths := make([]string, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		normalized := normalizeRelPath(line)
		if normalized == "" {
			continue
		}
		rawPaths = append(rawPaths, line)
		paths = append(paths, normalized)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return rawPaths, paths, nil
}

func validateWithBinariesCompatibility(s command.ExecutionScope) error {
	if s.Contains != "" {
		return newUsageError("Error: --with-binaries cannot be combined with --contains.\n  --contains searches file content, which is not meaningful for binary files.")
	}
	if len(s.NotContains) > 0 {
		return newUsageError("Error: --with-binaries cannot be combined with --not-contains.\n  --not-contains searches file content, which is not meaningful for binary files.")
	}
	if s.Snippet {
		return newUsageError("Error: --with-binaries cannot be combined with --snippet.\n  --snippet extracts content blocks, which is not meaningful for binary files.")
	}
	if s.Diff {
		return newUsageError("Error: --with-binaries cannot be combined with diff output.\n  Diff output operates on file content, which is not meaningful for binary files.")
	}
	return nil
}

func looksLikeGlobConfusion(pattern string) bool {
	return strings.Contains(pattern, "*") &&
		!strings.Contains(pattern, ".") &&
		!strings.Contains(pattern, "[") &&
		!strings.Contains(pattern, "|") &&
		!strings.Contains(pattern, "\\")
}

// FormatScopeSummary is exported because root run() emits it under --verbose.
func FormatScopeSummary(s command.ExecutionScope) string {
	parts := []string{
		fmt.Sprintf("targets=%q", s.Targets),
		fmt.Sprintf("included=%q", s.IncludedTargets),
		fmt.Sprintf("only=%q", s.Only),
		fmt.Sprintf("exclude=%q", s.Exclude),
	}
	if s.Contains != "" {
		parts = append(parts, fmt.Sprintf("contains=%q", s.Contains))
	}
	if len(s.NotContains) > 0 {
		parts = append(parts, fmt.Sprintf("not_contains=%q", s.NotContains))
	}
	if s.Snippet {
		if s.SnippetPattern != "" {
			parts = append(parts, fmt.Sprintf("snippet=%q", s.SnippetPattern))
		} else {
			parts = append(parts, "snippet=true")
		}
	}
	if s.Paths {
		parts = append(parts, "paths=true")
	}
	if s.Changed {
		parts = append(parts, "changed=true")
	}
	if s.Staged {
		parts = append(parts, "staged=true")
	}
	if s.Unstaged {
		parts = append(parts, "unstaged=true")
	}
	if s.Untracked {
		parts = append(parts, "untracked=true")
	}
	if s.Diff {
		parts = append(parts, "diff=true")
	}
	for _, stage := range s.Stages {
		switch stage.Kind {
		case command.StageRecent:
			if stage.Limit == nil {
				parts = append(parts, "recent=true")
				continue
			}
			parts = append(parts, fmt.Sprintf("recent=%d", *stage.Limit))
		case command.StageSize:
			switch len(stage.Nums) {
			case 0:
				parts = append(parts, "size=true")
			case 1:
				parts = append(parts, fmt.Sprintf("size_min=%d", stage.Nums[0]))
			default:
				parts = append(parts, fmt.Sprintf("size=%d..%d", stage.Nums[0], stage.Nums[1]))
			}
		case command.StageDepth:
			if stage.Limit == nil {
				continue
			}
			parts = append(parts, fmt.Sprintf("depth=%d", *stage.Limit))
		case command.StagePaths:
			parts = append(parts, "paths_stage=true")
		}
	}
	return strings.Join(parts, " ")
}

// FormatResolvedStartupCommand renders the canonical "Resolved
// command:" string from argv. Falls back to a shell-quoted echo of the
// args if parsing fails. Used by startup_picker / startup_undo at root
// for the post-pick "Resolved command:" output.
func FormatResolvedStartupCommand(args []string) string {
	if cfg, err := ParseArgsAllowImplicitDot(args); err == nil {
		return command.CanonicalResolvedInvocationCommand(
			command.ResolvedFromParsed(cfg),
			command.RenderFlagsFromParsed(cfg),
		)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "catclip")
	for _, arg := range args {
		parts = append(parts, shellQuoteArg(arg))
	}
	return strings.Join(parts, " ")
}

// HasGlobChars / IsModifierBoundaryToken / IsValueTakingFlag /
// IsKnownScopeModifierToken are exported wrappers so root callers
// (resolver, startup_picker, positional_glob_normalization, etc.) can
// share these stateless checks without duplicating them N times.

// HasGlobChars reports whether pattern contains any glob metacharacter.
func HasGlobChars(pattern string) bool { return hasGlobChars(pattern) }

// ConsumeModifierValues collects argv tokens up to the next modifier
// boundary. Exported because root main.go's positional-glob and
// modifier-collection code uses it during arg pre-normalization.
func ConsumeModifierValues(args []string, start int) ([]string, int) {
	return consumeModifierValues(args, start)
}
