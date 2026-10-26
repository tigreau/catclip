package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPathClassificationReuseAcrossRootsAndSubsets(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"a.audit": "text", "b.audit": "\x00", "c.audit": "later"} {
		if err := os.WriteFile(filepath.Join(root, "src", name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := classifyResidue(context.Background(), root, []string{"src/a.audit", "src/b.audit"})
	if err != nil || len(first.scanned) != 2 || len(first.text) != 1 || len(first.binary) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// The same files selected relative to a different root share evidence, but
	// keep the caller's relative paths. An earlier subset must not hide c.audit.
	second, err := classifyResidue(context.Background(), filepath.Join(root, "src"), []string{"a.audit", "b.audit", "c.audit"})
	if err != nil || !reflect.DeepEqual(second.scanned, []string{"c.audit"}) || len(second.text) != 2 || len(second.binary) != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if second.observed["a.audit"] != first.observed["src/a.audit"] {
		t.Fatal("did not reuse metadata observation")
	}
	set, capture, err := ClassifyTextPathsWithSizeCapture(root, []string{"src/a.audit", "src/b.audit"})
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Stop()
	<-capture.Done()
	if len(set) != 1 || capture.MetadataSnapshot()["src/a.audit"].SizeBytes != 4 {
		t.Fatalf("lost cached text size: set=%v metadata=%v", set, capture.MetadataSnapshot())
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "a.audit"), []byte{0}, 0600); err != nil {
		t.Fatal(err)
	}
	third, err := classifyResidue(context.Background(), other, []string{"a.audit"})
	if err != nil || len(third.binary) != 1 || len(third.scanned) != 1 {
		t.Fatalf("different root reused unrelated evidence: %+v %v", third, err)
	}
}

func TestPathClassificationCacheRetriesUnconfirmedAndCancelled(t *testing.T) {
	for _, reason := range []string{"unconfirmed", "cancelled"} {
		t.Run(reason, func(t *testing.T) {
			var cache pathClassificationCache
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			scan := func(context.Context, string, []byte) fileClassification {
				calls++
				if calls == 1 {
					if reason == "cancelled" {
						cancel()
						return fileClassification{class: contentText}
					}
					return fileClassification{}
				}
				return fileClassification{class: contentBinary}
			}
			cache.classify(ctx, "path", nil, scan)
			got := cache.classify(context.Background(), "path", nil, scan)
			if calls != 2 || got.class != contentBinary || got.reused {
				t.Fatalf("retry=%+v calls=%d", got, calls)
			}
			got = cache.classify(context.Background(), "path", nil, scan)
			if calls != 2 || !got.reused || got.class != contentBinary {
				t.Fatalf("reuse=%+v calls=%d", got, calls)
			}
		})
	}
}

func TestPathClassificationCacheConcurrentReadersAndCancellation(t *testing.T) {
	var cache pathClassificationCache
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	scan := func(context.Context, string, []byte) fileClassification {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return fileClassification{class: contentText}
	}
	owner := make(chan fileClassification, 1)
	go func() { owner <- cache.classify(context.Background(), "path", nil, scan) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan fileClassification, 1)
	go func() { waiter <- cache.classify(ctx, "path", nil, scan) }()
	cancel()
	select {
	case got := <-waiter:
		if got.class != contentUnconfirmed {
			t.Fatalf("cancelled waiter=%+v", got)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("cancelled waiter blocked on owner")
	}
	const readers = 16
	results := make(chan fileClassification, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.classify(context.Background(), "path", nil, scan)
		}()
	}
	close(release)
	if got := <-owner; got.class != contentText || got.reused {
		t.Fatalf("owner=%+v", got)
	}
	wg.Wait()
	close(results)
	for got := range results {
		if got.class != contentText || !got.reused {
			t.Fatalf("reader=%+v", got)
		}
	}
	if calls.Load() != 1 || len(cache.pending) != 0 {
		t.Fatalf("calls=%d pending=%d", calls.Load(), len(cache.pending))
	}
}

func TestPathClassificationCacheCancelledOwnerAllowsRetry(t *testing.T) {
	var cache pathClassificationCache
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var calls atomic.Int32
	scan := func(ctx context.Context, _ string, _ []byte) fileClassification {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return fileClassification{}
		}
		return fileClassification{class: contentText}
	}
	ownerDone := make(chan struct{})
	go func() {
		cache.classify(ctx, "path", nil, scan)
		close(ownerDone)
	}()
	<-entered
	retry := make(chan fileClassification, 1)
	go func() { retry <- cache.classify(context.Background(), "path", nil, scan) }()
	cancel()
	select {
	case got := <-retry:
		if got.class != contentText || got.reused || calls.Load() != 2 {
			t.Fatalf("retry=%+v calls=%d", got, calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled owner left retry blocked")
	}
	<-ownerDone
}

// Compare the same warm filesystem with no cache, a fresh invocation cache,
// and a repeated scope. Fresh constructs a new cache on every iteration.
func BenchmarkPathClassificationReuse(b *testing.B) {
	root := b.TempDir()
	paths := make([]string, 256)
	for i := range paths {
		paths[i] = fmt.Sprintf("file-%d.audit", i)
		if err := os.WriteFile(filepath.Join(root, paths[i]), []byte("text content\n"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	for _, mode := range []string{"uncached", "first", "repeat"} {
		b.Run(mode, func(b *testing.B) {
			var cache pathClassificationCache
			scan := func(ctx context.Context, abs string, buf []byte) fileClassification {
				return cache.classify(ctx, abs, buf, classifyRegularFile)
			}
			if mode == "uncached" {
				scan = classifyRegularFile
			}
			if mode == "repeat" {
				if _, err := classifyResidueGo(context.Background(), root, paths, scan); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if mode == "first" {
					cache = pathClassificationCache{}
				}
				if _, err := classifyResidueGo(context.Background(), root, paths, scan); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
