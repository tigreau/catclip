//go:build linux || darwin

package search

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestGoClassifierDoesNotOpenFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan fileClassification, 1)
	go func() {
		done <- classifyRegularFile(ctx, filepath.Join(root, "pipe.unknown"), make([]byte, textScanBufferSize))
	}()
	select {
	case result := <-done:
		if result.class != contentUnconfirmed || result.info == nil || result.info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("result=%+v", result)
		}
	case <-ctx.Done():
		t.Fatal("scanner blocked opening FIFO")
	}
}
