package discovery

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

// ContentMatchMemoFilename is the picker-session sidecar written by the
// content-match reload child. The chooser reads it after fzf confirms a query
// and before removing the session directory, allowing the parent to reuse the
// exact membership that was already displayed.
const ContentMatchMemoFilename = "content-match-memo.json"

// FzfPreviewCommand is used by target-selection pickers before a parent scope
// has settled entries. SCC does not apply there; modifier previews use the
// checkpoint wrappers in startup_picker.go / fzfCheckpointContentMatchListCommand.
func FzfPreviewCommand(_ bool, withBinaries ...bool) string {
	return fzfPreviewCommand("", withBinaries...)
}

// FzfPreviewCommandWithInventory makes target preview children project their
// selection from the parent picker's compact inventory instead of rediscovering
// and reclassifying the project on every focus change.
func FzfPreviewCommandWithInventory(inventoryPath string, withBinaries ...bool) string {
	return fzfPreviewCommand(inventoryPath, withBinaries...)
}

func fzfPreviewCommand(inventoryPath string, withBinaries ...bool) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	selfQ := ShellQuoteArg(self)
	binaryFlag := ""
	if len(withBinaries) > 0 && withBinaries[0] {
		binaryFlag = " --with-binaries"
	}

	// {+f} passes all selected rows through a file (focused row when none are
	// marked). Keep the raw file placeholder quoted, unlike ordinary fields.
	// {2}/{3}/{4} are the focused entry's metadata for tree highlight.
	command := selfQ + ` --quiet` + binaryFlag + ` --internal-tree-preview`
	if inventoryPath != "" {
		command += ` --internal-target-inventory ` + ShellQuoteArg(inventoryPath)
	}
	return command +
		` --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4}` +
		` --internal-target-selection ` + ShellQuoteArg("{+f}")
}

// FzfContentPreviewCommand builds the preview-pane command for the
// content-match picker. The same command serves three states inside
// runInternalFilePreview:
//
//   - Empty {q}: emits the contextual hint document (smart-case tips +
//     pattern examples). No checkpoint needed.
//   - Non-empty {q}, empty {3}, empty {1}: emits the searching document.
//   - Non-empty {q}, empty {3} (the `[all current matches]` row): if a
//     checkpoint path is wired in, emits the full scope tree from the
//     checkpoint. Otherwise emits nothing.
//   - Non-empty {q}, non-empty {3}: per-file preview with match
//     highlighting (or snippet extraction / diff).
//
// An explicitly empty checkpointPath keeps standalone preview callers'
// `[all current matches]` preview empty. The interactive content picker
// requires a checkpoint and reports setup failure instead. Pass the path
// returned by fzfCheckpointContentMatchListCommand to enable the tree.
func FzfContentPreviewCommand(flag, checkpointPath string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{
		ShellQuoteArg(self),
		"--quiet",
		"--internal-file-preview",
		"--internal-searching-preview",
		"--internal-file-path", "{3}",
		"--internal-tree-target", "{1}",
	}
	if checkpointPath != "" {
		parts = append(parts, "--internal-prediscovered", ShellQuoteArg(checkpointPath))
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " ")
}

// FzfContentSearchingPreviewCommand builds the one-shot preview action used
// before the content picker reloads results for the current query. It renders
// the empty-regex teaching document when {q} is empty, and the searching hint
// for non-empty {q}; normal focused-row previews still use
// FzfContentPreviewCommand.
func FzfContentSearchingPreviewCommand(flag string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{
		ShellQuoteArg(self),
		"--quiet",
		"--internal-file-preview",
		"--internal-searching-preview",
		"--internal-file-path", ShellQuoteArg(""),
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " ")
}

