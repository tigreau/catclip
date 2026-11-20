package output

import "github.com/tigreau/catclip/internal/platform"

// CopyText puts text on the clipboard as text (never as a bundle file), the
// same way catclip copies small output: the clipboard API on Windows, cmd.exe
// with UTF-8 under WSL, pbcopy or wl-copy elsewhere.
func CopyText(plat string, text []byte, colors platform.Palette) error {
	_, err := emitBufferedToTextClipboard(RuntimeEnvironment{Platform: plat}, text, 0, colors)
	return err
}
