package catclip

import "github.com/tigreau/catclip/internal/picker"

const (
	fzfANSIColorDim       = "8"
	fzfANSIColorWarn      = "3"
	fzfANSIColorDir       = "4"
	fzfANSIColorPrompt    = "6"
	fzfANSIColorGit       = "5"
	fzfANSIColorValue     = "7"
	fzfANSIColorPreviewBg = "0"

	fzfTreePreviewTheme = "fzf-dark"
)

func themedFzfRequest(req picker.Request) picker.Request {
	hasPreview := req.PreviewCommand != "" || req.PreviewWindow != ""
	req.ColorSpecs = fzfColorSpecs(activeColorPalette(), hasPreview)
	return req
}

func fzfColorSpecs(palette colorPalette, hasPreview bool) []string {
	if palette == (colorPalette{}) {
		return nil
	}

	// Keep the list pane on the terminal's normal background while mapping fzf's
	// UI accents onto catclip's prompt / git / dim palette semantics.
	specs := []string{
		"prompt:" + fzfANSIColorPrompt,
		"pointer:" + fzfANSIColorPrompt,
		"marker:" + fzfANSIColorGit,
		"spinner:" + fzfANSIColorPrompt,
		"hl:" + fzfANSIColorDir,
		"hl+:" + fzfANSIColorDir,
		"info:" + fzfANSIColorWarn,
		"header:" + fzfANSIColorDim,
		"border:" + fzfANSIColorDim,
		"label:" + fzfANSIColorDim,
		"header-border:" + fzfANSIColorDim,
		"header-label:" + fzfANSIColorDim,
		"fg+:" + fzfANSIColorValue,
		"bg+:" + fzfANSIColorDim,
	}
	if hasPreview {
		specs = append(specs,
			"preview-bg:"+fzfANSIColorPreviewBg,
			"preview-fg:"+fzfANSIColorValue,
			"preview-border:"+fzfANSIColorDim,
			"preview-label:"+fzfANSIColorPrompt,
		)
	}
	return specs
}

// fzfTreeRenderArgs is the target-tier preview render: bare tree, no metadata
// columns. Bare is now catclip-tree's default, so no flag is needed for it.
func fzfTreeRenderArgs() []string {
	return []string{"--preview-theme", fzfTreePreviewTheme, "--color", "always"}
}

// fzfFilterTreeRenderArgs is the filter-tier preview render: bare layout plus
// the shape-tag, git-badge, and entry-size columns and the count/size/token
// summary footer. Every one of those is computed while building the preview
// plan regardless of whether it is displayed, so rendering them adds no
// per-refresh cost — the size collection (one Lstat per non-SizeKnown entry)
// is the same work the plan build already does (see the v0.5.5 tree-shape
// plan, "Why entry sizes are free in the filter tier"; measured ~3.5 µs/file).
// The footer's token estimate is the load-bearing reason to show it: it tells
// the user whether the current selection will blow the context window before
// they commit the command.
func fzfFilterTreeRenderArgs() []string {
	return []string{"--shape-tags", "--git-badges", "--entry-sizes", "--summary-footer", "--preview-theme", fzfTreePreviewTheme, "--color", "always"}
}