// fzfCheckpointContentMatchListCommand shares one checkpoint between reload
// and preview children. Setup errors must not restore unbounded scope argv or
// let a child rediscover a different membership universe.
func fzfCheckpointContentMatchListCommand(currentArgs []string, flag string) (string, string, func(), error) {
	noop := func() {}
	switch flag {
	case "--contains", "--snippet", "--not-contains":
	default:
		return "", "", noop, fmt.Errorf("unsupported content picker flag %q", flag)
	}
	if scopeViewResolverFn == nil {
		return "", "", noop, fmt.Errorf("content picker requires a retained scope resolver")
	}
	view, ok := scopeViewResolverFn(currentArgs)
	if !ok || len(view.Targets) == 0 {
		return "", "", noop, fmt.Errorf("content picker could not retain the current scope")
	}

	self, err := os.Executable()
	if err != nil {
		return "", "", noop, err
	}
	tmpdir, err := os.MkdirTemp("", "catclip-scc-*")
	if err != nil {
		return "", "", noop, err
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, GitStatusPathspecsForEntries(view.GitContext, view.Entries))
		if err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", "", noop, err
		}
	}
	if err := WriteCheckpoint(checkpointPath, view.WorkingDir, CheckpointData{
		Scope:      &command.ExecutionScope{Targets: append([]string(nil), view.Targets...)},
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
		NoIgnore:   view.NoIgnore,
	}); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", noop, err
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-content-match-list", "--internal-prediscovered", ShellQuoteArg(checkpointPath), "--internal-checkpoint-scope"}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " "), checkpointPath, func() {
		_ = os.RemoveAll(tmpdir)
	}, nil
}

// ContentMatchPreviewWindow returns the fzf --preview-window spec for the
// content match picker. For --contains, it appends a `+{6}-/2` offset so
// the preview pane opens centered on the first match per focused file
// (column 6 carries the first-match line number, populated by
// attachFirstMatchLines). Snippet mode skips the offset because the
// preview already renders matched blocks, not the full file — centering
// on a line number would scroll PAST the snippet content.
//
// Cross-platform: --preview-window's `+{N}-/2` syntax is fzf-native, not
// shell-evaluated, so cmd.exe / PowerShell / sh handle it identically.
// The substitution value is always a positive integer (the [all current
// matches] row uses contentMatchAllMatchesPreviewLine = "1") so fzf never
// sees an empty `{6}` that could break the flag parse.
func ContentMatchPreviewWindow(flag string) string {
	if flag != "--contains" {
		return ""
	}
	return picker.DefaultPreviewWindow + ":+{6}-/2"
}

func ContentMatchPickerHeader(flag string) string {
	return ContentMatchPickerHeaderWithEscHint(flag, "")
}

func ContentMatchPickerHeaderWithEscHint(flag, escHint string) string {
	firstLine := "Keep files whose contents match a regex."
	switch flag {
	case "--snippet":
		firstLine = "Extract snippets whose contents match a regex."
	case "--not-contains":
		firstLine = "Drop files whose contents match a regex."
	}
	return PickerHeader(
		firstLine,
		"Type a regex.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  %s", platform.MultiSelectToggleAllKey(), startupEscLabel(escHint)),
	)
}

func MultiSelectPickerBindings() []string {
	return []string{
		"tab:toggle+down",
		"btab:toggle+up",
		platform.MultiSelectToggleAllBinding(),
		"multi:refresh-preview",
	}
}

func ShellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	// fzf's file placeholder is substituted raw. It needs double quotes;
	// ordinary placeholders must remain unquoted in the command builders.
	if arg == "{+f}" {
		return `"{+f}"`
	}
	if runtime.GOOS == "windows" {
		// Keep the existing native shell contract until the separate cmd /
		// PowerShell launcher repair has real-fzf Windows acceptance coverage.
		if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
			return arg
		}
		return strconv.Quote(arg)
	}
	plain := true
	for _, r := range arg {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_./:-", r) {
			continue
		}
		plain = false
		break
	}
	if plain {
		return arg
	}
	// Preserve the established readable spelling for simple spaces/globs.
	// These characters have no expansion semantics inside double quotes.
	if !strings.ContainsAny(arg, "$`\\\"!\r\n") {
		return `"` + arg + `"`
	}
	// Literal shell words, not Go string literals: double quotes alone allow
	// dollar/backtick expansion. Spell apostrophes and backslashes as separate
	// double-quoted fragments so this also works in fish, whose single quotes
	// interpret backslash escapes differently from POSIX shells.
	return "'" + strings.NewReplacer("'", `'"'"'`, "\\", `'"\\"'`).Replace(arg) + "'"
}

