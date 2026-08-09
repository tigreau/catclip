package discovery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

func (r *Resolver) ChooseRootTargetMatches(query, prompt string, includeCopyAll bool, selectedPaths []string) ([]TargetMatch, error) {
	query = NormalizeInteractivePickerQuery(query)
	if SelectionContainsAll(selectedPaths) {
		return nil, ErrSelectionCancelled
	}
	stopSpinner := func() {}
	if !r.interactiveTargetsOk {
		// Renamed from "Loading targets..." to plain English; the
		// 5 s delayed hint acknowledges the unavoidable cold-boot
		// scan cost so users don't think it's hung and Ctrl-C out.
		// On Windows, platform.SlowFileScanHint names the Defender
		// once-per-boot scan explicitly; elsewhere it returns no hint.
		// See
		// RESOLVED_PLAN_target_picker_spinner_reassurance.md.
		stopSpinner = platform.StartLoadingSpinnerWithDelayedHint(
			os.Stderr,
			"Scanning files...",
			platform.SlowFileScanHint(),
			5*time.Second,
		)
	}
	var allTargets []TargetMatch
	var err error
	if r.NoIgnore {
		allTargets, err = r.AllNoIgnoreTargets(nil)
		allTargets = eligibleTargetMatches(allTargets)
	} else {
		allTargets, err = r.allVisibleTargets()
	}
	stopSpinner()
	if err != nil {
		return nil, err
	}
	// Startup probing and headless resolution already give exact basenames
	// priority over fuzzy path matches. Preserve that same candidate set when
	// the no-ignore picker opens; otherwise a query such as "src" unexpectedly
	// shows thousands of weaker dependency-path matches.
	if r.NoIgnore && query != "" {
		if exact := exactBasenameTargetMatches(allTargets, query); len(exact) > 0 {
			allTargets = exact
		}
	}
	options := make([]TargetMatch, 0, len(allTargets))
	for _, target := range allTargets {
		if CoveredBySelection(target.Path, selectedPaths) {
			continue
		}
		options = append(options, target)
	}
	if includeCopyAll {
		options = append([]TargetMatch{{Path: ".", Kind: "all"}}, options...)
	}
	if len(options) == 0 {
		return nil, ErrSelectionCancelled
	}
	if match, ok := exactInteractiveTargetMatch(options, query); ok {
		return []TargetMatch{match}, nil
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}

	labels, index := TargetMatchLabels(options)
	header := TargetPickerHeaderWithEscHint(prompt, r.StartupEscHint)
	revealedHeader := styledTargetPickerHeaderWithSymbols(prompt, r.StartupEscHint, platform.ActivePalette())
	selectedLabels, err := chooseManyTargetMatchesWithFzfChrome(path, query, prompt, header, revealedHeader, labels, false, r.WithBinaries)
	if err != nil {
		return nil, err
	}

	selected := make([]TargetMatch, 0, len(selectedLabels))
	for _, key := range selectedLabels {
		match, ok := index[key]
		if ok {
			if match.Kind == "all" {
				return []TargetMatch{match}, nil
			}
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, ErrSelectionCancelled
	}
	return selected, nil
}

func exactInteractiveTargetMatch(options []TargetMatch, query string) (TargetMatch, bool) {
	if !shouldAutoAcceptInteractiveQuery(query) {
		return TargetMatch{}, false
	}
	return exactTargetPathMatch(options, query)
}

func exactTargetPathMatch(options []TargetMatch, query string) (TargetMatch, bool) {
	trimmed := strings.TrimSuffix(query, "/")
	want := normalizeRelPath(trimmed)
	if want == "" || want == "." {
		return TargetMatch{}, false
	}
	for _, option := range options {
		if option.Kind == "all" {
			continue
		}
		if option.Path == want {
			if strings.HasSuffix(query, "/") && option.Kind != "dir" {
				continue
			}
			return option, true
		}
	}
	return TargetMatch{}, false
}

func shouldAutoAcceptInteractiveQuery(query string) bool {
	trimmed := strings.TrimSuffix(query, "/")
	if trimmed == "" || trimmed == "." {
		return false
	}
	return strings.Contains(trimmed, "/")
}

func NormalizeInteractivePickerQuery(query string) string {
	if strings.TrimSpace(query) == "*" {
		return ""
	}
	return query
}

func (r *Resolver) InteractiveQueryCoveredBySelection(query string, selectedPaths []string) (bool, error) {
	query = NormalizeInteractivePickerQuery(query)
	if query == "" || len(selectedPaths) == 0 {
		return false, nil
	}
	if hasGlobChars(query) {
		return false, nil
	}
	if SelectionContainsAll(selectedPaths) {
		return true, nil
	}

	normalized := normalizeRelPath(query)
	if normalized != "" && normalized != "." {
		exists, err := r.TargetPathExists(normalized)
		if err != nil {
			return false, err
		}
		if exists && CoveredBySelection(normalized, selectedPaths) {
			return true, nil
		}
	}
	if strings.Contains(normalized, "/") {
		return false, nil
	}

	sawMatch := false

	if err := r.BuildVisibleDirIndex(); err != nil {
		return false, err
	}
	for _, rel := range r.VisibleDirs.Dirs {
		if path.Base(rel) != normalized {
			continue
		}
		sawMatch = true
		if !CoveredBySelection(rel, selectedPaths) {
			return false, nil
		}
	}

	if err := r.BuildVisibleFileList(); err != nil {
		return false, err
	}
	for _, entry := range r.VisibleFileList {
		base := path.Base(entry.RelPath)
		if base != normalized && strings.TrimSuffix(base, path.Ext(base)) != normalized {
			continue
		}
		sawMatch = true
		if !CoveredBySelection(entry.RelPath, selectedPaths) {
			return false, nil
		}
	}
	return sawMatch, nil
}

func FilterRedundantTargetMatches(candidates []TargetMatch, selectedPaths []string) []TargetMatch {
	if len(selectedPaths) == 0 {
		return candidates
	}
	filtered := make([]TargetMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if CoveredBySelection(candidate.Path, selectedPaths) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func CoveredBySelection(path string, selectedPaths []string) bool {
	for _, selected := range selectedPaths {
		selected = normalizeRelPath(selected)
		switch {
		case selected == ".":
			return true
		case path == selected:
			return true
		case selected != "" && strings.HasPrefix(path, selected+"/"):
			return true
		}
	}
	return false
}

func SelectionContainsAll(selectedPaths []string) bool {
	for _, selected := range selectedPaths {
		if normalizeRelPath(selected) == "." {
			return true
		}
	}
	return false
}

func chooseDirectoryMatch(cfg command.Invocation, needle, currentRel string, matches []string, stderr io.Writer, colors platform.Palette) ([]string, error) {
	if !canPromptForChoice(cfg) {
		return nil, headlessDirectoryAmbiguityError(needle, currentRel, matches)
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithTypedFzf(path, needle, "dir> ", matches, treeTargetKindDir, treeTargetStateOK, false, cfg.WithBinaries)
}

func chooseFileMatch(cfg command.Invocation, needle, currentRel string, matches []string, includeTarget bool, stderr io.Writer, colors platform.Palette) ([]string, error) {
	if !canPromptForChoice(cfg) {
		return nil, headlessFileAmbiguityError(needle, currentRel, matches)
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithTypedFzf(path, needle, "file> ", matches, treeTargetKindFile, treeTargetStateText, includeTarget, cfg.WithBinaries)
}

func chooseTargetMatch(cfg command.Invocation, needle string, matches []TargetMatch, stderr io.Writer, colors platform.Palette) ([]TargetMatch, error) {
	if !canPromptForChoice(cfg) {
		return nil, headlessTargetAmbiguityError(needle, matches)
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	labels, index := TargetMatchLabels(matches)
	selectedKeys, err := chooseManyTargetMatchesWithFzf(path, needle, "select> ", labels, false, cfg.WithBinaries)
	if err != nil {
		return nil, err
	}
	selected := make([]TargetMatch, 0, len(selectedKeys))
	for _, key := range selectedKeys {
		match, ok := index[key]
		if ok {
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, ErrSelectionCancelled
	}
	return selected, nil
}

func chooseFuzzyTargetMatches(cfg command.Invocation, needle string, matches []TargetMatch, stderr io.Writer, colors platform.Palette) ([]TargetMatch, error) {
	dirs := make([]string, 0, len(matches))
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		switch match.Kind {
		case treeTargetKindDir:
			dirs = append(dirs, match.Path)
		case treeTargetKindFile:
			files = append(files, match.Path)
		}
	}

	switch {
	case len(files) == 0:
		selected, err := chooseDirectoryMatch(cfg, needle, ".", dirs, stderr, colors)
		if err != nil {
			return nil, err
		}
		return selectedTargetMatches(selected, matches), nil
	case len(dirs) == 0:
		selected, err := chooseFileMatch(cfg, needle, ".", files, false, stderr, colors)
		if err != nil {
			return nil, err
		}
		return selectedTargetMatches(selected, matches), nil
	default:
		return chooseTargetMatch(cfg, needle, matches, stderr, colors)
	}
}

func selectedTargetMatches(paths []string, candidates []TargetMatch) []TargetMatch {
	byPath := make(map[string]TargetMatch, len(candidates))
	for _, candidate := range candidates {
		byPath[normalizeRelPath(candidate.Path)] = candidate
	}
	out := make([]TargetMatch, 0, len(paths))
	for _, selected := range paths {
		if candidate, ok := byPath[normalizeRelPath(selected)]; ok {
			out = append(out, candidate)
		}
	}
	return out
}

const HeadlessCandidateListLimit = 10

func FormatHeadlessCandidateList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	limit := len(items)
	if limit > HeadlessCandidateListLimit {
		limit = HeadlessCandidateListLimit
	}
	var b strings.Builder
	b.WriteString("\n  Matches:")
	for _, item := range items[:limit] {
		fmt.Fprintf(&b, "\n    - %s", item)
	}
	if len(items) > limit {
		fmt.Fprintf(&b, "\n    - ... and %d more", len(items)-limit)
	}
	return b.String()
}

func headlessDirectoryAmbiguityError(needle, currentRel string, matches []string) error {
	var b strings.Builder
	if currentRel == "." {
		fmt.Fprintf(&b, "Error: Multiple directories match %s in headless mode (--headless).", SingleQuoted(needle))
	} else {
		fmt.Fprintf(&b, "Error: Multiple directories match %s in %s in headless mode (--headless).", SingleQuoted(needle), currentRel)
	}
	b.WriteString(FormatHeadlessCandidateList(matches))
	b.WriteString("\n  Use a more specific path segment to disambiguate.")
	return errors.New(b.String())
}

func headlessFileAmbiguityError(needle, currentRel string, matches []string) error {
	var b strings.Builder
	if currentRel == "." {
		fmt.Fprintf(&b, "Error: Multiple files match %s in headless mode (--headless).", SingleQuoted(needle))
	} else {
		fmt.Fprintf(&b, "Error: Multiple files match %s in %s in headless mode (--headless).", SingleQuoted(needle), currentRel)
	}
	b.WriteString(FormatHeadlessCandidateList(matches))
	b.WriteString("\n  Use a more specific name or path to disambiguate.")
	return errors.New(b.String())
}

func headlessTargetAmbiguityError(needle string, matches []TargetMatch) error {
	items := make([]string, 0, len(matches))
	for _, match := range matches {
		items = append(items, fmt.Sprintf("[%s] %s", match.Kind, match.Path))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Error: Multiple files and directories match %s in headless mode (--headless).", SingleQuoted(needle))
	b.WriteString(FormatHeadlessCandidateList(items))
	b.WriteString("\n  Use a more specific name or path to disambiguate.")
	return errors.New(b.String())
}

func FzfBinary() (string, bool) {
	return platform.BundledToolBinary("CATCLIP_FZF", "fzf")
}

func FuzzyResolverBinary() (string, error) {
	path, ok := FzfBinary()
	if ok {
		return path, nil
	}
	return "", fmt.Errorf("Error: this catclip install is missing bundled fzf.\n  Reinstall catclip with its packaged tools; runtime does not fall back to arbitrary PATH copies.")
}

func fuzzyFilterCandidates(query string, candidates []string) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return runFzfFilter(path, query, candidates)
}

func runFzfFilter(bin, query string, candidates []string) ([]string, error) {
	return runFzfFilterLines(bin, query, formatFzfCandidates(candidates, "", ""))
}

func runFzfFilterLines(bin, query string, lines []string) ([]string, error) {
	return picker.Filter(bin, query, lines)
}

// TargetMatchLabels keeps presentation metadata in column 1 and the actual
// cwd-relative path in column 2. Search only the path: words such as
// "file" and ".gitignore" describe a row but are not target names and must
// not make an unrelated query match.
func runFzfTargetFilterLines(bin, query string, lines []string) ([]string, error) {
	return picker.FilterByNth(bin, query, lines, "2")
}

// FzfDiffFilePreviewCommand is intentionally not checkpoint-backed: diff
// pickers preview one focused file via --internal-file-preview, so they do not
// rerun project discovery for a tree payload.
func FzfDiffFilePreviewCommand(currentArgs []string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-file-preview", "--internal-file-path", "{3}"}
	for _, arg := range currentArgs {
		parts = append(parts, ShellQuoteArg(arg))
	}
	parts = append(parts, "--only", "{+2}")
	return strings.Join(parts, " ")
}

func ChooseContentMatchesWithFzfAndEscHint(query string, currentArgs []string, flag string, escHint string) (fzfChooseResult, error) {
	bin, err := FuzzyResolverBinary()
	if err != nil {
		return fzfChooseResult{}, err
	}

	command, checkpointPath, cleanup := fzfCheckpointContentMatchListCommand(currentArgs, flag)
	defer cleanup()
	if command == "" {
		return fzfChooseResult{}, ErrSelectionCancelled
	}
	searchingPreviewCommand := FzfContentSearchingPreviewCommand(flag)

	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         "match> ",
		WithNth:        "1",
		Nth:            "1",
		Header:         ContentMatchPickerHeaderWithEscHint(flag, escHint),
		PreviewCommand: FzfContentPreviewCommand(flag, checkpointPath),
		PreviewWindow:  ContentMatchPreviewWindow(flag),
		Disabled:       true,
		Multi:          true,
		PrintQuery:     true,
		Bindings:       append(contentMatchReloadBindings(command, searchingPreviewCommand), MultiSelectPickerBindings()...),
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return fzfChooseResult{}, ErrSelectionCancelled
	}
	if err != nil {
		return fzfChooseResult{}, err
	}
	if strings.TrimSpace(result.Query) == "" && result.Key == "" && len(result.Matches) == 0 {
		return fzfChooseResult{}, ErrSelectionCancelled
	}
	return fzfChooseResult{Query: result.Query, Key: result.Key, Matches: result.Matches}, nil
}

func contentMatchReloadBindings(reloadCommand, searchingPreviewCommand string) []string {
	if searchingPreviewCommand == "" {
		return []string{"start:reload:" + reloadCommand, "change:reload:" + reloadCommand}
	}
	return []string{
		"start:preview<" + searchingPreviewCommand + ">+reload<" + reloadCommand + ">",
		"change:preview<" + searchingPreviewCommand + ">+reload<" + reloadCommand + ">",
	}
}

func chooseManyWithTypedFzf(bin, query, prompt string, candidates []string, kind, state string, includeTarget, withBinaries bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1,2", "", "", FzfPreviewCommand(includeTarget, withBinaries), formatFzfCandidates(candidates, kind, state))
}

func chooseManyTargetMatchesWithFzfChrome(bin, query, prompt, header, revealedHeader string, candidates []string, includeTarget, withBinaries bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "2", "1,2", header, revealedHeader, FzfPreviewCommand(includeTarget, withBinaries), candidates)
}

func chooseManyTargetMatchesWithFzf(bin, query, prompt string, candidates []string, includeTarget, withBinaries bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "2", "1,2", "", "", FzfPreviewCommand(includeTarget, withBinaries), candidates)
}

type fzfChooseResult struct {
	Query   string
	Key     string
	Matches []string
}

func chooseManyWithFzfOptions(bin, query, prompt, nth, withNth, header, revealedHeader, previewCommand string, candidates []string) ([]string, error) {
	platform.StopActiveSpinner()
	req := picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        withNth,
		Nth:            nth,
		Header:         header,
		PreviewCommand: previewCommand,
		Multi:          true,
		Bindings:       MultiSelectPickerBindings(),
		Lines:          candidates,
	}
	if revealedHeader != "" {
		req.Bindings = append(req.Bindings, targetPickerRevealHeaderBinding(revealedHeader))
	}
	result, err := picker.Run(bin, themedFzfRequest(req))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, ErrSelectionCancelled
	}
	if err != nil {
		return nil, err
	}
	if len(result.Matches) == 0 {
		return nil, ErrSelectionCancelled
	}
	return result.Matches, nil
}

func targetPickerRevealHeaderBinding(header string) string {
	return picker.RevealHeaderAfterQueryChangeBinding(header)
}
