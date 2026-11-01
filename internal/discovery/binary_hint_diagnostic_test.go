package discovery

import (
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func TestBinaryGlobHintUsesTargetLocalEvidenceWithoutRediscovery(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		"assets/archive.jar": "a\x00b", "assets/notes.txt": "text\n",
	})
	for _, noIgnore := range []bool{false, true} {
		t.Run(map[bool]string{false: "visible", true: "no-ignore"}[noIgnore], func(t *testing.T) {
			walks := 0
			defer search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { walks++ })()
			r := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: noIgnore}
			entries, diagnostics, _, _, err := r.resolveGlobTarget(0, "assets/*.jar", platform.Palette{})
			if err != nil || len(entries) != 0 || len(diagnostics) != 1 {
				t.Fatalf("entries=%v diagnostics=%v err=%v", entries, diagnostics, err)
			}
			message := diagnostics[0].Message
			want := "catclip " + ShellQuoteArg("assets/*.jar")
			if noIgnore {
				want += " --no-ignore"
			}
			want += " -b"
			if !strings.Contains(message, want) || !strings.Contains(message, "Binary files were excluded") || strings.Contains(message, "\x1b") {
				t.Fatalf("wrong hint: %s", message)
			}
			if diagnostics[0].IsTargetNotFound {
				t.Fatal("observed binary matches must not be reported as missing paths")
			}
			if walks != 1 {
				t.Fatalf("binary hint caused %d walks, want 1", walks)
			}
			// Empty candidates must clear prior evidence, including when -b skips classification.
			for _, withBinaries := range []bool{false, true} {
				r.WithBinaries = withBinaries
				r.lastClassificationExcludedBinary = true
				_, diagnostics, _, _, err = r.resolveGlobTarget(0, "*.absent", platform.Palette{})
				if err != nil || len(diagnostics) != 1 || strings.Contains(diagnostics[0].Message, "Binary files were excluded") {
					t.Fatalf("stale binary evidence: diagnostics=%v err=%v", diagnostics, err)
				}
			}
		})
	}
}

func TestMissingTargetBinaryAdviceIsConditional(t *testing.T) {
	// NoIgnore prevents the existing ignored-ancestor probe. The new advice
	// must work with no filesystem evidence and must not claim files exist.
	r := Resolver{NoIgnore: true}
	message := targetNotFoundOrIgnoredAncestorMessage(&r, "absent.jar", 0, platform.Palette{})
	if !strings.Contains(message, "For binary files,") || strings.Contains(message, "Binary files were excluded") {
		t.Fatalf("wrong conditional advice: %s", message)
	}
	r.WithBinaries = true
	message = targetNotFoundOrIgnoredAncestorMessage(&r, "absent.jar", 0, platform.Palette{})
	if strings.Contains(message, "--with-binaries") {
		t.Fatalf("already enabled: %s", message)
	}
}

func BenchmarkBinaryGlobHint(b *testing.B) {
	r := Resolver{lastClassificationExcludedBinary: true}
	b.ReportAllocs()
	for b.Loop() {
		_ = globZeroMatchWarning(&r, "assets/*.jar", 0, platform.Palette{})
	}
}
