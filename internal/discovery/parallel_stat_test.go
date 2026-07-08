package discovery

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParallelStatReturnsPerIndexResults(t *testing.T) {
	dir := t.TempDir()
	// Two real files with distinct sizes plus one missing path.
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("aaa"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("bbbbbbb"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	paths := []string{a, filepath.Join(dir, "missing"), b}

	infos, errs := parallelStat(paths, os.Stat)

	if len(infos) != len(paths) || len(errs) != len(paths) {
		t.Fatalf("result-slice length mismatch: infos=%d errs=%d want=%d", len(infos), len(errs), len(paths))
	}
	if errs[0] != nil {
		t.Fatalf("index 0 unexpected error: %v", errs[0])
	}
	if infos[0].Size() != 3 {
		t.Fatalf("index 0 size = %d, want 3", infos[0].Size())
	}
	if errs[1] == nil {
		t.Fatal("index 1 expected error for missing path, got nil")
	}
	if infos[2].Size() != 7 {
		t.Fatalf("index 2 size = %d, want 7", infos[2].Size())
	}
}

func TestParallelStatSkipsEmptyPaths(t *testing.T) {
	// Empty string in a slot means "caller decided this index doesn't
	// need statting" — the sequential loops use empty AbsPath as the
	// skip signal. parallelStat mirrors that: (nil, nil) at that index.
	paths := []string{"", ""}
	infos, errs := parallelStat(paths, os.Stat)
	for i := range paths {
		if infos[i] != nil {
			t.Errorf("index %d expected nil info, got %v", i, infos[i])
		}
		if errs[i] != nil {
			t.Errorf("index %d expected nil error, got %v", i, errs[i])
		}
	}
}

func TestParallelStatEmptyInputReturnsEmptySlices(t *testing.T) {
	infos, errs := parallelStat(nil, os.Stat)
	if infos == nil || errs == nil {
		t.Fatal("nil input should still return non-nil zero-length slices for caller-side range safety")
	}
	if len(infos) != 0 || len(errs) != 0 {
		t.Fatalf("expected empty slices, got infos=%d errs=%d", len(infos), len(errs))
	}
}

func TestParallelStatHonorsStatFuncChoice(t *testing.T) {
	if os.Getenv("SKIP_SYMLINK_TESTS") != "" {
		t.Skip("symlink tests disabled by env")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink on this filesystem: %v", err)
	}

	// Stat follows the symlink → size of target (3 bytes).
	infosStat, errsStat := parallelStat([]string{link}, os.Stat)
	if errsStat[0] != nil {
		t.Fatalf("Stat unexpected error: %v", errsStat[0])
	}
	if infosStat[0].Size() != 3 {
		t.Fatalf("Stat size = %d, want 3 (should follow symlink)", infosStat[0].Size())
	}

	// Lstat does NOT follow → symlink's own metadata (size = length of target path or platform-dependent).
	infosLstat, errsLstat := parallelStat([]string{link}, os.Lstat)
	if errsLstat[0] != nil {
		t.Fatalf("Lstat unexpected error: %v", errsLstat[0])
	}
	if infosLstat[0].Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Lstat should have preserved symlink mode; got %v", infosLstat[0].Mode())
	}
}

func TestStatWorkerCountDefaults(t *testing.T) {
	t.Setenv("CATCLIP_STAT_WORKERS", "")
	got := statWorkerCount()
	if got < 1 || got > 8 {
		t.Fatalf("default worker count out of expected [1,8] range: %d", got)
	}
}

func TestStatWorkerCountRespectsEnvOverride(t *testing.T) {
	t.Setenv("CATCLIP_STAT_WORKERS", "16")
	if got := statWorkerCount(); got != 16 {
		t.Fatalf("env override 16 got %d", got)
	}
	t.Setenv("CATCLIP_STAT_WORKERS", "1")
	if got := statWorkerCount(); got != 1 {
		t.Fatalf("env override 1 got %d", got)
	}
}

func TestStatWorkerCountRejectsInvalidValues(t *testing.T) {
	// Invalid values must fall through to the default, NOT disable
	// parallelism (0 workers would deadlock the fan-out).
	for _, bad := range []string{"0", "-4", "not-a-number", " "} {
		t.Setenv("CATCLIP_STAT_WORKERS", bad)
		got := statWorkerCount()
		if got < 1 {
			t.Fatalf("invalid value %q produced worker count %d — must fall back to positive default", bad, got)
		}
	}
}

// End-to-end regression: exercise the full fan-out with more paths
// than workers to catch channel-close / ordering bugs. Uses a
// deterministic ordered-write pattern so we can assert each index
// received the right result even under parallel scheduling.
func TestParallelStatPreservesIndexOrderingUnderFanOut(t *testing.T) {
	dir := t.TempDir()
	const n = 100
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, strconv.Itoa(i))
		if err := os.WriteFile(p, make([]byte, i), 0o644); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		paths[i] = p
	}
	infos, errs := parallelStat(paths, os.Stat)
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("index %d error: %v", i, errs[i])
		}
		if got := int(infos[i].Size()); got != i {
			t.Fatalf("index %d size = %d, want %d — indices got shuffled", i, got, i)
		}
	}
}
