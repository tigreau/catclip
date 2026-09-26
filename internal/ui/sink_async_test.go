package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
)

// Synchronous oracle for preview parity and phase benchmarks; production
// routes ordinary preview rendering through the cancellable worker.
func renderSinkOutputTextPreview(plan output.Plan, emitCfg output.EmitConfig, limit int64) (sinkPreview, error) {
	return renderSinkPreviewWithModeContext(context.Background(), StartupSinkPickerContext{Plan: plan, Emit: emitCfg}, sinkPreviewModeOutputText, limit)
}

func TestSinkBundleDecisionCountsRawBytes(t *testing.T) {
	project := setupTestProject(t, map[string]string{"small.go": "package main\n" + strings.Repeat("var value = 123 // a comment\n", 65)})
	plan := testSinkOutputPlan(t, project, "small.go")
	m := measureOutputForSinkMenu(plan, output.EmitConfig{})
	if m.Err != nil {
		t.Fatal(m.Err)
	}
	if m.RawPreview == nil || len(highlightFileBlocksForSinkPreview(m.RawPreview.Body)) < output.BundleThreshold {
		t.Fatal("fixture must exceed threshold after coloring")
	}
	var raw bytes.Buffer
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(&raw, output.EmitConfig{}, plan); err != nil {
		t.Fatal(err)
	}
	if m.Bytes != int64(raw.Len()) || m.Bytes >= output.BundleThreshold || m.WouldBundle {
		t.Fatalf("ANSI changed bundle decision: raw=%d measured=%d bundle=%t", raw.Len(), m.Bytes, m.WouldBundle)
	}
}

func TestSinkBundleDecisionExactRawThreshold(t *testing.T) {
	for _, size := range []int{output.BundleThreshold - 1, output.BundleThreshold, output.BundleThreshold + 1, int(output.PreviewByteLimit) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			project := setupTestProject(t, map[string]string{"data.txt": strings.Repeat("x", size)})
			plan := testSinkOutputPlan(t, project, "data.txt")
			m := measureOutputForSinkMenu(plan, output.EmitConfig{Raw: true})
			if m.Err != nil {
				t.Fatal(m.Err)
			}
			if m.Bytes != min(int64(size), int64(output.BundleThreshold)) || m.WouldBundle != (size >= output.BundleThreshold) {
				t.Fatalf("size=%d measurement=%+v", size, m)
			}
		})
	}
}

func TestSinkMeasurementFailureDoesNotOpenPicker(t *testing.T) {
	want := errors.New("payload read failed")
	_, used, err := pickOutputSink(StartupSinkPickerContext{}, sinkPayloadMeasurement{Err: want})
	if !errors.Is(err, want) || used {
		t.Fatalf("used=%t err=%v", used, err)
	}
}

func TestSinkTreePreparationDoesNotBlockAndCleanupJoins(t *testing.T) {
	parent := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, parent)
	}
	started, finished := make(chan struct{}), make(chan struct{})
	render := func(ctx context.Context, _ StartupSinkPickerContext, mode sinkPreviewMode, _ int64) (sinkPreview, error) {
		if mode != sinkPreviewModeTreeReport {
			return sinkPreview{}, errors.New("measured output was re-rendered")
		}
		close(started)
		defer close(finished)
		select {
		case <-ctx.Done():
			return sinkPreview{}, ctx.Err()
		case <-time.After(5 * time.Second):
			return sinkPreview{}, errors.New("test renderer timed out")
		}
	}
	start := time.Now()
	files, err := prepareStartupSinkPreviewFilesWithRenderer(StartupSinkPickerContext{}, &sinkPreview{Body: []byte("ready output\n")}, render)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Cleanup()
	if time.Since(start) > 2*time.Second {
		t.Fatal("setup waited for tree rendering")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("tree worker did not start")
	}
	dirs, err := os.ReadDir(parent)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("artifact dirs=%v err=%v", dirs, err)
	}
	dir := filepath.Join(parent, dirs[0].Name())
	if _, err := os.Stat(filepath.Join(dir, "tree.txt.pending")); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := RunInternalSinkPreview(filepath.Join(dir, "mode"), filepath.Join(dir, "output.txt"), filepath.Join(dir, "tree.txt"), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.String(), "ready output") {
		t.Fatal("ready output not readable while tree is pending")
	}
	files.Cleanup()
	select {
	case <-finished:
	default:
		t.Fatal("cleanup did not join worker")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("artifacts survived cleanup: %v", err)
	}
}

func TestAsyncSinkPreviewPublicationAndErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "tree.txt")
			if err := os.WriteFile(target+".pending", nil, 0600); err != nil {
				t.Fatal(err)
			}
			render := func(context.Context, StartupSinkPickerContext, sinkPreviewMode, int64) (sinkPreview, error) {
				if fail {
					return sinkPreview{}, errors.New("test render failure")
				}
				return sinkPreview{Mode: sinkPreviewModeTreeReport, Body: []byte("complete tree\n")}, nil
			}
			writeAsyncSinkPreview(context.Background(), StartupSinkPickerContext{}, sinkPreviewModeTreeReport, target, render)
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			want := "complete tree"
			if fail {
				want = "Preview unavailable: test render failure"
			}
			if !strings.Contains(string(got), want) {
				t.Fatalf("artifact=%q", got)
			}
			for _, suffix := range []string{".pending", ".part"} {
				if _, err := os.Stat(target + suffix); !os.IsNotExist(err) {
					t.Fatalf("left %s: %v", suffix, err)
				}
			}
		})
	}
}

func TestAsyncSinkCancelledBeforeStartDoesNotRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	target := filepath.Join(dir, "tree.txt")
	if err := os.WriteFile(target+".pending", nil, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	writeAsyncSinkPreview(ctx, StartupSinkPickerContext{}, sinkPreviewModeTreeReport, target, func(context.Context, StartupSinkPickerContext, sinkPreviewMode, int64) (sinkPreview, error) {
		called = true
		return sinkPreview{}, nil
	})
	if called {
		t.Fatal("cancelled worker rendered")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("cancelled worker published: %v", err)
	}
	if _, err := os.Stat(target + ".pending"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestSinkMetadataAndTreeStillRenderAsynchronously(t *testing.T) {
	modes := make(chan sinkPreviewMode, 2)
	release := make(chan struct{})
	files, err := prepareStartupSinkPreviewFilesWithRenderer(StartupSinkPickerContext{Config: command.Parsed{PayloadKind: command.PayloadMetadata}}, nil,
		func(ctx context.Context, _ StartupSinkPickerContext, mode sinkPreviewMode, _ int64) (sinkPreview, error) {
			modes <- mode
			select {
			case <-release:
				return sinkPreview{Mode: mode}, nil
			case <-ctx.Done():
				return sinkPreview{}, ctx.Err()
			case <-time.After(5 * time.Second):
				return sinkPreview{}, errors.New("test renderer timed out")
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Cleanup()
	select {
	case mode := <-modes:
		if mode != sinkPreviewModeOutputText {
			t.Fatal("metadata output must render first")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metadata worker did not start")
	}
	close(release)
	select {
	case mode := <-modes:
		if mode != sinkPreviewModeTreeReport {
			t.Fatal("tree did not follow metadata")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tree worker did not start")
	}
}

func TestOrdinarySinkOutputIsAsyncAndCancellationJoins(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	files, err := prepareStartupSinkPreviewFilesWithRenderer(StartupSinkPickerContext{}, nil,
		func(ctx context.Context, _ StartupSinkPickerContext, mode sinkPreviewMode, _ int64) (sinkPreview, error) {
			if mode != sinkPreviewModeOutputText {
				t.Error("tree started after cancellation")
				return sinkPreview{}, nil
			}
			close(started)
			defer close(finished)
			<-ctx.Done()
			return sinkPreview{}, ctx.Err()
		})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Cleanup()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("output worker did not start")
	}
	files.Cleanup()
	select {
	case <-finished:
	default:
		t.Fatal("cleanup did not join output worker")
	}
}

func TestSinkMeasurementStopsAtThresholdAndDefersLaterErrors(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.txt": strings.Repeat("x", output.BundleThreshold), "b.txt": "later"})
	plan := output.BuildPlan([]output.PreparedFileUnit{
		{Entry: discovery.Entry{RelPath: "a.txt", AbsPath: filepath.Join(project, "a.txt"), Mode: command.EntryModeFull}, BodyBytes: output.BundleThreshold},
		{Entry: discovery.Entry{RelPath: "b.txt", AbsPath: filepath.Join(project, "b.txt"), Mode: command.EntryModeFull}, BodyBytes: 5},
	})
	if err := os.Remove(filepath.Join(project, "b.txt")); err != nil {
		t.Fatal(err)
	}
	for _, emit := range []output.EmitConfig{{}, {Raw: true}} {
		m := measureOutputForSinkMenu(plan, emit)
		if m.Err != nil || !m.WouldBundle || m.RawPreview != nil || m.Bytes != output.BundleThreshold {
			t.Fatalf("measurement read beyond decision or retained incomplete prefix: raw=%t %+v", emit.Raw, m)
		}
	}
	_, err := renderSinkPreviewWithModeContext(context.Background(), StartupSinkPickerContext{Plan: plan}, sinkPreviewModeOutputText, output.PreviewByteLimit)
	if err == nil {
		t.Fatal("later preview read error was lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderSinkPreviewWithModeContext(ctx, StartupSinkPickerContext{Plan: plan}, sinkPreviewModeOutputText, output.PreviewByteLimit); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled render: %v", err)
	}
}

func TestAsyncSinkFailedPublicationUnblocksReaderWithError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tree.txt")
	if err := os.WriteFile(target+".pending", nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Force a failed write without relying on platform-specific permission bits.
	if err := os.Mkdir(target+".part", 0700); err != nil {
		t.Fatal(err)
	}
	writeAsyncSinkPreview(context.Background(), StartupSinkPickerContext{}, sinkPreviewModeTreeReport, target,
		func(context.Context, StartupSinkPickerContext, sinkPreviewMode, int64) (sinkPreview, error) {
			return sinkPreview{Body: []byte("tree")}, nil
		})
	if err := waitForSinkPreviewArtifact(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(target); err == nil {
		t.Fatal("failed publication was reported as successful content")
	}
}

func TestAsyncSinkArtifactsPreserveOutputShapes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/keep.go":  "package main\n// KEEP\nfunc main() {}\n",
		"src/drop.txt": "other text\n",
		"src/data.bin": "binary\x00data",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/keep.go", "package main\n// KEEP changed\nfunc main() {}\n")
	t.Chdir(project)
	defer scopeViewMemoReset()
	for _, args := range [][]string{
		{"src", "--only", "*.go"},
		{"src", "--paths"},
		{"src", "--only", "*.go", "--lines", "1", "2"},
		{"src", "--only", "*.go", "--lines", "1", "2", "--raw"},
		{"src", "--snippet", "KEEP"},
		{"src", "--snippet", "KEEP", "0"},
		{"src", "--changed-diff"},
		{"src", "--lines", "1", "1", "--then", "src", "--paths"},
		{"src", "--with-binaries"},
		{"src", "--metadata"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			scopeViewMemoReset()
			ctx, err := buildStartupSinkPickerContext(args)
			if err != nil {
				t.Fatal(err)
			}
			m := measureStartupSinkPayload(ctx)
			if m.Err != nil {
				t.Fatal(m.Err)
			}
			parent := t.TempDir()
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, parent)
			}
			ctx.rawOutputPreview = m.RawPreview
			files, err := prepareStartupSinkPreviewFiles(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Cleanup()
			dirs, err := os.ReadDir(parent)
			if err != nil || len(dirs) != 1 {
				t.Fatalf("artifact dirs=%v err=%v", dirs, err)
			}
			dir := filepath.Join(parent, dirs[0].Name())
			canonical := ctx
			canonical.Render = ctx.Render.WithPreparedPresentation(nil)
			canonical.rawOutputPreview = nil
			for _, mode := range []sinkPreviewMode{sinkPreviewModeOutputText, sinkPreviewModeTreeReport} {
				want, err := renderSinkPreviewWithModeContext(context.Background(), canonical, mode, output.PreviewByteLimit)
				if err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				if err := RunInternalSinkPreview(filepath.Join(dir, "mode"), filepath.Join(dir, "output.txt"), filepath.Join(dir, "tree.txt"), &got); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Bytes(), formatSinkPreview(want)) {
					t.Fatalf("async artifact differs for mode %v", mode)
				}
				if err := RunInternalSinkToggle(filepath.Join(dir, "mode"), &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
