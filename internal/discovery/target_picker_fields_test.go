package discovery

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

func TestNoIgnoreCandidateNarrowingKeepsFullPreviewInventory(t *testing.T) {
	all := []TargetMatch{
		{Path: "src", Kind: treeTargetKindDir},
		{Path: "src/a.ts", Kind: treeTargetKindFile},
		{Path: "other/src", Kind: treeTargetKindDir},
		{Path: "other/src/b.ts", Kind: treeTargetKindFile},
	}
	candidates, inventory := targetPickerMatchSets(all, true, "src")
	if got, want := targetMatchPaths(candidates), []string{"src", "other/src"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate paths = %v, want %v", got, want)
	}
	if got, want := targetMatchPaths(inventory), targetMatchPaths(all); !reflect.DeepEqual(got, want) {
		t.Fatalf("preview inventory paths = %v, want %v", got, want)
	}
}

func targetMatchPaths(matches []TargetMatch) []string {
	out := make([]string, len(matches))
	for index := range matches {
		out[index] = matches[index].Path
	}
	return out
}

func TestTargetPickerSessionEnvironmentReplacesInheritedValue(t *testing.T) {
	t.Setenv(picker.TargetPreviewSessionEnv, "stale-session")
	env := environmentWithValue(picker.TargetPreviewSessionEnv, "current-session")
	want := picker.TargetPreviewSessionEnv + "=current-session"
	count := 0
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, picker.TargetPreviewSessionEnv) {
			count++
			if item != want {
				t.Fatalf("target preview environment = %q, want %q", item, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("target preview environment entries = %d, want 1 (process env had %d entries)", count, len(os.Environ()))
	}
}

func TestStyledTargetPickerHeaderAccentsOnlyMatchSymbols(t *testing.T) {
	plain := strings.TrimSuffix(TargetPickerHeaderWithEscHint("select> ", ""), "\n") + "\n" + TargetPickerSymbolsHint()
	colors := platform.Palette{Bold: "<bold>", Prompt: "<cyan>", Reset: "<reset>"}
	styled := styledTargetPickerHeaderWithSymbols("select> ", "", colors)

	for _, want := range []string{
		"<bold><cyan>'<reset>name not fuzzy",
		"<bold><cyan>^<reset>name starts with",
		"name<bold><cyan>$<reset> ends with",
	} {
		if !strings.Contains(styled, want) {
			t.Fatalf("styled target header missing %q: %q", want, styled)
		}
	}
	stripped := strings.NewReplacer(
		"<bold>", "",
		"<cyan>", "",
		"<reset>", "",
	).Replace(styled)
	if stripped != plain {
		t.Fatalf("styling changed target header text\nwant: %q\n got: %q", plain, stripped)
	}
	if got := styledTargetPickerHeaderWithSymbols("select> ", "", platform.Palette{}); got != plain {
		t.Fatalf("disabled palette changed target header: %q", got)
	}
}

func TestTargetPickerSymbolsRevealOnceAfterQueryChange(t *testing.T) {
	binding := targetPickerRevealHeaderBinding(TargetPickerSymbolsHint())
	if !strings.HasPrefix(binding, "change:change-header{") {
		t.Fatalf("expected native change-header action, got %q", binding)
	}
	if !strings.HasSuffix(binding, "}+unbind(change)") {
		t.Fatalf("expected reveal binding to remove itself, got %q", binding)
	}
}

func TestTargetPickerRevealHeaderBindingIsAcceptedByFzf(t *testing.T) {
	fzf, err := FuzzyResolverBinary()
	if err != nil {
		t.Skipf("fzf unavailable: %v", err)
	}
	header := styledTargetPickerHeaderWithSymbols("select> ", "", platform.ANSIPalette())
	cmd := exec.Command(fzf, "--ansi", "--bind", targetPickerRevealHeaderBinding(header), "--filter", "candidate")
	cmd.Stdin = strings.NewReader("candidate\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fzf rejected target reveal binding: %v\n%s", err, out)
	}
}

func TestTargetPickerFzfFieldsDisplayMetadataAndMatchPath(t *testing.T) {
	fzf, err := FuzzyResolverBinary()
	if err != nil {
		t.Skipf("fzf unavailable: %v", err)
	}

	lines, _ := TargetMatchLabels([]TargetMatch{
		{Path: "src/components/Login.tsx", Kind: "file"},
		{Path: "src/pages/LoginForm.tsx", Kind: "file"},
		{Path: "docs", Kind: "dir"},
	})

	filter := func(query string) (string, int) {
		t.Helper()
		cmd := exec.Command(
			fzf,
			"--ansi",
			"--delimiter", "\t",
			"--with-nth", "1,2",
			"--nth", "2",
			"--filter", query,
		)
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
		out, runErr := cmd.Output()
		if runErr == nil {
			return string(out), 0
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("fzf filter %q: %v", query, runErr)
		return "", -1
	}

	out, code := filter("lgn")
	if code != 0 {
		t.Fatalf("path query returned exit %d; target picker would be empty", code)
	}
	for _, want := range []string{"src/components/Login.tsx", "src/pages/LoginForm.tsx"} {
		if !strings.Contains(out, want) {
			t.Fatalf("path query missing %q:\n%s", want, out)
		}
	}

	out, code = filter("file")
	if code != 1 || out != "" {
		t.Fatalf("metadata-only query must not match target rows; exit=%d output=%q", code, out)
	}
}
