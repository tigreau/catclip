package catclip

import (
	"os"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
)

func resolveStartupDepthArgs(currentArgs []string) ([]string, bool, error) {
	depth, usedFzf, err := chooseStartupDepth(currentArgs, "")
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
	info, err := currentScopeDepthInfo(currentArgs)
	if err != nil {
		return 0, false, err
	}
	if info.MaxDepth <= 0 {
		return 0, false, errSelectionCancelled
	}

	selected, err := chooseDepthWithFzf(query, startupDepthPickerLines(info.Buckets), depthPickerPreviewCommand(currentArgs))
	if err != nil {
		return 0, true, err
	}
	depth, err := parseDepthToken(selected)
	if err != nil {
		return 0, true, err
	}
	if depth > info.MaxDepth {
		return 0, true, depthExceedsCurrentScopeError(depth, info.MaxDepth)
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

func chooseDepthWithFzf(query string, lines []string, previewCommand string) (string, error) {
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
		Header:         depthPickerHeader(),
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
	return pickerHeader(
		"Pick maximum path depth.",
		"Depth counts path segments from the working directory root.",
		"[Up/Down] move  [Enter] confirm  [Esc] cancel",
	)
}

func depthPickerPreviewCommand(currentArgs []string) string {
	treeBin, ok := treePreviewBinary()
	if !ok {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	cfg, err := parseArgs(currentArgs)
	if err != nil {
		return ""
	}
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return ""
	}
	scopeArgs := canonicalScopeArgs(executionScopeFromCommandScopeSpec(scopeSpecs[len(scopeSpecs)-1]))
	if len(scopeArgs) == 0 {
		return ""
	}

	commandParts := []string{shellQuoteArg(self), "--quiet", "--internal-tree-payload"}
	for _, arg := range scopeArgs {
		commandParts = append(commandParts, arg)
	}
	command := shellSetCommand(commandParts)

	script := []string{
		`depth_value={2};`,
		command + ";",
		`if [ -n "$depth_value" ]; then set -- "$@" --depth "$depth_value"; fi;`,
		`"$@" | ` + shellQuoteArg(treeBin) + ` ` + strings.Join(fzfTreeRenderArgs(), " "),
	}
	return strings.Join(script, " ")
}
