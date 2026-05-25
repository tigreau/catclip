package catclip

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/tree"
)

// Phase 0 of docs/versions/v0.5.5/reports/ACTIVE_PLAN_preview_refresh_cost.md:
// break the per-refresh preview cost into its stages so the fix (compact
// checkpoint / progressive render / cap / pipe collapse) is chosen with data.
//
// These benchmarks isolate the per-stage CPU (no process spawn); the ~26 ms
// spawn floor per process comes from the hyperfine end-to-end numbers in the
// plan. Synthetic entries are SizeKnown=true so nothing stats; an empty scope
// means applyScopeStages runs no stages (no rg) — we are timing the structural
// decode/plan/encode/render cost that scales with entry count.

func benchPreviewEntries(n int) []fileEntry {
	es := make([]fileEntry, n)
	for i := range n {
		es[i] = fileEntry{
			RelPath:    fmt.Sprintf("src/pkg%02d/sub%02d/file%05d.go", i%50, (i/50)%20, i),
			AbsPath:    fmt.Sprintf("/repo/src/pkg%02d/sub%02d/file%05d.go", i%50, (i/50)%20, i),
			SizeBytes:  int64(800 + i),
			SizeKnown:  true,
			GitVisible: true,
			Mode:       entryModeFull,
		}
	}
	return es
}

func benchPreviewCheckpointRaw(tb testing.TB, n int) []byte {
	tb.Helper()
	raw, err := marshalPrediscoveredCheckpoint(prediscoveredCheckpointData{
		GitContext: gitContext{},
		GitStatus:  map[string]string{},
		Entries:    benchPreviewEntries(n),
	})
	if err != nil {
		tb.Fatalf("marshal checkpoint: %v", err)
	}
	return raw
}

func benchPreviewPlan(tb testing.TB, entries []fileEntry) outputPlan {
	tb.Helper()
	cfg := invocationConfig{WorkingDir: "/repo"}
	var scope executionScope
	tail, err := applyPrediscoveredScopeTail(cfg, gitContext{}, scope, entries)
	if err != nil {
		tb.Fatalf("scope tail: %v", err)
	}
	plan, err := buildOutputPlanForResolvedScopes(gitContext{}, []executionScope{scope},
		[]evaluatedOutputScope{{Paths: scope.Paths, Entries: tail}}, tail)
	if err != nil {
		tb.Fatalf("plan build: %v", err)
	}
	return plan
}

func benchPreviewPayload(tb testing.TB, plan outputPlan) []byte {
	tb.Helper()
	var buf bytes.Buffer
	if err := encodeTreePayloadFromPlan(&buf, renderConfig{TreePayload: true}, gitContext{}, plan, nil); err != nil {
		tb.Fatalf("encode payload: %v", err)
	}
	return buf.Bytes()
}

func benchPrediscoveredContentProject(tb testing.TB, n int) (string, string) {
	tb.Helper()

	project := tb.TempDir()
	entries := make([]fileEntry, 0, n)
	for i := range n {
		rel := fmt.Sprintf("src/pkg%02d/file%05d.go", i%25, i)
		content := fmt.Sprintf("package pkg%02d\n\nfunc File%d() {}\n\n// TODO benchmark marker %d\n", i%25, i, i)
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			tb.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			tb.Fatalf("write %s: %v", rel, err)
		}
		entries = append(entries, fileEntry{
			AbsPath:    abs,
			RelPath:    rel,
			SizeBytes:  int64(len(content)),
			SizeKnown:  true,
			GitVisible: true,
			Mode:       entryModeFull,
		})
	}

	checkpointPath := filepath.Join(project, "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, project, prediscoveredCheckpointData{
		GitContext: gitContext{},
		GitStatus:  map[string]string{},
		Entries:    entries,
	}); err != nil {
		tb.Fatalf("write checkpoint: %v", err)
	}
	return project, checkpointPath
}

func benchParseCommand(tb testing.TB, project string, args []string) parsedCommand {
	tb.Helper()
	cfg, err := parseArgs(args)
	if err != nil {
		tb.Fatalf("parseArgs(%v): %v", args, err)
	}
	cfg.WorkingDir = project
	return cfg
}

// BenchmarkEnsureEntryAbsPaths quantifies the marginal cost the rel-only
// checkpoint (dropped serialized AbsPath) adds to EVERY checkpoint reader —
// tree and content/slice alike — since they all run ensureEntryAbsPaths in
// applyPrediscoveredScopeTail. "rel_only" derives AbsPath (N filepath.Join);
// "abs_set" is the pre-drop case (skips). The delta is the only thing the
// abs-drop changes for --contains/--snippet/--lines; their rg/read work is
// unchanged. (Owed measurement from the preview-refresh-cost plan's
// content/slice priority rule.)
func BenchmarkEnsureEntryAbsPaths(b *testing.B) {
	relOnly := benchPreviewEntries(5000)
	for i := range relOnly {
		relOnly[i].AbsPath = ""
	}
	absSet := benchPreviewEntries(5000) // AbsPath already populated

	b.Run("rel_only_derives", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			es := append([]fileEntry(nil), relOnly...)
			_ = ensureEntryAbsPaths(es, "/repo")
		}
	})
	b.Run("abs_set_skips", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			es := append([]fileEntry(nil), absSet...)
			_ = ensureEntryAbsPaths(es, "/repo")
		}
	})
}

