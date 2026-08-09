package discovery

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

func TestNoIgnoreBasenameResolutionUsesCompleteUniverseAndPreservesAttribution(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(project, "config")
	t.Setenv("XDG_CONFIG_HOME", configDir)

	for rel, contents := range map[string]string{
		".gitignore":            "generated/\n",
		"generated/config.ts":   "export const hiddenDuplicate = true\n",
		"generated/from-git.ts": "export const git = true\n",
		"local/from-hiss.ts":    "export const hiss = true\n",
		"src/config.ts":         "export const visible = true\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hissPath := filepath.Join(configDir, "catclip", ".hiss")
	if err := os.MkdirAll(filepath.Dir(hissPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hissPath, []byte("local/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := Resolver{
		Cfg:      command.Invocation{WorkingDir: project, Headless: true},
		NoIgnore: true,
		WantedBasenames: map[string]struct{}{
			"config.ts":    {},
			"from-git.ts":  {},
			"from-hiss.ts": {},
		},
	}

	_, _, _, _, err := resolver.resolveAndDiscoverTarget(0, "config.ts", io.Discard, platform.Palette{})
	if err == nil {
		t.Fatal("expected visible and ignored config.ts files to be ambiguous")
	}
	for _, want := range []string{"src/config.ts", "generated/config.ts"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("combined ambiguity missing %q:\n%s", want, err)
		}
	}

	wants := map[string]struct {
		path        string
		blockSource string
	}{
		"from-git.ts":  {path: "generated/from-git.ts", blockSource: ".gitignore"},
		"from-hiss.ts": {path: "local/from-hiss.ts", blockSource: ".hiss"},
	}
	for basename, want := range wants {
		entries, diagnostics, _, cancelled, err := resolver.resolveAndDiscoverTarget(0, basename, io.Discard, platform.Palette{})
		if err != nil {
			t.Fatalf("resolveAndDiscoverTarget(%q): %v", basename, err)
		}
		if cancelled || len(diagnostics) != 0 || len(entries) != 1 {
			t.Fatalf("resolveAndDiscoverTarget(%q) entries=%+v diagnostics=%+v cancelled=%v", basename, entries, diagnostics, cancelled)
		}
		got := entries[0]
		if got.RelPath != want.path || !got.IgnoreBypassed || got.BlockSource != want.blockSource {
			t.Fatalf("resolveAndDiscoverTarget(%q) attribution = %+v, want path=%q bypassed source=%q", basename, got, want.path, want.blockSource)
		}
	}
}

func TestNoIgnoreFuzzyResolutionUsesCompleteUniverse(t *testing.T) {
	t.Run("visible and ignored fuzzy results share one ambiguity set", func(t *testing.T) {
		project := writeNoIgnoreTargetFixture(t, map[string]string{
			".gitignore":          "generated/\n",
			"generated/Login.tsx": "export const hidden = true\n",
			"src/LoginPage.tsx":   "export const visible = true\n",
		})
		resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

		_, _, _, _, err := resolver.resolveAndDiscoverTarget(0, "lgn", io.Discard, platform.Palette{})
		if err == nil {
			t.Fatal("expected visible and ignored fuzzy results to be ambiguous")
		}
		for _, want := range []string{"src/LoginPage.tsx", "generated/Login.tsx"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("combined ambiguity missing %q:\n%s", want, err)
			}
		}
	})

	t.Run("unique ignored fuzzy result resolves", func(t *testing.T) {
		project := writeNoIgnoreTargetFixture(t, map[string]string{
			".gitignore":          "generated/\n",
			"generated/Login.tsx": "export const hidden = true\n",
		})
		resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

		entries, diagnostics, _, cancelled, err := resolver.resolveAndDiscoverTarget(0, "lgn", io.Discard, platform.Palette{})
		if err != nil {
			t.Fatalf("resolveAndDiscoverTarget: %v", err)
		}
		if cancelled || len(diagnostics) != 0 || len(entries) != 1 {
			t.Fatalf("entries=%+v diagnostics=%+v cancelled=%v", entries, diagnostics, cancelled)
		}
		if got := entries[0]; got.RelPath != "generated/Login.tsx" || !got.IgnoreBypassed || got.BlockSource != ".gitignore" {
			t.Fatalf("ignored fuzzy attribution = %+v", got)
		}
	})

	t.Run("ignored exact directory outranks visible fuzzy file", func(t *testing.T) {
		project := writeNoIgnoreTargetFixture(t, map[string]string{
			".gitignore":                "generated/\n",
			"generated/cache/hidden.ts": "export const hidden = true\n",
			"src/cacheClient.ts":        "export const visible = true\n",
		})
		resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

		entries, diagnostics, _, cancelled, err := resolver.resolveAndDiscoverTarget(0, "cache", io.Discard, platform.Palette{})
		if err != nil {
			t.Fatalf("resolveAndDiscoverTarget: %v", err)
		}
		if cancelled || len(diagnostics) != 0 || len(entries) != 1 || entries[0].RelPath != "generated/cache/hidden.ts" {
			t.Fatalf("entries=%+v diagnostics=%+v cancelled=%v", entries, diagnostics, cancelled)
		}
	})

	t.Run("exact ambiguity includes visible and ignored duplicates", func(t *testing.T) {
		project := writeNoIgnoreTargetFixture(t, map[string]string{
			".gitignore":                  "generated/\n",
			"a/cache/one.ts":              "export const one = true\n",
			"b/cache/two.ts":              "export const two = true\n",
			"generated/cache/hidden.ts":   "export const hidden = true\n",
			"generated/cache/ignored2.ts": "export const hidden2 = true\n",
		})
		resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

		_, _, _, _, err := resolver.resolveAndDiscoverTarget(0, "cache", io.Discard, platform.Palette{})
		if err == nil {
			t.Fatal("expected visible exact basename ambiguity")
		}
		message := err.Error()
		for _, want := range []string{"a/cache", "b/cache"} {
			if !strings.Contains(message, want) {
				t.Fatalf("ambiguity error missing %q:\n%s", want, message)
			}
		}
		if !strings.Contains(message, "generated/cache") {
			t.Fatalf("combined ambiguity omitted ignored duplicate:\n%s", message)
		}
	})

	t.Run("binary-only ignored directory is not a text target fallback", func(t *testing.T) {
		project := writeNoIgnoreTargetFixture(t, map[string]string{
			".gitignore":                      "generated/\n",
			"generated/binary-cache/blob.bin": "\x00\x01\x02",
		})
		resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

		entries, diagnostics, _, cancelled, err := resolver.resolveAndDiscoverTarget(0, "bny", io.Discard, platform.Palette{})
		if err != nil {
			t.Fatalf("resolveAndDiscoverTarget: %v", err)
		}
		if cancelled || len(entries) != 0 || len(diagnostics) == 0 {
			t.Fatalf("entries=%+v diagnostics=%+v cancelled=%v", entries, diagnostics, cancelled)
		}
	})
}

func TestProbeStartupTargetNoIgnoreUsesIgnoredFuzzyFallback(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":                "generated/\n",
		"generated/Login.tsx":       "export const login = true\n",
		"generated/LoginForm.tsx":   "export const form = true\n",
		"generated/cache/hidden.ts": "export const cache = true\n",
		"src/cacheClient.ts":        "export const visible = true\n",
	})
	resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

	probe, err := resolver.ProbeStartupTarget("cache")
	if err != nil {
		t.Fatalf("ProbeStartupTarget(cache): %v", err)
	}
	if probe.Outcome != StartupTargetUniqueFuzzy || len(probe.Matches) != 1 || probe.Matches[0].Path != "generated/cache" {
		t.Fatalf("cache probe = %+v", probe)
	}

	probe, err = resolver.ProbeStartupTarget("lgn")
	if err != nil {
		t.Fatalf("ProbeStartupTarget(lgn): %v", err)
	}
	if probe.Outcome != StartupTargetAmbiguousFuzzy || len(probe.Matches) != 2 {
		t.Fatalf("lgn probe = %+v", probe)
	}
}

func TestAllNoIgnoreTargetsContainsVisibleAndIgnoredRows(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":          "generated/\n",
		"src/LoginPage.tsx":   "export const visible = true\n",
		"generated/Login.tsx": "export const ignored = true\n",
	})
	resolver := Resolver{Cfg: command.Invocation{WorkingDir: project}, NoIgnore: true}

	targets, err := resolver.AllNoIgnoreTargets(nil)
	if err != nil {
		t.Fatalf("AllNoIgnoreTargets: %v", err)
	}
	byPath := make(map[string]TargetMatch, len(targets))
	for _, target := range targets {
		byPath[target.Path] = target
	}
	if got, ok := byPath["src/LoginPage.tsx"]; !ok || got.Ignored {
		t.Fatalf("visible row missing or mislabeled: %+v (present=%v)", got, ok)
	}
	if got, ok := byPath["generated/Login.tsx"]; !ok || !got.Ignored || got.IgnoreSource != ".gitignore" {
		t.Fatalf("ignored row missing or mislabeled: %+v (present=%v)", got, ok)
	}
}

func writeNoIgnoreTargetFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range files {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return project
}
