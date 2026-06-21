package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

const sizePickerNoMaxToken = "none"

type sizePickerCandidate struct {
	Token       string
	PreviewStem string
	Nums        []int
	Label       string
}

func resolveStartupSizeArgs(currentArgs []string) ([]string, bool, error) {
	return resolveStartupSizeArgsWithEscHint(currentArgs, "")
}

// resolveStartupSizeArgsWithEscHint drives the two-stage --size picker
// (MIN, then MAX) by looping over two single-purpose pickers. This is the
// reference shape rule 20 in RULES.md describes for multi-stage modifiers:
//
//   - Esc in MIN returns ErrSelectionCancelled, exiting the whole --size
//     flow with no change to currentArgs.
//   - Esc in MAX `continue`s the loop and re-opens MIN from scratch; the
//     just-chosen MIN is a Go local that goes out of scope, so there is no
//     stored state to "forget."
//   - currentArgs only gains a `--size` token after BOTH pickers commit
//     (the append at the bottom of the loop). A half-committed run is a
//     true no-op against the chain — undo cleanly returns to the modifier
//     menu without leaving partial state.
//
// The MAX picker bakes the just-chosen min as literal text in its preview
// command parts (see chooseStartupMaxSize). When the loop iterates, a fresh
// MAX picker is built against whatever MIN is current — no cross-stage
// plumbing leaks between iterations.
func resolveStartupSizeArgsWithEscHint(currentArgs []string, escHint string) ([]string, bool, error) {
	view, entries, err := prepareStartupSizePicker(currentArgs)
	if err != nil {
		return nil, false, err
	}

	for {
		min, err := chooseStartupMinSizeWithEscHint(view, entries, escHint)
		if err != nil {
			return nil, true, err
		}
		max, hasMax, err := chooseStartupMaxSize(view, entries, min)
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			// Esc in MAX → loop back to MIN. See function docstring.
			continue
		}
		if err != nil {
			return nil, true, err
		}

		args := append(append([]string(nil), currentArgs...), "--size")
		switch {
		case min == 0 && !hasMax:
			return args, true, nil
		case hasMax:
			args = append(args, strconv.Itoa(min), strconv.Itoa(max))
		default:
			args = append(args, strconv.Itoa(min))
		}
		return args, true, nil
	}
}

func prepareStartupSizePicker(currentArgs []string) (resolvedScopeView, []discovery.Entry, error) {
	finishBench := platform.InternalBenchSpan("ui.size_picker.prepare",
		"argc", platform.InternalBenchInt(len(currentArgs)),
	)
	finishViewBench := platform.InternalBenchSpan("ui.size_picker.prepare.resolve_scope")
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	finishViewBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return resolvedScopeView{}, nil, err
	}
	if len(view.Entries) == 0 {
		finishBench("err", "false", "cancelled", "true", "reason", "empty")
		return resolvedScopeView{}, nil, discovery.ErrSelectionCancelled
	}
	finishSizeBench := platform.InternalBenchSpan("ui.size_picker.prepare.ensure_sizes",
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	entries, err := discovery.EnsureEntrySizes(view.Entries, view.Invocation.WorkingDir)
	finishSizeBench("err", platform.InternalBenchError(err))
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return resolvedScopeView{}, nil, err
	}
	view.Entries = entries
	finishBench(
		"err", "false",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	return view, entries, nil
}

