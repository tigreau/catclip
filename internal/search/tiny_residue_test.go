package search

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func rgTinyOracle(t testing.TB, dir string, paths []string) map[string]struct{} {
	t.Helper()
	bin, ok := RipgrepBinary()
	if !ok {
		t.Skip("rg unavailable")
	}
	args := append([]string{"--files-without-match", "--text", "--no-messages", "-0", "-e", `\x00`, "--"}, paths...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
	result := make(map[string]struct{})
	for _, rel := range splitNullSeparated(out) {
		if rel = normalizeRelPath(rel); rel != "" && rel != "." {
			result[rel] = struct{}{}
		}
	}
	return result
}

func TestTinyResidueMatchesRipgrep(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	root := t.TempDir()
	fixtures := map[string][]byte{
		"empty.unknown": nil, "one.unknown": []byte("x"), "nul.unknown": {0},
		"text space.unknown": []byte("hello\r\nworld"), "utf8.unknown": []byte("é雪\n"),
		"invalid.unknown": {0xff, 'a', 0x80}, "invalidnul.unknown": {0x80, 0},
		"utf16le.unknown":  {0xff, 0xfe, 'a', 0, '\n', 0},
		"utf16be.unknown":  {0xfe, 0xff, 0, 'a', 0, '\n'},
		"utf16nul.unknown": {0xff, 0xfe, 'a', 0, 0, 0},
		"late-nul.unknown": append(bytes.Repeat([]byte("a"), 65536), 0),
		"large.unknown":    bytes.Repeat([]byte("a"), tinyResidueMaxBytes+1),
	}
	for name, body := range fixtures {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixtures["missing.unknown"] = nil
	if err := os.Symlink("text space.unknown", filepath.Join(root, "link.unknown")); err == nil {
		fixtures["link.unknown"] = nil
	}
	for name := range fixtures {
		t.Run(name, func(t *testing.T) {
			for _, paths := range [][]string{{name}, {name, "text space.unknown"}} {
				want := rgTinyOracle(t, root, paths)
				got, err := runRipgrepNulScanFiles(root, paths)
				if err != nil || !reflect.DeepEqual(want, got) {
					t.Fatalf("paths=%v got=%v want=%v err=%v", paths, got, want, err)
				}
			}
		})
	}
}

func TestTinyResidueLimitsConfigAndCancellation(t *testing.T) {
	paths := []string{"a", "b", "c"}
	_, fallback, err := scanTinyResidue(context.Background(), t.TempDir(), paths)
	if err != nil || !reflect.DeepEqual(fallback, paths) {
		t.Fatal("large residue did not keep rg")
	}
	t.Setenv("RIPGREP_CONFIG_PATH", "configured")
	_, fallback, err = scanTinyResidue(context.Background(), t.TempDir(), paths[:1])
	if err != nil || !reflect.DeepEqual(fallback, paths[:1]) {
		t.Fatal("configured rg was bypassed")
	}
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = scanTinyResidue(ctx, t.TempDir(), paths[:1])
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestTinyResidueStatObservationsFeedMetadataAndEmptyAdmission(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.unknown")
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	set, capture, err := ClassifyTextPathsWithSizeCapture(root, []string{"a.unknown"})
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Stop()
	<-capture.Done()
	if _, ok := set["a.unknown"]; !ok {
		t.Fatal("missing text")
	}
	if metadata := capture.MetadataSnapshot()["a.unknown"]; metadata.SizeBytes != 5 || metadata.ModTime.IsZero() || !metadata.Mode.IsRegular() {
		t.Fatalf("lost classifier stat: %+v", metadata)
	}
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	selected := make(map[string]struct{})
	stats, admitted := admitEmptyFilesToTextSet(root, []string{"a.unknown"}, selected, nil, map[string]os.FileInfo{"a.unknown": info})
	if stats != 0 || admitted != 1 {
		t.Fatalf("empty admission repeated a completed stat: stats=%d admitted=%d", stats, admitted)
	}
}

func TestResidueCancellationCoversLocalAndBatchedPaths(t *testing.T) {
	if _, ok := RipgrepBinary(); !ok {
		t.Skip("rg unavailable")
	}
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	saved := reloadCancelCtx
	defer func() { reloadCancelCtx = saved }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reloadCancelCtx = ctx
	for _, paths := range [][]string{{"a.unknown"}, {"a.unknown", "b.unknown", "c.unknown"}} {
		if _, err := runRipgrepNulScanFiles(t.TempDir(), paths); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled %d-file scan: %v", len(paths), err)
		}
	}
}

func BenchmarkTinyResidue(b *testing.B) {
	b.Setenv("RIPGREP_CONFIG_PATH", "")
	root := b.TempDir()
	paths := []string{"a.unknown", "b.unknown"}
	for _, name := range paths {
		if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte("text\n"), 100), 0600); err != nil {
			b.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"rg", func() { _ = rgTinyOracle(b, root, paths) }},
		{"local", func() {
			if _, err := runRipgrepNulScanFiles(root, paths); err != nil {
				b.Fatal(err)
			}
		}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tc.run()
			}
		})
	}
}
