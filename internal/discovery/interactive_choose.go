package discovery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
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
	// A bare target picker already has to classify the complete visible file
	// universe. Queue exact-size capture with that classifier, then begin its
	// metadata reads after classification instead of in the first preview child.
	r.CaptureTargetPreviewSizes = query == ""
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
	allTargets, previewInventory := targetPickerMatchSets(allTargets, r.NoIgnore, query)
	r.beginTargetPreviewGeneration()
	r.targetPreviewInventory = append([]TargetMatch(nil), previewInventory...)
	r.targetPreviewInventoryOK = true
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
	sizeCapture := r.ensureTargetPreviewSizeCapture(previewInventory)
	// The picker-specific setup also stops the capture so it can join its
	// sidecar writer before removing the session directory. This outer guard
	// covers failures that happen before that setup exists (missing fzf or an
	// unavailable temporary directory). Stop is idempotent.
	defer sizeCapture.Stop()

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}

	labels, index := TargetMatchLabels(options)
	header := TargetPickerHeaderWithEscHint(prompt, r.StartupEscHint)
	revealedHeader := styledTargetPickerHeaderWithSymbols(prompt, r.StartupEscHint, platform.ActivePalette())
	selectedLabels, inventoryLease, err := chooseManyTargetMatchesWithFzfChrome(path, query, prompt, header, revealedHeader, labels, previewInventory, r.GitCtx, sizeCapture, false, r.WithBinaries)
	if err != nil {
		return nil, err
	}
	// Until a valid target row is committed, the lease remains responsible for
	// cleanup. This makes an immediate Enter safe: picker.Run may return before
	// any preview child starts, but the synchronously written inventory is no
	// longer removed by the generic picker defer before ownership reaches the
	// resolver.
	defer inventoryLease.Release()

	selected := make([]TargetMatch, 0, len(selectedLabels))
	for _, key := range selectedLabels {
		match, ok := index[key]
		if ok {
			if match.Kind == "all" {
				inventoryLease.TransferTo(r)
				return []TargetMatch{match}, nil
			}
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, ErrSelectionCancelled
	}
	inventoryLease.TransferTo(r)
	return selected, nil
}

func targetPickerMatchSets(allTargets []TargetMatch, noIgnore bool, query string) (candidates, previewInventory []TargetMatch) {
	// Candidate narrowing changes only what fzf offers. The preview inventory
	// must retain the complete classified universe so a selected directory can
	// still project all of its descendant files.
	previewInventory = allTargets
	candidates = allTargets
	if noIgnore && query != "" {
		if exact := exactBasenameTargetMatches(allTargets, query); len(exact) > 0 {
			candidates = exact
		}
	}
	return candidates, previewInventory
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
	var matchMemo []byte
	if checkpointPath != "" {
		matchMemo, _ = os.ReadFile(filepath.Join(filepath.Dir(checkpointPath), ContentMatchMemoFilename))
	}
	return fzfChooseResult{Query: result.Query, Key: result.Key, Matches: result.Matches, MatchMemo: matchMemo}, nil
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
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1,2", "", "", staticPreviewCommand(FzfPreviewCommand(includeTarget, withBinaries)), formatFzfCandidates(candidates, kind, state))
}

type targetPreviewInventoryLease struct {
	sessionDir    string
	inventoryPath string
	baseComplete  bool
}

func (l *targetPreviewInventoryLease) Release() {
	if l == nil || l.sessionDir == "" {
		return
	}
	_ = os.RemoveAll(l.sessionDir)
	l.sessionDir = ""
	l.inventoryPath = ""
	l.baseComplete = false
}

func (l *targetPreviewInventoryLease) TransferTo(resolver *Resolver) {
	if l == nil || resolver == nil || l.sessionDir == "" || l.inventoryPath == "" {
		return
	}
	resolver.retainTargetPreviewInventory(l.sessionDir, l.inventoryPath, l.baseComplete)
	l.sessionDir = ""
	l.inventoryPath = ""
	l.baseComplete = false
}

