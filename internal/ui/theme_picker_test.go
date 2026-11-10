package ui

import (
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/picker"
)

func TestThemePickerUsesOnlyBuiltInSamples(t *testing.T) {
	req := themePickerRequest("/tmp/a path/catclip", "github")
	if len(req.Lines) != 19 || !strings.HasSuffix(req.Lines[0], "\tgithub") {
		t.Fatal("current theme not first")
	}
	if req.Header != "Choose a theme.\nUsed in interactive mode and --print.\n[Up/Down] move  [Enter] save\n[Esc] cancel" {
		t.Fatal("theme hints do not use the compact menu layout")
	}
	if req.Multi || req.WithNth != "1" || !strings.Contains(req.PreviewCommand, "--internal-theme-preview {2}") {
		t.Fatal("incorrect picker request")
	}
	if req.PreviewWindow != "right:70%:wrap:border-rounded:noinfo" {
		t.Fatal("preview is not on the right")
	}
}

func TestThemePickerSaveIntentWinsQueuedRelaunch(t *testing.T) {
	id, _, reload, err := parseThemePickerResult(picker.Result{Matches: []string{themeReloadMarker + "2", themeSaveMarker + "monokai", "xcode"}})
	if err != nil || id != "monokai" || reload {
		t.Fatalf("Enter intent was lost to a queued redraw: %s %t %v", id, reload, err)
	}
	for _, matches := range [][]string{{themeSaveMarker + "unknown", "xcode"}, {themeSaveMarker + "xcode"}, {themeSaveMarker + "xcode", themeSaveMarker + "monokai", "xcode"}} {
		if _, _, _, err := parseThemePickerResult(picker.Result{Matches: matches}); err == nil {
			t.Fatalf("invalid save intent accepted: %v", matches)
		}
	}
}
func BenchmarkThemePickerRequest(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = themePickerRequest("/usr/local/bin/catclip", "xcode-dark")
	}
}

func TestThemeActionBypassesTargetPicker(t *testing.T) {
	usePicker, err := shouldUseStartupPicker([]string{"--theme"})
	if err != nil || usePicker {
		t.Fatalf("theme triggered target selection: %t %v", usePicker, err)
	}
}

func TestThemePickerCoalescesRelaunchNotifications(t *testing.T) {
	id, pos, reload, err := parseThemePickerResult(picker.Result{Query: "Xcode Light", Matches: []string{themeReloadMarker + "2", themeReloadMarker + "1", "xcode"}})
	if err != nil || id != "xcode" || pos != 1 || !reload {
		t.Fatalf("result=%s,%d,%t,%v", id, pos, reload, err)
	}
	id, _, reload, err = parseThemePickerResult(picker.Result{Matches: []string{"github-dark"}})
	if err != nil || id != "github-dark" || reload {
		t.Fatal("normal Enter became a relaunch")
	}
	for _, matches := range [][]string{{themeReloadMarker + "0", "xcode"}, {themeReloadMarker + "1"}, {"unknown"}} {
		if _, _, _, err := parseThemePickerResult(picker.Result{Matches: matches}); err == nil {
			t.Fatal("invalid picker result accepted")
		}
	}
}

func TestThemeFocusDoesNotActOnEmptyOrUnchangedSelection(t *testing.T) {
	for _, focused := range []string{"", "terminal"} {
		var out strings.Builder
		if err := RunInternalThemeFocus([]string{"terminal", focused}, &out); err != nil || out.Len() != 0 {
			t.Fatalf("unchanged focus emitted an action: %q %v", out.String(), err)
		}
	}
}
