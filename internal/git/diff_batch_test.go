package git

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPatchBatchingRespectsConfiguredDrivers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_EXTERNAL_DIFF", "")
	if err := os.Unsetenv("GIT_EXTERNAL_DIFF"); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if _, err := Capture(root, args...); err != nil {
			t.Fatal(args, err)
		}
	}
	run("init", "-q")
	ctx := Context{Enabled: true, Root: root}
	run("config", "diff.custom.textconv", "must-not-execute")
	if ok, err := CanBatchPatches(ctx, []string{"a.go", "b.go"}); err != nil || !ok {
		t.Fatalf("unused driver blocked batching: %v %v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.go diff=custom\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := CanBatchPatches(ctx, []string{"a.go", "b.go"}); err != nil || ok {
		t.Fatalf("active driver allowed batching: %v %v", ok, err)
	}
	run("config", "diff.external", "must-not-execute")
	if ok, err := CanBatchPatches(ctx, []string{"a.txt", "b.txt"}); err != nil || ok {
		t.Fatalf("external diff allowed batching: %v %v", ok, err)
	}
}

func TestSplitModifiedPatchesRejectsAmbiguousFormats(t *testing.T) {
	raw := ":100644 100644 abc 000 M\x00a.go\x00\x00"
	patch := "diff --git a/a.go b/a.go\nindex abc..def 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"
	got, ok := splitModifiedPatches(raw+patch, []string{"a.go", "clean.go"})
	if !ok || !reflect.DeepEqual(got, map[string]string{"a.go": patch, "clean.go": ""}) {
		t.Fatal(got, ok)
	}
	for _, out := range []string{
		strings.Replace(raw+patch, " M\x00", " R100\x00", 1),
		strings.Replace(raw+patch, "diff --git a/a.go b/a.go", "diff --git a.go a.go", 1),
		raw + patch + "diff --git a/injected.go b/injected.go\n",
		"custom external output", raw + "", ":bad\x00a.go\x00\x00" + patch,
	} {
		if _, ok := splitModifiedPatches(out, []string{"a.go", "clean.go"}); ok {
			t.Fatalf("accepted unsafe output %q", out)
		}
	}
}

func TestGitPathBatchesStayBounded(t *testing.T) {
	paths := make([]string, 301)
	for i := range paths {
		paths[i] = strings.Repeat("x", 140)
	}
	count := 0
	for _, batch := range gitPathBatches(paths) {
		size := 0
		for _, p := range batch {
			size += 2*len(p) + 3
		}
		if size > 12000 || len(batch) > 100 {
			t.Fatalf("batch %d paths %d bytes", len(batch), size)
		}
		count += len(batch)
	}
	if count != len(paths) {
		t.Fatalf("lost paths %d", count)
	}
}
