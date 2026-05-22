package catclip

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
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
	depth, err := parseDepthToken(value)
	if err != nil {
		return 0, err
	}

	maxDepth, err := currentScopeMaxDepth(currentArgs)
	if err != nil {
		return 0, err
	}
	if maxDepth > 0 && depth > maxDepth {
		return 0, depthExceedsCurrentScopeError(depth, maxDepth)
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
	buckets := computeDepthBuckets(uniqueSortedRelPaths(view.Entries))
	if len(buckets) == 0 {
		return 0, false, errSelectionCancelled
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
	depth, err := parseDepthToken(selected)
	if err != nil {
		return 0, true, err
	}
	if depth > maxDepth {
		return 0, true, depthExceedsCurrentScopeError(depth, maxDepth)
	}
	return depth, true, nil
}

type scopeDepthInfo struct {
	MaxDepth int
	Buckets  []depthBucket
}

func currentScopeDepthInfo(currentArgs []string) (scopeDepthInfo, error) {
	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return scopeDepthInfo{}, err
	}
	buckets := computeDepthBuckets(relPaths)
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

func startupDepthPickerLines(buckets []depthBucket) []string {
	if len(buckets) == 0 {
		return nil
	}
	lines := make([]string, 0, len(buckets))
	for _, b := range buckets {
		value := strconv.Itoa(b.Depth)
		label := "keep files at depth <= " + value + " (" + strconv.Itoa(b.CumulativeCount) + " files)"
		lines = append(lines, strings.Join([]string{value, value, label}, "\t"))
	}
	return lines
}

func uniqueSortedRelPaths(entries []fileEntry) []string {
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
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return "", err
	}

	stopActiveSpinner()
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
			return "", errSelectionCancelled
		}
		return strings.TrimSpace(result.Matches[0]), nil
	}
	if err == picker.ErrSelectionCancelled {
		return "", errSelectionCancelled
	}
	return "", err
}

func depthPickerHeader() string {
	return depthPickerHeaderWithEscHint("")
}

func depthPickerHeaderWithEscHint(escHint string) string {
	return pickerHeader(
		"Pick maximum path depth.",
		"Depth counts path segments from the working directory root.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

// buildDepthPickerPreview pre-renders one tree payload per bucket into a
// tmpdir and returns the fzf preview command that reads the per-bucket file.
// Returns ("", "") on any failure (missing catclip-tree binary, mkdir failure,
// render failure for any bucket) — the picker still works, just without a
// preview. The caller owns the tmpdir lifetime via os.RemoveAll.
func buildDepthPickerPreview(view resolvedScopeView, buckets []depthBucket) (cmd string, tmpdir string) {
	treeBin, ok := treePreviewBinary()
	if !ok {
		return "", ""
	}
	tmpdir, err := os.MkdirTemp("", "catclip-depth-*")
	if err != nil {
		return "", ""
	}
	scopes := []evaluatedOutputScope{{Paths: view.Scope.Paths}}
	for _, b := range buckets {
		filtered, err := applyDepthStage(view.Entries, b.Depth)
		if err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", ""
		}
		scopes[0].Entries = filtered
		path := filepath.Join(tmpdir, strconv.Itoa(b.Depth)+".json")
		if err := writeDepthPayloadFile(path, view, scopes, filtered); err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", ""
		}
	}

	parts := []string{shellQuoteArg(treeBin)}
	parts = append(parts, fzfTreeRenderArgs()...)
	// Pass --input-dir and --input-stem as separate args so the {2}
	// substitution lives as a standalone token. POSIX sh and Windows cmd.exe
	// quote differently for placeholder substitutions; keeping {2} away
	// from any adjacent characters means catclip-tree assembles the final
	// path in Go (platform-neutral), not the shell. See
	// internal/tree/cli.go for the receiving side.
	parts = append(parts, "--input-dir", shellQuoteArg(tmpdir), "--input-stem", "{2}")
	return strings.Join(parts, " "), tmpdir
}

func writeDepthPayloadFile(path string, view resolvedScopeView, scopes []evaluatedOutputScope, entries []fileEntry) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	plan, err := buildOutputPlanForResolvedScopes(view.GitContext, []executionScope{view.Scope}, scopes, entries)
	if err != nil {
		_ = f.Close()
		return err
	}
	if err := encodeTreePayloadFromPlan(f, view.Render, view.GitContext, plan, nil); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
