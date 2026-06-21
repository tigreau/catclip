package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

func resolveStartupDepthArgs(currentArgs []string) ([]string, bool, error) {
	return resolveStartupDepthArgsWithEscHint(currentArgs, "")
}

func resolveStartupDepthArgsWithEscHint(currentArgs []string, escHint string) ([]string, bool, error) {
	depth, usedFzf, err := chooseStartupDepthWithEscHint(currentArgs, "", escHint)
	if err != nil {
		return nil, usedFzf, err
	}
	return append(append([]string(nil), currentArgs...), "--depth", strconv.Itoa(depth)), usedFzf, nil
}

func validateStartupDepthValue(currentArgs []string, value string) (int, error) {
	depth, err := discovery.ParseDepthToken(value)
	if err != nil {
		return 0, err
	}

	maxDepth, err := currentScopeMaxDepth(currentArgs)
	if err != nil {
		return 0, err
	}
	if maxDepth > 0 && depth > maxDepth {
		return 0, discovery.DepthExceedsCurrentScopeError(depth, maxDepth)
	}
	return depth, nil
}

func chooseStartupDepth(currentArgs []string, query string) (int, bool, error) {
	return chooseStartupDepthWithEscHint(currentArgs, query, "")
}

func chooseStartupDepthWithEscHint(currentArgs []string, query string, escHint string) (int, bool, error) {
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return 0, false, err
	}
	bucketFinish := platform.InternalBenchSpan("ui.depth_picker.compute_buckets",
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	buckets := discovery.ComputeDepthBuckets(uniqueSortedRelPaths(view.Entries))
	bucketFinish("buckets", platform.InternalBenchInt(len(buckets)))
	if len(buckets) == 0 {
		return 0, false, discovery.ErrSelectionCancelled
	}
	maxDepth := buckets[len(buckets)-1].Depth

	previewCmd, tmpdir := buildDepthPickerPreview(view, buckets)
	if tmpdir != "" {
		defer os.RemoveAll(tmpdir)
	}

	selected, err := chooseDepthWithFzf(query, startupDepthPickerLines(buckets), previewCmd, escHint)
	if err != nil {
		return 0, true, err
	}
	depth, err := discovery.ParseDepthToken(selected)
	if err != nil {
		return 0, true, err
	}
	if depth > maxDepth {
		return 0, true, discovery.DepthExceedsCurrentScopeError(depth, maxDepth)
	}
	return depth, true, nil
}

type scopeDepthInfo struct {
	MaxDepth int
	Buckets  []discovery.DepthBucket
}

func currentScopeDepthInfo(currentArgs []string) (scopeDepthInfo, error) {
	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return scopeDepthInfo{}, err
	}
	buckets := discovery.ComputeDepthBuckets(relPaths)
	maxDepth := 0
	if len(buckets) > 0 {
		maxDepth = buckets[len(buckets)-1].Depth
	}
	return scopeDepthInfo{MaxDepth: maxDepth, Buckets: buckets}, nil
}

func currentScopeMaxDepth(currentArgs []string) (int, error) {
	info, err := currentScopeDepthInfo(currentArgs)
	if err != nil {
		return 0, err
	}
	return info.MaxDepth, nil
}

func startupDepthPickerLines(buckets []discovery.DepthBucket) []string {
	if len(buckets) == 0 {
		return nil
	}
	lines := make([]string, 0, len(buckets))
	for _, b := range buckets {
		value := strconv.Itoa(b.Depth)
		label := "keep files at depth <= " + value + " (" + strconv.Itoa(b.CumulativeCount) + " files)"
		// Column 4 is the depth value fzf substitutes into `{4}` in the
		// per-focus preview command (see buildDepthPickerPreview). It must be
		// a single shell token because fzf single-quotes the substituted
		// value; the `--depth` flag itself lives as literal text in the
		// preview command. Hidden from display via --with-nth "1,3" in
		// chooseDepthWithFzf.
		lines = append(lines, strings.Join([]string{value, value, label, value}, "\t"))
	}
	return lines
}

func uniqueSortedRelPaths(entries []discovery.Entry) []string {
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.RelPath == "" {
			continue
		}
		if _, ok := seen[e.RelPath]; ok {
			continue
		}
		seen[e.RelPath] = struct{}{}
		out = append(out, e.RelPath)
	}
	sort.Strings(out)
	return out
}

func chooseDepthWithFzf(query string, lines []string, previewCommand string, escHint ...string) (string, error) {
	hint := ""
	if len(escHint) > 0 {
		hint = escHint[0]
	}
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return "", err
	}

	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          strings.TrimSpace(query),
		Prompt:         "depth> ",
		WithNth:        "1,3",
		Nth:            "1",
		Header:         depthPickerHeaderWithEscHint(hint),
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

func depthPickerHeader() string {
	return depthPickerHeaderWithEscHint("")
}

func depthPickerHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Pick maximum path depth.",
		"Depth counts path segments from the working directory root.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

// buildDepthPickerPreview writes a single shared checkpoint and returns a
// preview command that fzf invokes per focus. The per-row `--depth N` arg
// is encoded in a hidden 4th tab-separated column (see startupDepthPickerLines);
// fzf substitutes `{4}` into the command on each focus and the child applies
// the depth filter against the checkpoint in-memory.
//
// Item 5 redesign: replaces the previous loop that pre-wrote one JSON payload
// per depth bucket. Bucket counts are typically small (5–15), so the Windows
// gain is modest, but the design parity with size_picker keeps the per-bucket
// anti-pattern from re-emerging in either picker.
func buildDepthPickerPreview(view resolvedScopeView, buckets []discovery.DepthBucket) (cmd string, tmpdir string) {
	buildFinish := platform.InternalBenchSpan("ui.depth_picker.build_preview",
		"buckets", platform.InternalBenchInt(len(buckets)),
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
	tmpdir, err = os.MkdirTemp("", "catclip-depth-*")
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
		// `--depth` is literal text; `{4}` is the per-row depth integer (see
		// startupDepthPickerLines). fzf single-quotes substituted column
		// values, so the flag must NOT be inside the substituted column —
		// otherwise the shell would see `--depth 5` as one argument.
		"--depth",
		"{4}",
	}
	return strings.Join(parts, " "), tmpdir
}
