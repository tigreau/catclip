package theme

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// Derived once with the theme, not while drawing individual candidate rows.
func (t *Theme) buildInteractiveColors() {
	bg, fg := t.FzfBackground, t.FzfForeground
	prompt, match, marker, info := t.FzfLabel, t.paletteColor(t.Palette.Dir), t.paletteColor(t.Palette.Git), t.paletteColor(t.Palette.Warn)
	header, rowBG, rowFG := fg, "8", "7"
	if t.ID == TerminalID {
		prompt, match, marker, info, header = "6", "4", "5", "3", "8"
	} else {
		background := chroma.MustParseColour(t.Background)
		foreground := chroma.MustParseColour(t.Foreground)
		mix := func(a, b uint8) uint8 { return uint8((int(a)*86 + int(b)*14) / 100) }
		selection := chroma.NewColour(mix(background.Red(), foreground.Red()), mix(background.Green(), foreground.Green()), mix(background.Blue(), foreground.Blue()))
		rowBG, rowFG = fzfColor(selection), fg
		if isSolarized(t.ID) {
			roles := solarizedMode(t.ID)
			rowBG, rowFG = solarizedFzfColor(roles.selection), solarizedFzfColor(roles.emphasis)
		}
		// Quantization can reduce contrast; keep search matches readable on the
		// selected-row surface as well as on the ordinary list background.
		visible := selection
		if !trueColor() {
			_, visible = indexedColour(selection)
		}
		candidate := t.Style.Get(chroma.NameClass).Colour
		if !isSolarized(t.ID) && (!candidate.IsSet() || contrast(candidate, visible) < 3) {
			match = fg
		}
	}
	t.interactiveColors = []string{
		"bg:" + bg, "fg:" + fg, "list-bg:" + bg, "list-fg:" + fg,
		"input-bg:" + bg, "input-fg:" + fg, "header-bg:" + bg, "footer-bg:" + bg,
		"fg+:" + rowFG, "bg+:" + rowBG, "gutter:" + bg,
		"prompt:" + prompt, "pointer:" + prompt, "marker:" + marker, "spinner:" + prompt,
		"hl:" + match, "hl+:" + match, "info:" + info, "header:" + header, "footer:" + fg,
		"border:" + t.FzfBorder, "label:" + t.FzfLabel, "scrollbar:" + t.FzfBorder,
		"input-border:" + t.FzfBorder, "input-label:" + t.FzfLabel,
		"header-border:" + t.FzfBorder, "header-label:" + t.FzfLabel,
		"footer-border:" + t.FzfBorder, "footer-label:" + t.FzfLabel,
	}
}

func (t *Theme) paletteColor(sequence string) string {
	// UI emphasis is independent of the foreground color fzf needs.
	sequence = strings.TrimPrefix(sequence, t.Palette.Bold)
	// Nonterminal foregrounds contain one SGR, constructed by ansi().
	code := strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m")
	if strings.HasPrefix(code, "38;5;") {
		return strings.TrimPrefix(code, "38;5;")
	}
	if strings.HasPrefix(code, "38;2;") {
		parts := strings.Split(strings.TrimPrefix(code, "38;2;"), ";")
		if len(parts) == 3 {
			r, _ := strconv.Atoi(parts[0])
			g, _ := strconv.Atoi(parts[1])
			b, _ := strconv.Atoi(parts[2])
			return fmt.Sprintf("#%02x%02x%02x", r, g, b)
		}
	}
	return t.FzfForeground
}

// InteractiveColorSpecs returns an independent request slice; callers may add
// overrides without changing the cached theme. All picker families share it.
func (t *Theme) InteractiveColorSpecs(hasPreview bool) []string {
	specs := append([]string(nil), t.interactiveColors...)
	if hasPreview {
		specs = append(specs, t.PreviewColorSpecs()...)
	}
	return specs
}
