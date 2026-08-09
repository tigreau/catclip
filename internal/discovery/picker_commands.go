package discovery

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

// FzfPreviewCommand is used by target-selection pickers before a parent scope
// has settled entries. SCC does not apply there; modifier previews use the
// checkpoint wrappers in startup_picker.go / fzfCheckpointContentMatchListCommand.
func FzfPreviewCommand(_ bool, withBinaries ...bool) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	selfQ := ShellQuoteArg(self)
	binaryFlag := ""
	if len(withBinaries) > 0 && withBinaries[0] {
		binaryFlag = " --with-binaries"
	}

	// {+2} passes all selected targets (falls back to focused when none selected).
	// {2}/{3}/{4} are the focused entry's metadata for tree highlight.
	return selfQ + ` --quiet` + binaryFlag + ` --internal-tree-preview` +
		` --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4}` +
		` {+2}`
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
// checkpointPath is empty when the caller couldn't write a checkpoint
// (legacy fallback path); in that case the `[all current matches]`
// preview is empty, matching pre-v0.5.2 behavior. Pass the path
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

// FzfContentMatchListCommand is the legacy fallback when the checkpoint match
// list command cannot be built. Normal content pickers call
// fzfCheckpointContentMatchListCommand, which loads entries[N] from disk.
func FzfContentMatchListCommand(currentArgs []string, flag string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-content-match-list"}
	for _, arg := range currentArgs {
		parts = append(parts, ShellQuoteArg(arg))
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " ")
}

// fzfCheckpointContentMatchListCommand returns:
//   - the fzf `reload` command string for the content-match list,
//   - the checkpoint path on disk (empty when the fast SCC path was not
//     taken — caller should treat that as "no tree preview available"),
//   - a cleanup function that removes the tmpdir housing the checkpoint.
//
// The checkpoint path is exposed so the preview command builder can wire
// the same JSON file into --internal-file-preview's empty-path branch
// (the `[all current matches]` row's scope tree). Match-list reload and
// preview share the same checkpoint file — one JSON write per picker
// open.
// currentScopeNeedsNoIgnoreCheckpoint reports whether the current scope can
// contain ignored entries. The picker's direct rg subprocess needs
// --no-ignore to walk the same universe represented by the checkpoint.
func currentScopeNeedsNoIgnoreCheckpoint(args []string) bool {
	needNoIgnore := false
	for _, a := range args {
		switch a {
		case "--then":
			needNoIgnore = false
		case "--no-ignore":
			needNoIgnore = true
		}
	}
	return needNoIgnore
}

func fzfCheckpointContentMatchListCommand(currentArgs []string, flag string) (string, string, func()) {
	fallback := func() string {
		return FzfContentMatchListCommand(currentArgs, flag)
	}
	noop := func() {}
	switch flag {
	case "--contains", "--snippet", "--not-contains":
	default:
		return fallback(), "", noop
	}
	if scopeViewResolverFn == nil {
		return fallback(), "", noop
	}
	view, ok := scopeViewResolverFn(currentArgs)
	if !ok || len(view.Entries) == 0 {
		return fallback(), "", noop
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", "", noop
	}
	tmpdir, err := os.MkdirTemp("", "catclip-scc-*")
	if err != nil {
		return fallback(), "", noop
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, GitStatusPathspecsForEntries(view.GitContext, view.Entries))
		if err != nil {
			_ = os.RemoveAll(tmpdir)
			return fallback(), "", noop
		}
	}
	if err := WriteCheckpoint(checkpointPath, view.WorkingDir, CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
		NoIgnore:   currentScopeNeedsNoIgnoreCheckpoint(currentArgs),
	}); err != nil {
		_ = os.RemoveAll(tmpdir)
		return fallback(), "", noop
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-content-match-list", "--internal-prediscovered", ShellQuoteArg(checkpointPath)}
	// Embed the parent scope's positional targets so the child parses the
	// SAME scope instead of an implicit "." — direct-mode rg in the child
	// searches scope.Targets[0], and without these it walks the whole
	// working dir (correct results via checkpoint intersection, but
	// cwd-wide cost and exit-2 fragility; live failure 2026-07-04 with
	// cwd=Desktop, target=vscode-main).
	for _, target := range view.Targets {
		parts = append(parts, ShellQuoteArg(target))
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " "), checkpointPath, func() {
		_ = os.RemoveAll(tmpdir)
	}
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
	if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
		return arg
	}
	return strconv.Quote(arg)
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
		label := fmt.Sprintf("[%s]", match.Kind)
		if match.Kind == "all" {
			plain := "[select all files]"
			label = "\x1b[1m" + plain + "\x1b[0m"
		} else if match.Ignored {
			source := strings.TrimSpace(match.IgnoreSource)
			if source != "" {
				label = fmt.Sprintf("[%s %s]", match.Kind, source)
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
