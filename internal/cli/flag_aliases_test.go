package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

func TestNoIgnoreAlias(t *testing.T) {
	for _, args := range [][]string{
		{"src", "-ni"}, {"src", "-ni", "-wb", "--only", "*.go"},
		{"src", "-ni", "--then", "docs", "-ni", "--paths"},
		{"src", "--contains", "-ni"}, {"src", "--snippet", "-ni", "3"},
	} {
		canonical := NormalizeHiddenAliases(args)
		cfg, err := ParseArgs(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		want, err := ParseArgs(canonical)
		if err != nil || !reflect.DeepEqual(cfg, want) {
			t.Fatalf("different parsing: %v %v", args, err)
		}
		gotSpec, err := StartupPreflightCommandSpec(args)
		if err != nil {
			t.Fatalf("preflight %v: %v", args, err)
		}
		wantSpec, err := StartupPreflightCommandSpec(canonical)
		if err != nil || !reflect.DeepEqual(gotSpec, wantSpec) {
			t.Fatalf("different preflight %v", args)
		}
		resolved := command.CanonicalResolvedInvocationCommand(command.ResolvedFromParsed(cfg), command.RenderFlags{})
		if args[1] == "-ni" && (!strings.Contains(resolved, "--no-ignore") || strings.Contains(resolved, " -ni")) {
			t.Fatal(resolved)
		}
	}
	for _, args := range [][]string{{"src", "--only", "*.go", "-ni"}, {"src", "-ni", "-ni"}, {"src", "-ni", "value"}} {
		_, got := ParseArgs(args)
		_, want := ParseArgs(NormalizeHiddenAliases(args))
		if got == nil || want == nil || got.Error() != want.Error() || strings.Contains(got.Error(), "-ni") {
			t.Fatalf("alias errors %v: %v / %v", args, got, want)
		}
	}
	for _, help := range []string{ShortHelpText("dev", ".hiss", platform.Palette{}), FullHelpText("dev", ".hiss", platform.Palette{})} {
		if strings.Count(help, "-ni, --no-ignore") != 1 || !strings.Contains(help, "-wb, --with-binaries") {
			t.Fatal("help alias rows must use the same short/long format")
		}
	}
	if !IsModifierBoundaryToken("-ni") || !IsKnownScopeModifierToken("-ni") {
		t.Fatal("missing alias boundary")
	}
}

func TestHiddenAliasPreservesOperandsAndInput(t *testing.T) {
	args := []string{"src", "--contains", "-ni", "-ni", "--then", "./-ni", "--not-contains", "-ni"}
	before := append([]string(nil), args...)
	got := NormalizeHiddenAliases(args)
	want := []string{"src", "--contains", "-ni", "--no-ignore", "--then", "./-ni", "--not-contains", "-ni"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(args, before) {
		t.Fatalf("got %v; input %v", got, args)
	}
	plain := []string{"src", "--no-ignore", "--contains", "pattern"}
	if got := NormalizeHiddenAliases(plain); &got[0] != &plain[0] {
		t.Fatal("ordinary path allocated")
	}
	literal := []string{"src", "--contains", "-ni"}
	if got := NormalizeHiddenAliases(literal); &got[0] != &literal[0] {
		t.Fatal("literal operand changed")
	}
}

func BenchmarkHiddenAliasAbsent(b *testing.B) {
	args := []string{"src", "--only", "*.go", "--contains", "TODO", "--recent", "10", "--headless"}
	b.ReportAllocs()
	for b.Loop() {
		_ = NormalizeHiddenAliases(args)
	}
}
