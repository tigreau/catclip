package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
)

// Historical rg classifier batch size, retained only for benchmark oracles
// and coverage of the general command-argument limiter.
const residuePathChunkMaxCount = 1024

func rgNULOracle(t testing.TB, root string, paths []string) map[string]struct{} {
	t.Helper()
	bin, ok := RipgrepBinary()
	if !ok {
		t.Fatal("bundled rg required")
	}
	cmd := exec.Command(bin, append([]string{"--files-without-match", "--text", "--no-messages", "-0", "-e", `\x00`, "--"}, paths...)...)
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() > 2 {
			t.Fatal(err)
		}
	}
	out := make(map[string]struct{})
	for _, p := range splitNullSeparated(raw) {
		out[normalizeRelPath(p)] = struct{}{}
	}
	return out
}

func TestGoClassifierDecodedNULParity(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	root := t.TempDir()
	fixtures := [][]byte{
		nil, {'a'}, {0}, []byte("hello\r\n雪"), {0xff, 'a', 0x80}, {0x80, 0},
		{0xef, 0xbb, 0xbf, 'a'}, {0xef, 0xbb, 0xbf, 0},
		{0xff, 0xfe}, {0xfe, 0xff}, {0xff, 0xfe, 'a', 0}, {0xfe, 0xff, 0, 'a'},
		{0xff, 0xfe, 'a', 0, 0, 0}, {0xfe, 0xff, 0, 'a', 0, 0},
		{0xff, 0xfe, 0, 0xd8, 0}, {0xfe, 0xff, 0xd8, 0, 0, 0},
		{0xff, 0xfe, 0}, {0xfe, 0xff, 0}, {0xff, 0xfe, 0, 1, 1, 0},
		{0xff, 0xfe, 0, 0}, {0, 0, 0xfe, 0xff},
		bytes.Repeat([]byte{'a'}, (1<<20)+1),
		append(bytes.Repeat([]byte{'a'}, (1<<20)+1), 0),
		append([]byte{0xff, 0xfe}, bytes.Repeat([]byte{'a', 0}, textScanBufferSize+1)...),
	}
	for _, n := range []int{textScanBufferSize - 2, textScanBufferSize - 1, textScanBufferSize, textScanBufferSize + 1} {
		fixtures = append(fixtures, append(bytes.Repeat([]byte{'a'}, n), 0))
	}
	var paths []string
	for i, body := range fixtures {
		p := fmt.Sprintf("fixture-%02d.unknown", i)
		if err := os.WriteFile(filepath.Join(root, p), body, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	want := rgNULOracle(t, root, paths)
	for i, body := range fixtures {
		p := paths[i]
		_, text := want[p]
		for _, size := range []int{2, 3, 7, textScanBufferSize} {
			for _, r := range []io.Reader{bytes.NewReader(body), iotest.OneByteReader(bytes.NewReader(body)), iotest.DataErrReader(bytes.NewReader(body))} {
				got, err := scanDecodedNUL(context.Background(), r, make([]byte, size))
				if err != nil || (got == contentText) != text {
					t.Fatalf("%s buffer=%d class=%d text=%v err=%v", p, size, got, text, err)
				}
			}
		}
	}
	for _, subset := range [][]string{nil, paths[:1], paths[:2], paths, append(append([]string(nil), paths...), paths...)} {
		got, err := classifyResidue(context.Background(), root, subset)
		if err != nil {
			t.Fatal(err)
		}
		expected := make(map[string]struct{})
		for _, p := range subset {
			if _, ok := want[p]; ok {
				expected[p] = struct{}{}
			}
		}
		if !reflect.DeepEqual(got.text, expected) {
			t.Fatalf("%d-input membership mismatch", len(subset))
		}
	}
}

func TestGoClassifierReadErrorsAndEarlyRejection(t *testing.T) {
	failure := errors.New("read failure")
	for _, tc := range []struct {
		data []byte
		want contentClass
	}{
		{[]byte("hello"), contentUnconfirmed}, {[]byte("\x00x"), contentBinary},
		{[]byte("xx\x00"), contentBinary},
	} {
		r := io.MultiReader(bytes.NewReader(tc.data), iotest.ErrReader(failure))
		got, err := scanDecodedNUL(context.Background(), r, make([]byte, 3))
		if got != tc.want || (got == contentUnconfirmed && !errors.Is(err, failure)) {
			t.Fatalf("class=%d err=%v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanDecodedNUL(ctx, bytes.NewReader(nil), make([]byte, 2)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	r := &cancelScanReader{cancel: cancel}
	if _, err := scanDecodedNUL(ctx, r, make([]byte, 3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("between-read cancellation: %v", err)
	}
}

type cancelScanReader struct{ cancel context.CancelFunc }

func (r *cancelScanReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.cancel()
	return len(p), nil
}

func TestGoClassifierCancellationJoinsWorkers(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var active atomic.Int32
	scan := func(ctx context.Context, _ string, _ []byte) fileClassification {
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		<-release
		return fileClassification{class: contentText}
	}
	done := make(chan error, 1)
	go func() {
		got, err := classifyResidueGo(ctx, "", make([]string, 100), scan)
		if got.text != nil {
			err = errors.New("published cancelled result")
		}
		done <- err
	}()
	for range 4 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("returned before workers joined: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || active.Load() != 0 {
			t.Fatalf("err=%v active=%d", err, active.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not stop")
	}
}

func TestGoClassifierStatReuseAndSpecialFiles(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	root := t.TempDir()
	paths := []string{"text.unknown", "empty.unknown", "nul.unknown", "missing.unknown", "directory.unknown"}
	for p, body := range map[string]string{paths[0]: "hello", paths[1]: "", paths[2]: "\x00"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, paths[4]), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("text.unknown", filepath.Join(root, "link.unknown")); err == nil {
		paths = append(paths, "link.unknown")
	}
	got, err := classifyResidue(context.Background(), root, paths)
	if err != nil || len(got.text) != 2 || len(got.binary) != 1 || len(got.observed) != len(paths) {
		t.Fatalf("classification=%+v err=%v", got, err)
	}
	if err := os.Remove(filepath.Join(root, "empty.unknown")); err != nil {
		t.Fatal(err)
	}
	selected := make(map[string]struct{})
	stats, admitted := admitEmptyFilesToTextSet(root, paths, selected, nil, got.observed)
	if stats != 0 || admitted != 1 {
		t.Fatalf("repeated stat: stats=%d admitted=%d", stats, admitted)
	}
	set, capture, err := ClassifyTextPathsWithSizeCapture(root, []string{"text.unknown", "nul.unknown", "missing.unknown"})
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Stop()
	<-capture.Done()
	if len(set) != 1 {
		t.Fatalf("text set=%v", set)
	}
	m := capture.MetadataSnapshot()
	if len(m) != 1 || m["text.unknown"].SizeBytes != 5 || m["text.unknown"].ModTime.IsZero() {
		t.Fatalf("metadata=%+v", m)
	}
}

func TestClassifierConfigIndependenceAndCancellation(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "rg-config")
	if err := os.WriteFile(config, []byte("--encoding\nutf-16le\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.unknown"), []byte{'a', 0, 'b', 0}, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", config)
	got, err := ClassifyTextPaths(root, []string{"data.unknown"})
	if err != nil || len(got) != 0 {
		t.Fatalf("rg configuration changed classification: %v %v", got, err)
	}
	t.Setenv("CATCLIP_RG", filepath.Join(root, "missing-rg"))
	if err := os.WriteFile(filepath.Join(root, "text.unknown"), []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	if set, err := ClassifyTextPaths(root, []string{"text.unknown"}); err != nil || len(set) != 1 {
		t.Fatalf("classifier required rg: %v %v", set, err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	got, err = ClassifyTextPaths(root, []string{"data.unknown"})
	if err != nil || len(got) != 0 {
		t.Fatalf("unconfigured byte NUL lost: %v %v", got, err)
	}
	saved := reloadCancelCtx
	defer func() { reloadCancelCtx = saved }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reloadCancelCtx = ctx
	for _, cfg := range []string{"", config} {
		t.Setenv("RIPGREP_CONFIG_PATH", cfg)
		for _, paths := range [][]string{nil, {"known.txt"}, {"a.unknown"}, {"a.unknown", "b.unknown", "c.unknown"}} {
			set, capture, err := ClassifyTextPathsWithSizeCapture(root, paths)
			if !errors.Is(err, context.Canceled) || set != nil || capture != nil {
				t.Fatalf("cancelled generation: set=%v capture=%v err=%v", set, capture, err)
			}
		}
	}
}

func BenchmarkGoClassification(b *testing.B) {
	b.Setenv("RIPGREP_CONFIG_PATH", "")
	root := b.TempDir()
	for _, count := range []int{1, 2, 32} {
		paths := make([]string, count)
		for i := range paths {
			paths[i] = fmt.Sprintf("%d.unknown", i)
			if err := os.WriteFile(filepath.Join(root, paths[i]), bytes.Repeat([]byte("text\n"), 100), 0600); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := classifyEnumeratedTextPaths(root, paths); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
