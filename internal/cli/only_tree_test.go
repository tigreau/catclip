package cli

import (
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

func TestOnlyTreeHeadlessFlags(t *testing.T) {
	for _, flag := range []string{"--only-tree", "-ot"} {
		for _, args := range [][]string{{"src", flag, "--headless"}, {"--headless", flag, "src"}} {
			if err := ValidateStartupPreflightArgs(args); err != nil {
				t.Fatal(err)
			}
			cfg, err := ParseArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Quiet || !cfg.Headless || cfg.OutputMode != command.OutputModeStdout || cfg.EmissionPolicy != command.EmissionNever {
				t.Fatalf("unexpected config: %+v", cfg)
			}
			canonical := command.CanonicalGlobalArgs(command.InvocationFromParsed(cfg), command.RenderFlagsFromParsed(cfg))
			if !strings.Contains(strings.Join(canonical, " "), "--only-tree") {
				t.Fatal(canonical)
			}
			if _, err := ParseArgs(append(canonical, "src")); err != nil {
				t.Fatal(err)
			}
		}
		for _, conflict := range []string{"--yes", "--print", "--quiet", "--no-tree", "-nt"} {
			for _, flags := range [][]string{{flag, conflict}, {conflict, flag}} {
				args := append([]string{"src", "--headless"}, flags...)
				if _, err := ParseArgs(args); err == nil {
					t.Fatalf("accepted %q", args)
				}
				if err := ValidateStartupPreflightArgs(args); err == nil {
					t.Fatalf("preflight accepted %q", args)
				}
			}
		}
	}
	args := []string{"src", "--contains", "-ot", "--only-tree", "--headless"}
	cfg, err := ParseArgs(args)
	if err != nil || command.ExecutionScopesFromSpec(cfg.Command)[0].Contains != "-ot" {
		t.Fatalf("literal operand: %+v %v", cfg, err)
	}
	if _, err := ParseArgs([]string{"--only-tree", "--headless"}); err == nil {
		t.Fatal("headless requires targets")
	}
	for _, help := range []string{ShortHelpText("dev", ".hiss", platform.Palette{}), FullHelpText("dev", ".hiss", platform.Palette{})} {
		if strings.Count(help, "-ot, --only-tree") != 1 {
			t.Fatal("missing consistent alias row")
		}
	}
}
