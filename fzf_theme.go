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

func fzfTreeRenderArgs() []string {
	return []string{"--bare", "--preview-theme", fzfTreePreviewTheme, "--color", "always"}
}