func chooseStartupMinSizeWithEscHint(view resolvedScopeView, entries []discovery.Entry, escHint string) (int, error) {
	candFinish := platform.InternalBenchSpan("ui.size_picker.compute_min_candidates",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	candidates := startupMinSizeCandidates(entries)
	candFinish("candidates", platform.InternalBenchInt(len(candidates)))
	if len(candidates) == 0 {
		return 0, discovery.ErrSelectionCancelled
	}
	// MIN picker: `--size {4}` where {4} is the bucket's KiB value (including
	// "0" for the no-minimum row). ApplySizeStage with nums=[0] keeps every
	// file (minBytes = 0), so `--size 0` semantically equals "no minimum"
	// for the preview.
	previewCmd, tmpdir := buildSizePickerPreview(view, []string{"--size", "{4}"})
	if tmpdir != "" {
		defer os.RemoveAll(tmpdir)
	}

	selected, err := chooseSizeWithFzf("min-size> ", sizePickerMinHeaderWithEscHint(escHint), sizePickerLines(candidates, sizePickerMinColumn), previewCmd)
	if err != nil {
		return 0, err
	}
	n, err := parseSizePickerToken(selected)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func chooseStartupMaxSize(view resolvedScopeView, entries []discovery.Entry, min int) (int, bool, error) {
	candFinish := platform.InternalBenchSpan("ui.size_picker.compute_max_candidates",
		"entries", platform.InternalBenchInt(len(entries)),
		"min", platform.InternalBenchInt(min),
	)
	candidates := startupMaxSizeCandidates(entries, min)
	candFinish("candidates", platform.InternalBenchInt(len(candidates)))
	if len(candidates) == 0 {
		return 0, false, discovery.ErrSelectionCancelled
	}
	// MAX picker: `--size <min> {4}` where {4} is the bucket's max KiB value.
	// The chosen min is baked in as literal text (it's fixed for this picker).
	// The "[no maximum]" row uses a very large sentinel (see
	// sizePickerMaxColumn) so `--size <min> <huge>` semantically equals
	// "every file >= min" — the same set "no max" would actually select.
	previewCmd, tmpdir := buildSizePickerPreview(view, []string{"--size", strconv.Itoa(min), "{4}"})
	if tmpdir != "" {
		defer os.RemoveAll(tmpdir)
	}

	selected, err := chooseSizeWithFzf("max-size> ", sizePickerMaxHeader(), sizePickerLines(candidates, sizePickerMaxColumn), previewCmd)
	if err != nil {
		return 0, false, err
	}
	selected = strings.TrimSpace(selected)
	if selected == sizePickerNoMaxToken {
		return 0, false, nil
	}
	n, err := parseSizePickerToken(selected)
	if err != nil {
		return 0, false, err
	}
	if err := cli.ValidateSizeBounds([]int{min, n}); err != nil {
		return 0, false, err
	}
	return n, true, nil
}

func startupMinSizeCandidates(entries []discovery.Entry) []sizePickerCandidate {
	thresholds := map[int]struct{}{0: {}}
	for _, entry := range entries {
		kib := int(entry.SizeBytes / 1024)
		if kib > 0 {
			thresholds[kib] = struct{}{}
		}
	}
	values := sortedSizeThresholds(thresholds)
	out := make([]sizePickerCandidate, 0, len(values))
	for _, value := range values {
		nums := []int{value}
		if value == 0 {
			nums = nil
		}
		out = append(out, sizePickerCandidate{
			Token:       strconv.Itoa(value),
			PreviewStem: strconv.Itoa(value),
			Nums:        nums,
			Label:       minSizeLabel(value, countEntriesAtLeastSize(entries, value)),
		})
	}
	return out
}

func startupMaxSizeCandidates(entries []discovery.Entry, min int) []sizePickerCandidate {
	thresholds := map[int]struct{}{}
	for _, entry := range entries {
		kib := discovery.SizeBucketKiB(entry.SizeBytes)
		if kib < 1 || kib < min {
			continue
		}
		thresholds[kib] = struct{}{}
	}

	out := []sizePickerCandidate{{
		Token:       sizePickerNoMaxToken,
		PreviewStem: sizePickerNoMaxToken,
		Nums:        sizeNumsForMinOnly(min),
		Label:       "[no maximum]",
	}}
	for _, value := range sortedSizeThresholds(thresholds) {
		nums := []int{min, value}
		if err := cli.ValidateSizeBounds(nums); err != nil {
			continue
		}
		count := countEntriesBetweenSize(entries, min, value)
		if count == 0 {
			continue
		}
		out = append(out, sizePickerCandidate{
			Token:       strconv.Itoa(value),
			PreviewStem: strconv.Itoa(value),
			Nums:        nums,
			Label:       maxSizeLabel(value, count),
		})
	}
	return out
}

func sizeNumsForMinOnly(min int) []int {
	if min == 0 {
		return nil
	}
	return []int{min}
}

func sortedSizeThresholds(values map[int]struct{}) []int {
	out := make([]int, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sortInts(out)
	return out
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func countEntriesAtLeastSize(entries []discovery.Entry, min int) int {
	minBytes := int64(min) * 1024
	count := 0
	for _, entry := range entries {
		if entry.SizeBytes >= minBytes {
			count++
		}
	}
	return count
}

func countEntriesBetweenSize(entries []discovery.Entry, min, max int) int {
	minBytes := int64(min) * 1024
	maxBytes := int64(max) * 1024
	count := 0
	for _, entry := range entries {
		if entry.SizeBytes >= minBytes && entry.SizeBytes <= maxBytes {
			count++
		}
	}
	return count
}

func minSizeLabel(min, count int) string {
	if min == 0 {
		return fmt.Sprintf("[no minimum] sort all files by size (%d files)", count)
	}
	return fmt.Sprintf("keep files >= %d KiB (%d files)", min, count)
}

func maxSizeLabel(max, count int) string {
	return fmt.Sprintf("keep files <= %d KiB (%d files)", max, count)
}

func sizePickerLines(candidates []sizePickerCandidate, column4 func(sizePickerCandidate) string) []string {
	lines := make([]string, 0, len(candidates))
	for _, c := range candidates {
		// Column 4 is the single-token value fzf substitutes into `{4}` in
		// the per-focus preview command (see buildSizePickerPreview). The
		// MIN and MAX pickers format this differently (sizePickerMinColumn
		// / sizePickerMaxColumn) because their preview commands have
		// different shapes. Invisible to the user (chooseSizeWithFzf uses
		// --with-nth "1,3") and ignored by parseSizePickerToken (which only
		// reads column 1).
		lines = append(lines, strings.Join([]string{c.Token, c.PreviewStem, c.Label, column4(c)}, "\t"))
	}
	return lines
}

func chooseSizeWithFzf(prompt, header string, lines []string, previewCommand string) (string, error) {
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return "", err
	}

	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:         prompt,
		WithNth:        "1,3",
		Nth:            "1",
		Header:         header,
		PreviewCommand: previewCommand,
		NoSort:         true,
		Exact:          true,
		Lines:          lines,
	}))
	if err == nil {
		if len(result.Matches) == 0 {
			return "", discovery.ErrSelectionCancelled
		}
		return strings.TrimSpace(result.Matches[0]), nil
	}
	if err == picker.ErrSelectionCancelled {
		return "", discovery.ErrSelectionCancelled
	}
	return "", err
}