func chooseManyTargetMatchesWithFzfChrome(bin, query, prompt, header, revealedHeader string, candidates []string, inventory []TargetMatch, gitCtx git.Context, sizeCapture *search.TextSizeCapture, includeTarget, withBinaries bool) ([]string, targetPreviewInventoryLease, error) {
	inventoryPath := ""
	baseComplete := false
	previewBuilder := func(sessionDir string) previewCommandSetup {
		inventoryPath = filepath.Join(sessionDir, "targets.bin")
		sizesPending := !sizeCapture.Complete()
		// Check completion before taking the snapshot. If capture finishes after
		// this check, the inventory may conservatively stay pending and the
		// sidecar writer will publish the same final snapshot. Checking in the
		// opposite order could mark an incomplete snapshot final.
		currentInventory := ApplyTargetPreviewSizes(inventory, sizeCapture.Snapshot())
		finishBench := platform.InternalBenchSpan("discovery.target_picker.write_preview_inventory",
			"matches", platform.InternalBenchInt(len(inventory)),
			"sizes_pending", platform.InternalBenchBool(sizesPending),
		)
		err := WriteTargetPreviewInventoryWithOptions(inventoryPath, gitCtx, currentInventory, TargetPreviewInventoryWriteOptions{
			SizesPending: sizesPending,
		})
		finishBench("err", platform.InternalBenchError(err))
		if err != nil {
			inventoryPath = ""
			baseComplete = false
			return previewCommandSetup{
				Command: FzfPreviewCommand(includeTarget, withBinaries),
				Cleanup: sizeCapture.Stop,
			}
		}
		if !sizesPending {
			baseComplete = true
			return previewCommandSetup{
				Command:                FzfPreviewCommandWithInventory(inventoryPath, withBinaries),
				Cleanup:                sizeCapture.Stop,
				RetainSessionOnSuccess: true,
			}
		}

		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			<-sizeCapture.Done()
			if sizeCapture.Cancelled() {
				// The picker is closing, so no future preview can consume a full
				// sidecar. Wake any current waiter without delaying the next
				// interactive screen on a large, now-useless serialization pass.
				_ = os.WriteFile(TargetPreviewSizedInventoryDonePath(inventoryPath), nil, 0o600)
				return
			}
			completed := ApplyTargetPreviewSizes(inventory, sizeCapture.Snapshot())
			finishFinal := platform.InternalBenchSpan("discovery.target_picker.write_sized_preview_inventory",
				"matches", platform.InternalBenchInt(len(inventory)),
			)
			writeErr := WriteTargetPreviewInventory(
				TargetPreviewSizedInventoryPath(inventoryPath),
				gitCtx,
				completed,
			)
			finishFinal("err", platform.InternalBenchError(writeErr))
			// The done marker is best-effort. It prevents a child waiting on a
			// failed sized-inventory write from blocking for the picker lifetime.
			_ = os.WriteFile(TargetPreviewSizedInventoryDonePath(inventoryPath), nil, 0o600)
		}()
		return previewCommandSetup{
			Command:                FzfPreviewCommandWithInventory(inventoryPath, withBinaries),
			RetainSessionOnSuccess: true,
			Cleanup: func() {
				sizeCapture.Stop()
				<-writerDone
			},
		}
	}
	result, err := chooseManyWithFzfOptionsResult(bin, query, prompt, "2", "1,2", header, revealedHeader, previewBuilder, candidates)
	if err != nil {
		return nil, targetPreviewInventoryLease{}, err
	}
	lease := targetPreviewInventoryLease{}
	if result.PreviewSession != "" && inventoryPath != "" {
		lease = targetPreviewInventoryLease{
			sessionDir:    result.PreviewSession,
			inventoryPath: inventoryPath,
			baseComplete:  baseComplete,
		}
	}
	return result.Matches, lease, nil
}

