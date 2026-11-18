package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

func TestCompoundAliasesMatchLongFlags(t *testing.T) {
	for _, pair := range [][2]string{{"-wb", "--with-binaries"}, {"-ot", "--only-tree"}, {"-ni", "--no-ignore"}, {"-nt", "--no-tree"}} {
		short, err := ParseArgs([]string{"src", pair[0], "--headless"})
		if err != nil {
			t.Fatal(err)
		}
		long, err := ParseArgs([]string{"src", pair[1], "--headless"})
		if err != nil || !reflect.DeepEqual(short, long) {
			t.Fatalf("%v mismatch: %v", pair, err)
		}
		if err := ValidateStartupPreflightArgs([]string{"src", pair[0], "--headless"}); err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
			args := []string{"src", flag, pair[0], "--headless"}
			cfg, err := ParseArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.NoTree || cfg.WithBinaries || cfg.EmissionPolicy == command.EmissionNever {
				t.Fatalf("operand treated as flag: %q", args)
			}
			scope := command.ExecutionScopesFromSpec(cfg.Command)[0]
			if scope.NoIgnore {
				t.Fatalf("operand changed inclusion: %q", args)
			}
		}
		for _, help := range []string{ShortHelpText("dev", ".hiss", platform.Palette{}), FullHelpText("dev", ".hiss", platform.Palette{})} {
			if strings.Count(help, pair[0]+", "+pair[1]) != 1 {
				t.Fatalf("help must list %v once", pair)
			}
		}
	}
}
