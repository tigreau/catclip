package ui

import (
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/theme"
)

const fzfTreePreviewTheme = "fzf-dark"

func themedFzfRequest(req picker.Request) picker.Request {
	hasPreview := req.PreviewCommand != "" || req.PreviewWindow != ""
	req.ColorSpecs = fzfColorSpecs(platform.ActivePalette(), hasPreview)
	return req
}

func fzfColorSpecs(palette platform.Palette, hasPreview bool) []string {
	if palette == (platform.Palette{}) {
		return nil
	}

	return theme.Current().InteractiveColorSpecs(hasPreview)
}

func interactivePalette() platform.Palette {
	if platform.ActivePalette() == (platform.Palette{}) {
		return platform.Palette{}
	}
	return theme.PreviewPalette()
}