func chooseManyTargetMatchesWithFzf(bin, query, prompt string, candidates []string, includeTarget, withBinaries bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "2", "1,2", "", "", staticPreviewCommand(FzfPreviewCommand(includeTarget, withBinaries)), candidates)
}

type fzfChooseResult struct {
	Query   string
	Key     string
	Matches []string
	// MatchMemo is the final reload child's serialized content-match memo.
	// Callers must validate that its pattern equals Query before reuse.
	MatchMemo []byte
}

type previewCommandSetup struct {
	Command                string
	Cleanup                func()
	RetainSessionOnSuccess bool
}

func chooseManyWithFzfOptions(bin, query, prompt, nth, withNth, header, revealedHeader string, previewCommandBuilder func(string) previewCommandSetup, candidates []string) ([]string, error) {
	result, err := chooseManyWithFzfOptionsResult(bin, query, prompt, nth, withNth, header, revealedHeader, previewCommandBuilder, candidates)
	// This compatibility wrapper returns only matches, so it cannot transfer a
	// retained session lease. Keep ownership local if a future builder enables
	// retention accidentally; the target picker uses the result-bearing form.
	if result.PreviewSession != "" {
		_ = os.RemoveAll(result.PreviewSession)
	}
	return result.Matches, err
}

type chooseManyResult struct {
	Matches        []string
	PreviewSession string
}

func chooseManyWithFzfOptionsResult(bin, query, prompt, nth, withNth, header, revealedHeader string, previewCommandBuilder func(string) previewCommandSetup, candidates []string) (chooseManyResult, error) {
	platform.StopActiveSpinner()
	previewSession, err := os.MkdirTemp("", "catclip-target-preview-")
	if err != nil {
		return chooseManyResult{}, err
	}
	retainPreviewSession := false
	defer func() {
		picker.StopPreviewProcess(previewSession, picker.TargetPreviewPIDFile)
		if !retainPreviewSession {
			_ = os.RemoveAll(previewSession)
		}
	}()
	setup := previewCommandSetup{}
	if previewCommandBuilder != nil {
		setup = previewCommandBuilder(previewSession)
	}
	if setup.Cleanup != nil {
		defer setup.Cleanup()
	}
	req := picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        withNth,
		Nth:            nth,
		Header:         header,
		PreviewCommand: setup.Command,
		Multi:          true,
		Bindings:       MultiSelectPickerBindings(),
		Lines:          candidates,
		Env:            environmentWithValue(picker.TargetPreviewSessionEnv, previewSession),
	}
	if revealedHeader != "" {
		req.Bindings = append(req.Bindings, targetPickerRevealHeaderBinding(revealedHeader))
	}
	result, err := picker.Run(bin, themedFzfRequest(req))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return chooseManyResult{}, ErrSelectionCancelled
	}
	if err != nil {
		return chooseManyResult{}, err
	}
	if len(result.Matches) == 0 {
		return chooseManyResult{}, ErrSelectionCancelled
	}
	retainedPath := ""
	if setup.RetainSessionOnSuccess {
		// Flip ownership before returning. Deferred preview-process shutdown still
		// runs, but directory deletion is now the caller's responsibility.
		retainPreviewSession = true
		retainedPath = previewSession
	}
	return chooseManyResult{Matches: result.Matches, PreviewSession: retainedPath}, nil
}

func staticPreviewCommand(command string) func(string) previewCommandSetup {
	return func(string) previewCommandSetup { return previewCommandSetup{Command: command} }
}

func environmentWithValue(name, value string) []string {
	prefix := name + "="
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, name) {
			continue
		}
		out = append(out, item)
	}
	return append(out, prefix+value)
}

func targetPickerRevealHeaderBinding(header string) string {
	return picker.RevealHeaderAfterQueryChangeBinding(header)
}
