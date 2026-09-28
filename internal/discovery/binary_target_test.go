package discovery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func TestExactBinaryTargetDiagnosticsDoNotRediscover(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore": "bin/\nempty/\n",
		"bin/tool":   "a\x00b", "images/icon.png": "\x00png",
		"text/main.go": "package main\n", "empty-image.png": "",
	})
	if err := os.Mkdir(filepath.Join(project, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target  string
		binary  bool
		entries int
		walks   int
	}{
		{"bin/tool", true, 0, 1}, {"bin", true, 0, 1},
		{"./bin/", true, 0, 1}, {"images", true, 0, 1},
		{"empty", false, 0, 1}, {"empty-image.png", false, 1, 1},
		{"text", false, 1, 1},
	} {
		t.Run(tc.target, func(t *testing.T) {
			var events []search.MembershipEnumerationEvent
			defer search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) { events = append(events, event) })()
			got, err := EvaluateScope(command.Invocation{WorkingDir: project, Headless: true}, git.Context{}, 0,
				command.ExecutionScope{Targets: []string{tc.target}}, io.Discard, platform.Palette{})
			if err != nil || len(got.Entries) != tc.entries {
				t.Fatalf("scope=%+v err=%v", got, err)
			}
			if len(events) != tc.walks {
				t.Fatalf("unexpected membership walks: %+v", events)
			}
			var message string
			for _, d := range got.Diagnostics {
				message += d.Message
				if d.IsTargetNotFound || d.IsError {
					t.Fatalf("existing target misreported: %+v", d)
				}
			}
			if strings.Contains(message, "--with-binaries") != tc.binary || strings.Contains(message, "ignored") {
				t.Fatalf("wrong diagnostic: %s", message)
			}
			if tc.binary && !diagnosticsExplainTargetRequest(got.Diagnostics) {
				t.Fatalf("empty target not explained: %+v", got)
			}
		})
	}
}

func TestExactBinaryTargetsKeepBatchedCachedClassification(t *testing.T) {
	files := map[string]string{".gitignore": "bin/\n"}
	var targets []string
	for i := 0; i < 40; i++ {
		target := fmt.Sprintf("bin/tool-%03d", i)
		files[target] = "a\x00b"
		targets = append(targets, target)
	}
	files["empty.png"] = ""
	targets = append(targets, "empty.png")
	project := writeNoIgnoreTargetFixture(t, files)
	var events []search.MembershipEnumerationEvent
	defer search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) { events = append(events, event) })()
	for pass := 0; pass < 2; pass++ {
		got, err := EvaluateScope(command.Invocation{WorkingDir: project, Headless: true}, git.Context{}, 0,
			command.ExecutionScope{Targets: targets}, io.Discard, platform.Palette{})
		if err != nil || len(got.Entries) != 1 || got.Entries[0].RelPath != "empty.png" || len(got.Diagnostics) != 40 {
			t.Fatalf("pass %d: scope=%+v err=%v", pass, got, err)
		}
		for _, d := range got.Diagnostics {
			if !strings.Contains(d.Message, "--with-binaries") || d.ExplainsEmptyResult || d.IsTargetNotFound {
				t.Fatalf("incorrect per-file/cached evidence: %+v", d)
			}
		}
		if len(events) != 1 || events[0].Context.Reason != search.MembershipReasonTextSetFallback {
			t.Fatalf("classification repeated discovery or an ignored-path probe: %+v", events)
		}
	}
}

func TestExactEmptyTargetBinaryEvidenceDoesNotLeak(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{"bin/tool": "a\x00b"})
	if err := os.Mkdir(filepath.Join(project, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}}
	for _, target := range []string{"bin", "empty"} {
		_, handled, diagnostic, err := r.resolveExactTarget(target, false, platform.Palette{})
		if err != nil || !handled {
			t.Fatalf("%s: diagnostic=%+v err=%v", target, diagnostic, err)
		}
		if (diagnostic != nil) != (target == "bin") {
			t.Fatalf("binary evidence leaked across targets: %+v", diagnostic)
		}
	}
}
