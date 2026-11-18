package cli

import (
	"github.com/tigreau/catclip/internal/command"
	"strings"
	"testing"
)

func TestFileInclusionScopeAndOrder(t *testing.T) {
	for _, flags := range [][]string{{"-wb", "--no-ignore"}, {"--no-ignore", "--with-binaries"}} {
		args := append([]string{"src", "--then", "assets"}, flags...)
		args = append(args, "--paths", "--headless")
		cfg, err := ParseArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		scopes := command.ExecutionScopesFromSpec(cfg.Command)
		if cfg.WithBinaries || scopes[0].WithBinaries || !scopes[1].WithBinaries || !scopes[1].NoIgnore {
			t.Fatalf("scope policy: %+v", scopes)
		}
		replay := command.CanonicalResolvedInvocationCommand(command.ResolvedFromParsed(cfg), command.RenderFlagsFromParsed(cfg))
		if !strings.Contains(replay, "--then assets ") {
			t.Fatal(replay)
		}
		if err := ValidateStartupPreflightArgs(args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"src", "--only", "*.go", "-wb"},
		{"src", "--paths", "--with-binaries"},
		{"src", "-wb", "--no-ignore", "--with-binaries"},
		{"src", "--no-ignore", "-wb", "--no-ignore"},
	} {
		if _, err := ParseArgs(args); err == nil {
			t.Fatalf("strict accepted %v", args)
		}
		if err := ValidateStartupPreflightArgs(args); err == nil {
			t.Fatalf("preflight accepted %v", args)
		}
	}
	if _, err := ParseArgs([]string{"src", "--snippet", "func", "--then", "assets", "-wb"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"src", "-wb", "--not-contains", "TODO", "--metadata"}
	if _, err := ParseArgs(args); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStartupPreflightArgs(args); err != nil {
		t.Fatal(err)
	}
}