// BenchmarkPrediscoveredContentSliceHandlers pins the priority rule from the
// preview-refresh-cost plan: content/slice preview paths (--contains,
// --snippet, --lines) must stay neutral-or-better when checkpoint formats are
// tuned for tree preview speed. Unlike BenchmarkEnsureEntryAbsPaths, this goes
// through the actual checkpointed handlers. It intentionally includes rg/file
// reads where those handlers do; run explicitly when changing the checkpoint
// format:
//
//	go test -run=^$ -bench=BenchmarkPrediscoveredContentSliceHandlers -benchmem ./
func BenchmarkPrediscoveredContentSliceHandlers(b *testing.B) {
	project, checkpointPath := benchPrediscoveredContentProject(b, 1000)

	containsCfg := prediscoveredCommandConfigFromParsedCommand(benchParseCommand(b, project, []string{
		"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--contains", "TODO",
	}))
	snippetCfg := prediscoveredCommandConfigFromParsedCommand(benchParseCommand(b, project, []string{
		"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--snippet", "TODO",
	}))
	linesParsed := benchParseCommand(b, project, []string{
		"--quiet", "--internal-lines-preview", "--internal-prediscovered", checkpointPath, "--lines", "1", "4",
	})
	linesCfg := prediscoveredCommandConfigFromParsedCommand(linesParsed)
	linesEmitCfg := emitConfigFromParsedCommand(linesParsed)

	b.Run("contains_match_list_1k", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := runInternalPrediscoveredContentMatchList(containsCfg, io.Discard); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("snippet_match_list_1k", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := runInternalPrediscoveredContentMatchList(snippetCfg, io.Discard); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lines_preview_1k", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := runInternalLinesPreview(linesCfg, linesEmitCfg, io.Discard); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type firstByteRecorder struct {
	start time.Time
	seen  bool
	first time.Duration
	bytes int64
}

func (w *firstByteRecorder) Write(p []byte) (int, error) {
	if len(p) > 0 && !w.seen {
		w.seen = true
		w.first = time.Since(w.start)
	}
	w.bytes += int64(len(p))
	return len(p), nil
}

// BenchmarkPrediscoveredTreePreviewFirstByte is the Phase 2 gate for the
// preview-refresh-cost plan: total refresh time is not enough. Progressive
// rendering only matters if it moves time-to-first-byte before the old
// decode/plan/render gate. This benchmark records the current direct
// checkpoint tree preview baseline.
func BenchmarkPrediscoveredTreePreviewFirstByte(b *testing.B) {
	for _, n := range []int{1000, 5000} {
		raw := benchPreviewCheckpointRaw(b, n)
		checkpointPath := filepath.Join(b.TempDir(), fmt.Sprintf("scope-%d.json", n))
		if err := os.WriteFile(checkpointPath, raw, 0o600); err != nil {
			b.Fatalf("write checkpoint: %v", err)
		}
		parsed := benchParseCommand(b, "/repo", []string{
			"--quiet", "--internal-tree-preview", "--internal-prediscovered", checkpointPath,
		})
		cfg := prediscoveredCommandConfigFromParsedCommand(parsed)

		b.Run(fmt.Sprintf("n=%d/direct_tree_preview", n), func(b *testing.B) {
			b.ReportAllocs()
			var firstTotal time.Duration
			var totalTotal time.Duration
			var outputBytes int64
			for range b.N {
				w := firstByteRecorder{start: time.Now()}
				if err := runInternalPrediscoveredTreePreview(cfg, &w); err != nil {
					b.Fatal(err)
				}
				total := time.Since(w.start)
				if !w.seen {
					b.Fatal("preview wrote no bytes")
				}
				firstTotal += w.first
				totalTotal += total
				outputBytes += w.bytes
			}
			b.ReportMetric(float64(firstTotal.Nanoseconds())/float64(b.N)/1e6, "first_ms/op")
			b.ReportMetric(float64(totalTotal.Nanoseconds())/float64(b.N)/1e6, "total_ms/op")
			b.ReportMetric(float64(outputBytes)/float64(b.N), "output_B/op")
		})
	}
}

// BenchmarkPreviewRefreshStages reports per-stage ns/op for the per-refresh
// preview pipeline at representative scope sizes. Run:
//
//	go test -run=^$ -bench=BenchmarkPreviewRefreshStages -benchmem ./
func BenchmarkPreviewRefreshStages(b *testing.B) {
	for _, n := range []int{1000, 5000} {
		raw := benchPreviewCheckpointRaw(b, n)
		entries := benchPreviewEntries(n)
		plan := benchPreviewPlan(b, entries)
		payload := benchPreviewPayload(b, plan)
		opts := tree.DefaultRenderOptions()
		pal := tree.Palette{}

		b.Run(fmt.Sprintf("n=%d/stage1_checkpoint_decode", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for i := 0; i < b.N; i++ {
				if _, err := unmarshalPrediscoveredCheckpoint(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("n=%d/stage2_plan_build", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = benchPreviewPlan(b, entries)
			}
		})
		b.Run(fmt.Sprintf("n=%d/stage3_payload_encode", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = benchPreviewPayload(b, plan)
			}
		})
		b.Run(fmt.Sprintf("n=%d/stage4_render_decode_draw", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for i := 0; i < b.N; i++ {
				doc, err := tree.DecodePayload(bytes.NewReader(payload))
				if err != nil {
					b.Fatal(err)
				}
				if err := tree.RenderDocument(io.Discard, doc, opts, pal); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
