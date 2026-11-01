package search

import (
	"context"
	"fmt"
	"testing"
)

func TestClassificationProgressWorkerLifecycle(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			snapshot, stop := ObserveClassificationProgress()
			defer stop()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				_, err := classifyResidueGo(ctx, t.TempDir(), []string{"first", "second"}, func(_ context.Context, abs string, _ []byte) fileClassification {
					if snapshot().Completed == 1 {
						close(entered)
						<-release
					}
					return fileClassification{class: contentText}
				})
				finished <- err
			}()
			<-entered
			progress := snapshot()
			if progress.Completed != 1 || progress.Total != 2 || progress.Started.IsZero() {
				t.Errorf("unexpected worker progress: %+v", progress)
			}
			if cancelled {
				cancel()
			}
			close(release)
			err := <-finished
			if (err != nil) != cancelled {
				t.Fatalf("classification error=%v, cancelled=%v", err, cancelled)
			}
			if got := snapshot(); got.Total != 0 {
				t.Fatalf("completed/cancelled batch still active: %+v", got)
			}
			stop()
			if batch, finish := beginClassificationProgress(5); batch != nil {
				finish()
				t.Fatal("accounting stayed enabled after observer stopped")
			}
		})
	}
}

func TestClassificationProgressEmptyBatch(t *testing.T) {
	snapshot, stop := ObserveClassificationProgress()
	defer stop()
	batch, finish := beginClassificationProgress(0)
	defer finish()
	if batch != nil || snapshot().Total != 0 {
		t.Fatal("empty inventory must not announce classification")
	}
}

// Isolate accounting cost from filesystem/Defender variability while retaining
// the real worker pool, path joins, and result collection on a large inventory.
func BenchmarkClassificationProgress(b *testing.B) {
	paths := make([]string, 100000)
	for i := range paths {
		paths[i] = fmt.Sprintf("file-%d.unknown", i)
	}
	for _, enabled := range []bool{false, true} {
		b.Run(fmt.Sprint(enabled), func(b *testing.B) {
			if enabled {
				_, stop := ObserveClassificationProgress()
				defer stop()
			}
			root := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, err := classifyResidueGo(context.Background(), root, paths, func(context.Context, string, []byte) fileClassification {
					return fileClassification{class: contentText}
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
