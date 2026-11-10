package cli

import (
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestThemeIsImmediateAction(t *testing.T) {
	cfg, err := ParseArgs([]string{"--theme"})
	if err != nil || cfg.Action != command.ActionTheme {
		t.Fatalf("%+v %v", cfg, err)
	}
	if err := ValidateStartupPreflightArgs([]string{"--theme"}); err != nil {
		t.Fatal(err)
	}
}
