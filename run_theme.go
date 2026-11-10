package catclip

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/theme"
	"github.com/tigreau/catclip/internal/ui"
)

func runTheme(stderr io.Writer) error {
	id, err := ui.ChooseTheme()
	if err != nil || id == "" {
		return err
	}
	if err := theme.Save(id); err != nil {
		return fmt.Errorf("save theme: %w", err)
	}
	selected, _ := theme.Lookup(id)
	color := platform.ActivePaletteForWriter(stderr).Reset != "" && os.Getenv("TERM") != "dumb"
	return render.WriteThemeConfirmation(stderr, selected, color)
}
func runThemePreview(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("theme preview requires one theme ID")
	}
	selected, ok := theme.Lookup(args[1])
	if !ok {
		return fmt.Errorf("unknown theme %q", args[1])
	}
	width, _ := strconv.Atoi(os.Getenv("FZF_PREVIEW_COLUMNS"))
	height, _ := strconv.Atoi(os.Getenv("FZF_PREVIEW_LINES"))
	return render.WriteThemeSample(stdout, selected, width, height, os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb")
}
