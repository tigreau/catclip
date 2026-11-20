package catclip

import (
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/platform"
)

func TestReleaseNoteTerminalFormatting(t *testing.T) {
	colors := platform.ANSIPalette()
	notes := "### Faster previews\n\n- **Reuse** `cached_frames`\n> Keep *focused* output.\n[Changelog](https://example.test/changes)\n```sh\ncatclip '**/*.go'\n```\nunknown __syntax"
	got := formatReleaseNotes(notes, colors)
	for _, want := range []string{
		colors.Bold + colors.Prompt + "Faster previews" + colors.Reset,
		colors.Prompt + "•" + colors.Reset + " " + colors.Bold + "Reuse" + colors.Reset,
		colors.Prompt + "cached_frames" + colors.Reset,
		colors.Dim + "│ Keep ",
		colors.Bold + "focused" + colors.Reset + colors.Dim,
		colors.Prompt + "Changelog (https://example.test/changes)" + colors.Reset,
		"  " + colors.Prompt + "catclip '**/*.go'" + colors.Reset,
		"unknown __syntax",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "###") || strings.Contains(got, "```") {
		t.Fatalf("unrendered heading/fence: %q", got)
	}
	if plain := formatReleaseNotes(notes, platform.Palette{}); plain != notes || strings.Contains(plain, "\x1b") {
		t.Fatalf("changed piped Markdown: %q", plain)
	}
}

func TestReleaseNoteCodeSpansAndFenceLengths(t *testing.T) {
	c := platform.ANSIPalette()
	got := formatReleaseNotes("`` **literal** `value` ``\n~~~~\n### literal heading\n~~~\nstill literal\n~~~~", c)
	for _, want := range []string{c.Prompt + " **literal** `value` " + c.Reset, c.Prompt + "### literal heading" + c.Reset, c.Prompt + "~~~" + c.Reset, c.Prompt + "still literal" + c.Reset} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing literal %q in %q", want, got)
		}
	}
}
