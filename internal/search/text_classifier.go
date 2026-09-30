package search

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

const (
	textScanBufferSize = 32 * 1024
	textScanMaxWorkers = 8
)

type contentClass uint8

const (
	contentUnconfirmed contentClass = iota
	contentText
	contentBinary
)

type fileClassification struct {
	class contentClass
	info  os.FileInfo
}

type residueClassification struct {
	text     map[string]struct{}
	binary   map[string]struct{}
	observed map[string]os.FileInfo
}

// scanDecodedNUL implements rg's unconfigured decoded-NUL definition. UTF-16
// BOMs select aligned code units: only 0000 decodes to NUL, including malformed
// input (replacement characters are not NUL). Other input uses raw byte NULs.
// It rejects at the first NUL and otherwise reads to EOF; it never guesses from
// a prefix, extension, printability or UTF-8 validity.
func scanDecodedNUL(ctx context.Context, r io.Reader, buf []byte) (contentClass, error) {
	if err := ctx.Err(); err != nil {
		return contentUnconfirmed, err
	}
	n, err := io.ReadFull(r, buf[:2])
	utf16 := n == 2 && ((buf[0] == 0xff && buf[1] == 0xfe) || (buf[0] == 0xfe && buf[1] == 0xff))
	if !utf16 && bytes.IndexByte(buf[:n], 0) >= 0 {
		return contentBinary, nil
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return contentText, nil
	}
	if err != nil {
		return contentUnconfirmed, err
	}
	pending := -1
	for {
		if err := ctx.Err(); err != nil {
			return contentUnconfirmed, err
		}
		n, err = r.Read(buf)
		if utf16 {
			for _, b := range buf[:n] {
				if pending < 0 {
					pending = int(b)
				} else {
					if pending == 0 && b == 0 {
						return contentBinary, nil
					}
					pending = -1
				}
			}
		} else if bytes.IndexByte(buf[:n], 0) >= 0 {
			return contentBinary, nil
		}
		if err == io.EOF {
			return contentText, nil
		}
		if err != nil {
			return contentUnconfirmed, err
		}
	}
}

func classifyRegularFile(ctx context.Context, abs string, buf []byte) fileClassification {
	info, err := os.Lstat(abs)
	result := fileClassification{info: info}
	if err != nil || !info.Mode().IsRegular() || ctx.Err() != nil {
		return result
	}
	// Never open symlinks, directories or special files. Filesystem mutation
	// between Lstat/open is outside the invocation's stable-filesystem contract.
	f, err := os.Open(abs)
	if err != nil {
		return result
	}
	defer f.Close()
	result.class, _ = scanDecodedNUL(ctx, f, buf)
	// A failed read stays unconfirmed. Only an observed NUL is binary evidence;
	// cancellation is checked by the pool before any results are published.
	return result
}

func classifyResidue(ctx context.Context, root string, paths []string) (residueClassification, error) {
	return classifyResidueGo(ctx, root, paths, classifyRegularFile)
}

// The scanner parameter lets cancellation tests hold real worker lifetimes
// open without depending on filesystem speed. Each worker owns its buffer and
// result slots. There is no goroutine or open handle per inventory entry.
func classifyResidueGo(ctx context.Context, root string, paths []string, scan func(context.Context, string, []byte) fileClassification) (residueClassification, error) {
	if benchEnabled() {
		started := time.Now()
		defer func() { benchGoTextTotal.Add(int64(time.Since(started))); benchGoTextCalls.Add(1) }()
	}
	workers := min(len(paths), textScanMaxWorkers, max(1, runtime.GOMAXPROCS(0)))
	if len(paths) <= 2 {
		workers = min(1, len(paths))
	}
	finish := platform.InternalBenchSpan("search.classify.residue",
		"backend", "go", "paths", platform.InternalBenchInt(len(paths)),
		"workers", platform.InternalBenchInt(workers),
	)
	results := make([]fileClassification, len(paths))
	run := func(i int, buf []byte) {
		results[i] = scan(ctx, filepath.Join(root, filepath.FromSlash(paths[i])), buf)
	}
	if workers == 1 {
		buf := make([]byte, textScanBufferSize)
		for i := range paths {
			if ctx.Err() != nil {
				break
			}
			run(i, buf)
		}
	} else if workers > 1 {
		jobs := make(chan int, workers*2)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				buf := make([]byte, textScanBufferSize)
				for i := range jobs {
					if ctx.Err() != nil {
						return
					}
					run(i, buf)
				}
			}()
		}
	feed:
		for i := range paths {
			if ctx.Err() != nil {
				break
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()
	}
	if err := ctx.Err(); err != nil {
		finish("cancelled", "true")
		return residueClassification{}, err
	}
	out := residueClassification{
		text:     make(map[string]struct{}, len(paths)),
		binary:   make(map[string]struct{}),
		observed: make(map[string]os.FileInfo, len(paths)),
	}
	unconfirmed := 0
	for i, result := range results {
		rel := normalizeRelPath(paths[i])
		if rel == "" || rel == "." {
			continue
		}
		out.observed[rel] = result.info
		switch result.class {
		case contentText:
			out.text[rel] = struct{}{}
		case contentBinary:
			out.binary[rel] = struct{}{}
		default:
			unconfirmed++
		}
	}
	if err := ctx.Err(); err != nil {
		finish("cancelled", "true")
		return residueClassification{}, err
	}
	finish("cancelled", "false", "text", platform.InternalBenchInt(len(out.text)),
		"binary", platform.InternalBenchInt(len(out.binary)), "unconfirmed", platform.InternalBenchInt(unconfirmed),
		"stat_observations", platform.InternalBenchInt(len(out.observed)))
	return out, nil
}
