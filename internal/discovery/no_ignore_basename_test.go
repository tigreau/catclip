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

func TestNoIgnoreBasenameResolutionPreservesPriorityAndAttribution(t *testing.T) {
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

	wants := map[string]struct {
		path        string
		allowed     bool
		blockSource string
	}{
		"config.ts":    {path: "src/config.ts"},
		"from-git.ts":  {path: "generated/from-git.ts", allowed: true, blockSource: ".gitignore"},
		"from-hiss.ts": {path: "local/from-hiss.ts", allowed: true, blockSource: ".hiss"},
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
		if got.RelPath != want.path || got.AllowedByInclude != want.allowed || got.BlockSource != want.blockSource {
			t.Fatalf("resolveAndDiscoverTarget(%q) attribution = %+v, want path=%q allowed=%v source=%q", basename, got, want.path, want.allowed, want.blockSource)
		}
		if !want.allowed && !got.GitVisible {
			t.Fatalf("resolveAndDiscoverTarget(%q) visible entry was not marked visible: %+v", basename, got)
		}
	}
}

func TestNoIgnoreFuzzyFallbackPreservesVisibleFirstTiers(t *testing.T) {
	t.Run("visible fuzzy result wins before ignored fuzzy fallback", func(t *testing.T) {
		project := writeNoIgnoreTargetFixture(t, map[string]string{
			".gitignore":          "generated/\n",
			"generated/Login.tsx": "export const hidden = true\n",
			"src/LoginPage.tsx":   "export const visible = true\n",
		})
		resolver := Resolver{Cfg: command.Invocation{WorkingDir: project, Headless: true}, NoIgnore: true}

		entries, diagnostics, _, cancelled, err := resolver.resolveAndDiscoverTarget(0, "lgn", io.Discard, platform.Palette{})
		if err != nil {
			t.Fatalf("resolveAndDiscoverTarget: %v", err)
		}
		if cancelled || len(diagnostics) != 0 || len(entries) != 1 || entries[0].RelPath != "src/LoginPage.tsx" {
			t.Fatalf("entries=%+v diagnostics=%+v cancelled=%v", entries, diagnostics, cancelled)
		}
	})

	t.Run("ignored fuzzy fallback resolves after visible miss", func(t *testing.T) {
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
		if got := entries[0]; got.RelPath != "generated/Login.tsx" || !got.AllowedByInclude || got.BlockSource != ".gitignore" {
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

	t.Run("visible exact ambiguity excludes ignored duplicate", func(t *testing.T) {
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
		if strings.Contains(message, "generated/cache") {
			t.Fatalf("ignored lower-tier duplicate leaked into visible ambiguity:\n%s", message)
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
	if probe.Outcome != StartupTargetIncludedUnique || len(probe.Matches) != 1 || probe.Matches[0].Path != "generated/cache" {
		t.Fatalf("cache probe = %+v", probe)
	}

	probe, err = resolver.ProbeStartupTarget("lgn")
	if err != nil {
		t.Fatalf("ProbeStartupTarget(lgn): %v", err)
	}
	if probe.Outcome != StartupTargetIncludedAmbiguous || len(probe.Matches) != 2 {
		t.Fatalf("lgn probe = %+v", probe)
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
