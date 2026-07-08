package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/platform"
)

// The searching-hint document is the preview state for "pattern typed,
// reload has not produced rows yet". The Windows variant must explain
// the once-per-boot Defender cold-search wait; other platforms get the
// neutral searching text only. Pinned per the 2026-07-04 UX decision:
// the empty-pattern regex hints stay untouched, and this message disappears
// once a row exists and the normal focused-file preview runs.
func TestBuildInternalSearchingHintDocument(t *testing.T) {
	render := func(goos string) string {
		var buf bytes.Buffer
		if err := renderTreeDocument(&buf, buildInternalSearchingHintDocument(goos), FzfFilterTreeRenderOptions(), platform.ANSIPalette()); err != nil {
			t.Fatal(err)
		}
		return string(ansiEscape.ReplaceAll(buf.Bytes(), nil))
	}

	win := render("windows")
	if !strings.Contains(win, "no matches for this pattern yet") {
		t.Fatalf("windows doc missing searching line: %q", win)
	}
	if !strings.Contains(win, "antivirus") || !strings.Contains(win, "not recur until the next reboot") {
		t.Fatalf("windows doc missing Defender explanation: %q", win)
	}

	mac := render("darwin")
	if !strings.Contains(mac, "no matches for this pattern yet") {
		t.Fatalf("darwin doc missing searching line: %q", mac)
	}
	if strings.Contains(mac, "antivirus") {
		t.Fatalf("darwin doc must not carry the Windows paragraph: %q", mac)
	}
}
