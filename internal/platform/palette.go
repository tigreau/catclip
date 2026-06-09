package platform

import (
	"io"
	"os"
)

// Palette holds ANSI color escape strings used by stderr diagnostics, help
// text, and tree previews. A zero-value Palette emits no color (callers can
// disable color by passing Palette{}).
type Palette struct {
	Reset  string
	Bold   string
	Dim    string
	OK     string
	Err    string
	Warn   string
	Dir    string
	Label  string
	Value  string
	Tree   string
	Prompt string
	Git    string
}

// ActivePalette returns the palette that should be used for stderr-bound
// diagnostics.
func ActivePalette() Palette {
	return ActivePaletteForWriter(os.Stderr)
}

// ActivePaletteForWriter returns the palette appropriate for the given
// writer: a zero palette if the writer is not a terminal, NO_COLOR is set,
// or the writer is not an *os.File.
func ActivePaletteForWriter(w io.Writer) Palette {
	file, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || !IsTerminalFile(file) {
		return Palette{}
	}
	return ANSIPalette()
}

// ANSIPalette returns the standard ANSI escape palette catclip uses, with
// no terminal capability check (callers gate that themselves).
func ANSIPalette() Palette {
	return Palette{
		Reset:  "\033[0m",
		Bold:   "\033[1m",
		Dim:    "\033[2m",
		OK:     "\033[32m",
		Err:    "\033[31m",
		Warn:   "\033[33m",
		Dir:    "\033[1;34m",
		Label:  "\033[90m",
		Tree:   "\033[90m",
		Prompt: "\033[36m",
		Git:    "\033[35m",
	}
}
