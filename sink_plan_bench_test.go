package catclip

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func benchSinkProject(tb testing.TB, n int) func() {
	tb.Helper()
	project := tb.TempDir()
	for i := 0; i < n; i++ {
		rel := filepath.Join("src", "pkg"+strconv.Itoa(i%20), "f"+strconv.Itoa(i)+".go")
		body := "package p\n\nfunc F" + strconv.Itoa(i) + "() {\n\t// TODO mark " + strconv.Itoa(i) + "\n\tx := " + strconv.Itoa(i) + "\n}\n\nvar v = " + strconv.Itoa(i) + "\n"
		abs := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(project); err != nil {
		tb.Fatal(err)
	}
	return func() { os.Chdir(orig) }
}

// BenchmarkSinkPlanBuildByFlag compares the sink-picker plan build
// (buildOutputPlanForDiscoveredInvocation) across output modes. --contains is
// full-file (stat only); --lines and --snippet N read each file at prep.
func BenchmarkSinkPlanBuildByFlag(b *testing.B) {
	restore := benchSinkProject(b, 2000)
	defer restore()
	gitCtx := detectGitContext(mustGetwd(b))

	cases := map[string][]string{
		"contains":  {".", "--contains", "TODO"},
		"lines":     {".", "--lines", "1", "20"},
		"snippet_N": {".", "--snippet", "TODO", "3"},
	}
	for name, args := range cases {
		cfg, err := parseArgs(args)
		if err != nil {
			b.Fatalf("%s parse: %v", name, err)
		}
		cfg.WorkingDir = mustGetwd(b)
		disc, err := discoverInvocation(resolvedInvocationFromParsedCommand(cfg), gitCtx, io.Discard, colorPalette{})
		if err != nil {
			b.Fatalf("%s discover: %v", name, err)
		}
		b.Run(name, func(b *testing.B) {
			for range b.N {
				if _, err := buildOutputPlanForDiscoveredInvocation(gitCtx, disc.Invocation); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func mustGetwd(tb testing.TB) string {
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	return wd
}
