package search

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestContentPathChunksBudgetEntireCommand(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		for _, patternSize := range []int{1, 16000} {
			fixed := []string{"--pcre2", "-e", strings.Repeat("a", patternSize), "--"}
			var paths []string
			for i := 0; i < 1025; i++ {
				paths = append(paths, fmt.Sprintf(`C:\project with spaces\%04d\%s.go`, i, strings.Repeat("é雪", 40)))
			}
			chunks, err := contentPathChunks("rg executable", fixed, paths, execPathChunkMaxCount, goos)
			if err != nil {
				t.Fatal(err)
			}
			for _, chunk := range chunks {
				args := append(append([]string(nil), fixed...), chunk...)
				if len(chunk) == 0 || len(chunk) > execPathChunkMaxCount || commandArgBudgetUnits("rg executable", args, goos) > execPathChunkByteLimit(goos) {
					t.Fatalf("invalid %s batch with %d-byte pattern", goos, patternSize)
				}
			}
			if !reflect.DeepEqual(flattenExecPathChunks(chunks), paths) {
				t.Fatal("paths lost or reordered")
			}
		}
		fixed := []string{"-e", strings.Repeat("x", execPathChunkByteLimit(goos)), "--"}
		if chunks, err := contentPathChunks("rg", fixed, []string{"a"}, 256, goos); err == nil || chunks != nil {
			t.Fatal("oversized fixed operand accepted")
		}
		if chunks, err := contentPathChunks("rg", []string{"--"}, []string{"a", strings.Repeat("x", execPathChunkByteLimit(goos))}, 256, goos); err == nil || chunks != nil {
			t.Fatal("oversized later path returned partial batches")
		}
	}
}

func TestContentRunnersRejectOversizedFixedOperands(t *testing.T) {
	pattern := strings.Repeat("a", currentExecPathChunkByteLimit())
	paths := []string{filepath.Join(t.TempDir(), "not-opened.go")}
	for name, run := range map[string]func() error{
		"matches":  func() error { _, err := RunRipgrepMatches(pattern, paths); return err },
		"negative": func() error { _, err := RunRipgrepMatches(pattern, paths, true); return err },
		"lines":    func() error { _, err := RunRipgrepMatchLines(pattern, paths); return err },
		"first":    func() error { _, err := FirstMatchLinePerFile(pattern, paths); return err },
		"direct":   func() error { _, err := RunRipgrepDirect(filepath.Dir(paths[0]), ".", pattern, ""); return err },
		"direct_lines": func() error {
			_, err := RunRipgrepDirectMatchLines(filepath.Dir(paths[0]), ".", pattern, "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil || !strings.Contains(err.Error(), "command budget") || !strings.Contains(err.Error(), runtime.GOOS) {
				t.Fatalf("expected a pre-spawn budget error, got %v", err)
			}
		})
	}
}

// Real rg on all CI OSes, with a large pattern and short valid filenames.
// Every file must survive multiple content batches without a membership walk.
func TestContentLargePatternNativeParity(t *testing.T) {
	root := t.TempDir()
	pattern := strings.Repeat("a", 16000)
	var paths []string
	for i := 0; i < 270; i++ {
		path := filepath.Join(root, fmt.Sprintf("file-%04d-%s.unknown", i, strings.Repeat("x", 90)))
		if err := os.WriteFile(path, []byte(pattern+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	var events []MembershipEnumerationEvent
	restore := SetMembershipEnumerationObserver(func(e MembershipEnumerationEvent) { events = append(events, e) })
	defer restore()
	hits, err := RunRipgrepMatches(pattern, paths)
	if err != nil || len(hits) != len(paths) {
		t.Fatalf("contains: %d hits, %v", len(hits), err)
	}
	negative, err := RunRipgrepMatches(pattern+"z", paths, true)
	if err != nil || len(negative) != len(paths) {
		t.Fatalf("not-contains: %d hits, %v", len(negative), err)
	}
	lines, err := RunRipgrepMatchLines(pattern, paths)
	if err != nil || len(lines) != len(paths) {
		t.Fatalf("snippet lines: %d hits, %v", len(lines), err)
	}
	first, err := FirstMatchLinePerFile(pattern, paths)
	if err != nil || len(first) != len(paths) {
		t.Fatalf("first lines: %d hits, %v", len(first), err)
	}
	for _, path := range paths {
		if _, ok := hits[path]; !ok {
			t.Fatalf("lost %s", path)
		}
		if _, ok := negative[path]; !ok {
			t.Fatalf("lost negative %s", path)
		}
		if !reflect.DeepEqual(lines[path], []int{1}) || first[path] != 1 {
			t.Fatalf("line data lost for %s", path)
		}
	}
	if len(events) != 0 {
		t.Fatalf("content scan enumerated membership: %+v", events)
	}
}
