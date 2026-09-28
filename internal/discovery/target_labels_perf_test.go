package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

func legacyTargetLabels(matches []TargetMatch) ([]string, map[string]TargetMatch) {
	labels := make([]string, 0, len(matches))
	index := make(map[string]TargetMatch, len(matches))
	for _, match := range matches {
		label := fmt.Sprintf("[%s]", match.Kind)
		if match.Kind == "all" {
			label = "\x1b[1m[select all files]\x1b[0m"
		} else if match.Ignored {
			if source := strings.TrimSpace(match.IgnoreSource); source != "" {
				label = fmt.Sprintf("[%s %s]", match.Kind, source)
			}
		}
		labels = append(labels, strings.Join([]string{label, match.Path, TargetMatchPreviewKind(match), TargetMatchPreviewState(match)}, "\t"))
		index[match.Path] = match
	}
	return labels, index
}

func TestTargetLabelsAllocationParity(t *testing.T) {
	matches := []TargetMatch{{Path: ".", Kind: "all"}, {Path: "src", Kind: "dir"}, {Path: "src/a.go", Kind: "file", State: "text"}, {Path: "ignored", Kind: "dir", Ignored: true, IgnoreSource: " .gitignore "}, {Path: "x", Kind: "other", Ignored: true}}
	wl, wi := legacyTargetLabels(matches)
	gl, gi := TargetMatchLabels(matches)
	if !reflect.DeepEqual(wl, gl) || !reflect.DeepEqual(wi, gi) {
		t.Fatal("row fields or selection index changed")
	}
}

func TestTargetLabelsCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "Desktop", "catclip-test-data")
	r := &Resolver{Cfg: command.Invocation{WorkingDir: root}, GitCtx: git.Detect(root)}
	matches, err := r.allVisibleTargets()
	if err != nil {
		t.Fatal(err)
	}
	matches = append([]TargetMatch{{Path: ".", Kind: "all"}}, matches...)
	wl, wi := legacyTargetLabels(matches)
	gl, gi := TargetMatchLabels(matches)
	if !reflect.DeepEqual(wl, gl) || !reflect.DeepEqual(wi, gi) {
		t.Fatal("full-corpus rows changed")
	}
	t.Logf("rows=%d bytes=%d", len(gl), len(strings.Join(gl, "\n"))+1)
	for round := 0; round < 5; round++ {
		for offset := 0; offset < 2; offset++ {
			variant := (round + offset) % 2
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			var labels []string
			if variant == 0 {
				labels, _ = legacyTargetLabels(matches)
			} else {
				labels, _ = TargetMatchLabels(matches)
			}
			duration := time.Since(start)
			runtime.ReadMemStats(&after)
			t.Logf("round=%d variant=%d rows=%d ms=%.3f alloc_MiB=%.3f", round, variant, len(labels), float64(duration)/float64(time.Millisecond), float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
		}
	}
}
