package catclip

import (
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/ui"
)

// TestShouldWriteResolvedStartupCommandHonorsPickerSelectedHeadless
// stays at root because shouldWriteResolvedStartupCommand is a root
// function (it wires ui.StartupPickerResult into the root quiet-flow
// decision). The classifier moved it into internal/ui by mistake; this
// is the corrected location.
func TestShouldWriteResolvedStartupCommandHonorsPickerSelectedHeadless(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{".", "--headless"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !cfg.Quiet {
		t.Fatal("test setup expected --headless to imply quiet")
	}

	if !shouldWriteResolvedStartupCommand(ui.StartupPickerResult{UsedFzf: true, ForceResolvedCommand: true}, cfg.Quiet) {
		t.Fatal("expected picker-selected --headless to still print the resolved command")
	}
	if shouldWriteResolvedStartupCommand(ui.StartupPickerResult{UsedFzf: true}, cfg.Quiet) {
		t.Fatal("expected typed quiet/headless command without force to stay quiet")
	}
	if shouldWriteResolvedStartupCommand(ui.StartupPickerResult{ForceResolvedCommand: true}, cfg.Quiet) {
		t.Fatal("expected no resolved command when fzf was not used")
	}
}
