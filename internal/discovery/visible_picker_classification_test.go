package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestBuildVisibleFileListClassifiesOnlyVisibleWalkPaths(t *testing.T) {
	project := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "ignored/\n")
	write("src/app.ts", "export const app = true;\n")
	write("ignored/dependency.xyz", "ignored residue text\n")

	benchLog := filepath.Join(t.TempDir(), "bench.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", benchLog)
	resolver := Resolver{Cfg: command.Invocation{WorkingDir: project}}
	if err := resolver.BuildVisibleFileList(); err != nil {
		t.Fatal(err)
	}
	if len(resolver.VisibleFileList) != 2 {
		t.Fatalf("expected .gitignore and src/app.ts, got %#v", resolver.VisibleFileList)
	}

	raw, err := os.ReadFile(benchLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	if !strings.Contains(log, `event="search.rg.text_paths" paths="2"`) {
		t.Fatalf("visible conversion did not classify the bounded path set:\n%s", log)
	}
	if strings.Contains(log, `event="search.rg.text_files"`) {
		t.Fatalf("visible conversion unexpectedly classified the project-wide no-ignore universe:\n%s", log)
	}
}
