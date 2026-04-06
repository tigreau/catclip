package tree

import (
	"regexp"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestHighlightFilePreviewAddsANSIForRecognizedSource(t *testing.T) {
	out := highlightFilePreview("main.go", "package main\nfunc main() {}\n", RenderOptions{})
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI-highlighted output, got %q", out)
	}
	if !strings.Contains(out, "package") || !strings.Contains(out, "func") {
		t.Fatalf("expected highlighted output to retain source text, got %q", out)
	}
}

func TestHighlightFilePreviewFallsBackForPlaintext(t *testing.T) {
	in := "just some ordinary text\nwithout code markers\n"
	out := highlightFilePreview("notes.unknown", in, RenderOptions{})
	if out != in {
		t.Fatalf("expected plain text fallback, got %q", out)
	}
}

func TestHighlightFilePreviewAddsANSIForDiffHint(t *testing.T) {
	out := highlightFilePreview("diff", "diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-old\n+new\n", RenderOptions{})
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI-highlighted diff output, got %q", out)
	}
	if !strings.Contains(out, "diff --git") || !strings.Contains(out, "@@") {
		t.Fatalf("expected highlighted diff output to retain patch text, got %q", out)
	}
}

func TestPreviewChromaStyleUsesDarkThemeWithoutBackgroundForFzfPreview(t *testing.T) {
	style := previewChromaStyle(RenderOptions{PreviewTheme: previewThemeFzfDark})
	if got := chromaStyleForPreview(RenderOptions{PreviewTheme: previewThemeFzfDark}); got != fzfDarkPreviewStyle {
		t.Fatalf("preview style = %q, want %q", got, fzfDarkPreviewStyle)
	}
	if style.Get(chroma.Background).Background.IsSet() {
		t.Fatalf("expected fzf preview style background to be cleared, got %#v", style.Get(chroma.Background))
	}
}

func TestPreviewChromaStyleKeepsDefaultBackgroundOutsideFzfPreview(t *testing.T) {
	style := previewChromaStyle(RenderOptions{})
	if got := chromaStyleForPreview(RenderOptions{}); got != defaultChromaStyle {
		t.Fatalf("default preview style = %q, want %q", got, defaultChromaStyle)
	}
	if !style.Get(chroma.Background).Background.IsSet() {
		t.Fatalf("expected default preview style to retain its background, got %#v", style.Get(chroma.Background))
	}
}

func TestPreviewLineNumberColorUsesBrightANSIForFzfPreviewTheme(t *testing.T) {
	colors := Palette{Label: "\033[90m"}
	if got := previewLineNumberColor(RenderOptions{PreviewTheme: previewThemeFzfDark}, colors); got != "\033[37m" {
		t.Fatalf("fzf preview line number color = %q, want %q", got, "\033[37m")
	}
	if got := previewLineNumberColor(RenderOptions{}, colors); got != colors.Label {
		t.Fatalf("default preview line number color = %q, want %q", got, colors.Label)
	}
}

func TestHighlightMatchLineANSIWrapsMatchedText(t *testing.T) {
	re := regexp.MustCompile("TODO")
	line := highlightMatchLineANSI("\x1b[38;5;126mTODO\x1b[0m later", "TODO later", re)
	if !strings.Contains(line, previewMatchStart) || !strings.Contains(line, previewMatchEnd) {
		t.Fatalf("expected match highlight markers, got %q", line)
	}
	if !strings.Contains(line, "\x1b[38;5;126m"+previewMatchStart+"TODO"+previewMatchEnd+"\x1b[0m") {
		t.Fatalf("expected match highlight to wrap the matched token while preserving syntax color, got %q", line)
	}
}

func TestHighlightWholeLineANSIPreservesSyntaxColor(t *testing.T) {
	line := highlightWholeLineANSI("\x1b[38;5;126mTODO\x1b[0m later")
	if !strings.Contains(line, previewMatchStart) || !strings.Contains(line, previewMatchEnd) {
		t.Fatalf("expected whole-line highlight markers, got %q", line)
	}
	if !strings.Contains(line, previewMatchStart+"\x1b[38;5;126mTODO\x1b[0m"+previewMatchStart+" later"+previewMatchEnd) {
		t.Fatalf("expected whole-line highlight to survive ANSI resets, got %q", line)
	}
}