func parseSizePickerToken(selected string) (int, error) {
	value := selected
	if tab := strings.IndexByte(selected, '\t'); tab >= 0 {
		value = selected[:tab]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, discovery.ErrSelectionCancelled
	}
	return cli.ParseSizeBoundToken(value)
}

func sizePickerMinHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Pick the minimum file size.",
		"Values are KiB. Choose 0 for no lower bound.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

func sizePickerMaxHeader() string {
	return discovery.PickerHeader(
		"Pick the maximum file size.",
		"[no maximum] keeps the range open-ended.",
		"[Up/Down] move  [Enter] confirm  [Esc] back to minimum",
	)
}

// buildSizePickerPreview writes a single shared checkpoint and returns a
// preview command. fzf invokes the command per focus; the `{4}` placeholder
// in tailArgs is substituted with the focused row's hidden column-4 value
// (see sizePickerLines + sizePicker*Column). The `--size` flag and any
// always-present args (like the chosen min for the MAX picker) live as
// LITERAL TEXT in tailArgs — fzf single-quotes substituted values, so the
// flag name must not be inside the substituted column.
//
// Item 5 redesign: replaces the previous loop that pre-wrote one JSON
// payload per bucket (139+140 = 279 writes on vscode-main, ~29 s of
// Defender-amplified wall-clock on Windows). Per-focus child cost ~100 ms;
// total interactive cost amortizes to a few hundred ms across the buckets
// the user actually inspects.
func buildSizePickerPreview(view resolvedScopeView, tailArgs []string) (cmd string, tmpdir string) {
	buildFinish := platform.InternalBenchSpan("ui.size_picker.build_preview",
		"shape", "lazy_checkpoint",
	)
	var err error
	defer func() {
		buildFinish("err", platform.InternalBenchError(err))
	}()

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", ""
	}
	tmpdir, err = os.MkdirTemp("", "catclip-size-*")
	if err != nil {
		return "", ""
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, view.Entries))
		if err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", ""
		}
	}
	if err = discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
	}); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", ""
	}

	parts := []string{
		discovery.ShellQuoteArg(self),
		"--quiet",
		"--internal-tree-preview",
		"--internal-prediscovered",
		discovery.ShellQuoteArg(checkpointPath),
	}
	parts = append(parts, tailArgs...)
	return strings.Join(parts, " "), tmpdir
}

// sizePickerNoMaxSentinel is a very large KiB value used in the MAX-picker's
// hidden column 4 when the user's row is "[no maximum]". The preview child
// gets `--size <min> <sentinel>`, which ValidateSizeBounds accepts (max >=
// min, max != 0) and ApplySizeStage applies as "every file >= min" — the
// same set the actual "no max" choice would select. Only used in previews;
// the final emit uses the user's literal "none" selection through
// chooseStartupMaxSize, which returns hasMax=false.
const sizePickerNoMaxSentinel = "999999999"

// sizePickerMinColumn formats the hidden column-4 value for the MIN picker.
// Always the raw KiB integer ("0" for no-minimum row), substituted into
// `--size {4}`.
func sizePickerMinColumn(c sizePickerCandidate) string {
	return c.Token
}

// sizePickerMaxColumn formats the hidden column-4 value for the MAX picker.
// For numeric rows: the max KiB. For the "[no maximum]" row: the large
// sentinel above. Substituted into `--size <min> {4}`.
func sizePickerMaxColumn(c sizePickerCandidate) string {
	if c.Token == sizePickerNoMaxToken {
		return sizePickerNoMaxSentinel
	}
	return c.Token
}

func startupSizeBoundsFromRemaining(remaining []string) ([]int, int, error) {
	nums := make([]int, 0, 2)
	consumed := 0
	for consumed < len(remaining) {
		if cli.IsModifierBoundaryToken(remaining[consumed]) {
			break
		}
		if len(nums) == 2 {
			return nil, consumed, cli.SizeTooManyValuesError(remaining[consumed])
		}
		n, err := cli.ParseSizeBoundToken(remaining[consumed])
		if err != nil {
			return nil, consumed, err
		}
		nums = append(nums, n)
		consumed++
	}
	if err := cli.ValidateSizeBounds(nums); err != nil {
		return nil, consumed, err
	}
	return nums, consumed, nil
}
