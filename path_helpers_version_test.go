package catclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadVersionDoesNotTrustProjectVersionFile(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "VERSION"), []byte("99.99.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	if got, want := loadVersion(), strings.TrimSpace(sourceVersion); got != want {
		t.Fatalf("loadVersion() = %q, want embedded Catclip source version %q", got, want)
	}
}

func TestResolvedVersionPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		release string
		source  string
		want    string
	}{
		{name: "release overrides source", release: " 9.8.7\n", source: "1.2.3\n", want: "9.8.7"},
		{name: "source build", source: " 1.2.3\r\n", want: "1.2.3"},
		{name: "empty metadata", release: " \n", source: "\t", want: "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvedVersion(tc.release, tc.source); got != tc.want {
				t.Fatalf("resolvedVersion(%q, %q) = %q, want %q", tc.release, tc.source, got, tc.want)
			}
		})
	}
}

func TestLoadVersionUsesReleaseOverride(t *testing.T) {
	previous := releaseVersion
	releaseVersion = "7.6.5"
	t.Cleanup(func() { releaseVersion = previous })

	if got := loadVersion(); got != "7.6.5" {
		t.Fatalf("loadVersion() = %q, want linker override", got)
	}
}