func formatFzfCandidates(candidates []string, kind, state string) []string {
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		lines = append(lines, strings.Join([]string{
			path.Base(candidate),
			candidate,
			kind,
			state,
		}, "\t"))
	}
	return lines
}

// fuzzySearchTargetMatches runs the same mixed file-and-directory rows used by
// the interactive target picker through fzf's non-interactive filter mode.
// Keeping one input shape makes headless ambiguity the picker result without
// the prompt, rather than a second fuzzy language assembled from per-kind
// searches.
func (r *Resolver) fuzzySearchTargetMatches(baseRel, query string) ([]TargetMatch, error) {
	allTargets, err := r.allVisibleTargets()
	if err != nil {
		return nil, err
	}

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "" {
		baseRel = "."
	}
	prefix := ""
	if baseRel != "." {
		prefix = baseRel + "/"
	}

	candidates := make([]TargetMatch, 0, len(allTargets))
	for _, match := range allTargets {
		if prefix != "" && !strings.HasPrefix(match.Path, prefix) {
			continue
		}
		candidates = append(candidates, match)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	return fuzzyFilterTargetMatches(query, candidates)
}

// fuzzyFilterTargetMatches applies the target picker's fzf query grammar to
// an already-built candidate set. Visible-only and combined no-ignore
// inventories use the same row shape, search field, and ranking.
func fuzzyFilterTargetMatches(query string, candidates []TargetMatch) ([]TargetMatch, error) {
	bin, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	labels, index := TargetMatchLabels(candidates)
	filtered, err := runFzfTargetFilterLines(bin, query, labels)
	if err != nil {
		return nil, err
	}
	ranked := make([]TargetMatch, 0, len(filtered))
	for _, key := range filtered {
		match, ok := index[key]
		if ok {
			ranked = append(ranked, match)
		}
	}
	return ranked, nil
}

func exactBasenameTargetMatches(candidates []TargetMatch, basename string) []TargetMatch {
	basename = normalizeRelPath(basename)
	if basename == "" || strings.Contains(basename, "/") {
		return nil
	}
	matches := make([]TargetMatch, 0, 1)
	for _, candidate := range candidates {
		if path.Base(normalizeRelPath(candidate.Path)) == basename {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func eligibleTargetMatches(candidates []TargetMatch) []TargetMatch {
	eligible := make([]TargetMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Kind == treeTargetKindDir && candidate.State == treeTargetStateNoTextChildren {
			continue
		}
		eligible = append(eligible, candidate)
	}
	return eligible
}

func TargetMatchLabels(matches []TargetMatch) ([]string, map[string]TargetMatch) {
	labels := make([]string, 0, len(matches))
	index := make(map[string]TargetMatch, len(matches))
	for _, match := range matches {
		// fzf applies --nth after --with-nth. Keep the presentation prefix
		// and path as two transformed fields so target pickers can display
		// "[file] path" while matching only field 2. Collapsing presentation
		// to field 1 makes --nth 2 an empty search domain.
		label := ""
		switch match.Kind {
		case "file":
			label = "[file]"
		case "dir":
			label = "[dir]"
		default:
			label = "[" + match.Kind + "]"
		}
		if match.Kind == "all" {
			plain := "[select all files]"
			label = "\x1b[1m" + plain + "\x1b[0m"
		} else if match.Ignored {
			source := strings.TrimSpace(match.IgnoreSource)
			if source != "" {
				label = "[" + match.Kind + " " + source + "]"
			}
		}
		labels = append(labels, strings.Join([]string{
			label,
			match.Path,
			TargetMatchPreviewKind(match),
			TargetMatchPreviewState(match),
		}, "\t"))
		index[match.Path] = match
	}
	return labels, index
}

func TargetMatchPreviewKind(match TargetMatch) string {
	switch match.Kind {
	case "all", treeTargetKindDir:
		return treeTargetKindDir
	case treeTargetKindFile:
		return treeTargetKindFile
	default:
		return normalizeTreeTargetKind(match.Kind)
	}
}

func TargetMatchPreviewState(match TargetMatch) string {
	if state := normalizeTreeTargetState(match.State); state != "" {
		return state
	}
	switch TargetMatchPreviewKind(match) {
	case treeTargetKindDir:
		return treeTargetStateOK
	case treeTargetKindFile:
		return treeTargetStateText
	default:
		return ""
	}
}

func targetPickerHeader(prompt string) string {
	return TargetPickerHeaderWithEscHint(prompt, "")
}

func TargetPickerHeaderWithEscHint(prompt, escHint string) string {
	firstLine := "Pick files and folders to include."
	if prompt == "then> " {
		firstLine = "Add more files and folders."
	}
	controls := fmt.Sprintf(
		"[Up/Down] move [Enter] confirm [Tab] mark [%s] toggle",
		platform.MultiSelectToggleAllKey(),
	)
	if escHint == "undo" {
		controls = fmt.Sprintf(
			"[Enter] confirm [Tab] mark [%s] toggle [Esc] undo",
			platform.MultiSelectToggleAllKey(),
		)
	}
	return PickerHeader(
		firstLine,
		"Type to search by name.",
		controls,
	)
}

func TargetPickerSymbolsHint() string {
	return "Symbols: 'name not fuzzy, ^name starts with, name$ ends with"
}

// PickerHeaderWithFzfSearchSymbols fills a header's reserved fourth line with
// the compact fzf query-language hint used by name-based selection pickers.
func PickerHeaderWithFzfSearchSymbols(header string, colors platform.Palette) string {
	header = strings.TrimSuffix(header, "\n")
	hint := TargetPickerSymbolsHint()
	if colors == (platform.Palette{}) {
		return header + "\n" + hint
	}
	style := func(symbol string) string {
		return colors.Bold + colors.Prompt + symbol + colors.Reset
	}
	styledHint := strings.NewReplacer(
		"'name", style("'")+"name",
		"^name", style("^")+"name",
		"name$", "name"+style("$"),
	).Replace(hint)
	return header + "\n" + styledHint
}

func styledTargetPickerHeaderWithSymbols(prompt, escHint string, colors platform.Palette) string {
	return PickerHeaderWithFzfSearchSymbols(TargetPickerHeaderWithEscHint(prompt, escHint), colors)
}

func SafeTargetPickerHeader() string {
	return targetPickerHeader("select> ")
}

func IgnoredTargetPickerHeader() string {
	return IgnoredTargetPickerHeaderWithEscHint("")
}

func IgnoredTargetPickerHeaderWithEscHint(escHint string) string {
	return PickerHeader(
		"Add files and folders ignored by .gitignore or .hiss.",
		"Type to search by name.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  %s", platform.MultiSelectToggleAllKey(), startupEscLabel(escHint)),
	)
}

func PickerHeader(lines ...string) string {
	if len(lines) > 4 {
		lines = lines[:4]
	}
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// longestLiteralPathPrefix returns the longest leading path segment of
// pattern that contains no glob characters. For "cmd/*.go" it returns
// "cmd"; for "internal/cli/*.go" → "internal/cli"; for "*.go" → "".
func longestLiteralPathPrefix(pattern string) string {
	segs := strings.Split(pattern, "/")
	literal := segs[:0]
	for _, seg := range segs {
		if strings.ContainsAny(seg, "*?[") {
			break
		}
		literal = append(literal, seg)
	}
	return strings.Join(literal, "/")
}
