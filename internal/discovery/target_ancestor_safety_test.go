package discovery

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

// Frozen pre-shortcut oracle, independent of the optimized production loop.
func legacyKnownAncestorIndex(r *Resolver) error {
	if r.visibleDirsReady {
		return nil
	}
	if err := r.BuildVisibleFileList(); err != nil {
		return err
	}

	dirSet := make(map[string]struct{}, len(r.VisibleFileList))
	for _, entry := range r.VisibleFileList {
		dir := path.Dir(entry.RelPath)
		for dir != "." && dir != "" {
			dirSet[dir] = struct{}{}
			dir = path.Dir(dir)
		}
	}

	dirs := make([]string, 0, len(dirSet))
	for rel := range dirSet {
		dirs = append(dirs, rel)
	}
	sort.Strings(dirs)

	r.VisibleDirs = VisibleDirIndex{
		Dirs:        dirs,
		Set:         make(map[string]struct{}, len(dirs)),
		SymlinkDirs: nil,
	}
	for _, rel := range dirs {
		r.VisibleDirs.Set[rel] = struct{}{}
	}
	r.visibleDirsReady = true
	return nil
}

func checkKnownAncestorSafety(t *testing.T, entries []Entry) {
	t.Helper()
	original := slices.Clone(entries)
	baseline := &Resolver{VisibleFileList: entries, visibleFileListReady: true}
	if err := legacyKnownAncestorIndex(baseline); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{VisibleFileList: entries, visibleFileListReady: true}
	if err := r.BuildVisibleDirIndex(); err != nil {
		t.Fatalf("production: %v", err)
	}
	if !r.visibleDirsReady || !reflect.DeepEqual(r.VisibleDirs, baseline.VisibleDirs) {
		t.Fatal("production changed directory index")
	}
	if !reflect.DeepEqual(entries, original) {
		t.Fatal("production mutated entries")
	}
	// End-to-end target rows and their path lookup map must remain identical.
	want, err := baseline.allVisibleTargets()
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.allVisibleTargets()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("production changed picker targets")
	}
	wantLabels, wantIndex := TargetMatchLabels(want)
	gotLabels, gotIndex := TargetMatchLabels(got)
	if !reflect.DeepEqual(gotLabels, wantLabels) || !reflect.DeepEqual(gotIndex, wantIndex) {
		t.Fatal("production changed row rendering or selection index")
	}
	// A completed index is reused as-is, even if its source slice disappears.
	// Readiness must prevent a new file scan or resetting cached fields.
	r.VisibleFileList = nil
	r.visibleFileListReady = false
	if err := r.BuildVisibleDirIndex(); err != nil {
		t.Fatalf("production reused index attempted rebuild: %v", err)
	}
	if !reflect.DeepEqual(r.VisibleDirs, baseline.VisibleDirs) {
		t.Fatal("production changed cached index")
	}
	if len(r.VisibleDirs.Dirs) > 0 {
		key := r.VisibleDirs.Dirs[0]
		delete(r.VisibleDirs.Set, key)
		r.VisibleDirs.Dirs[0] = "mutated"
		if _, ok := baseline.VisibleDirs.Set[key]; !ok || baseline.VisibleDirs.Dirs[0] == "mutated" {
			t.Fatal("production shares mutable directory output with another resolver")
		}
	}
}

func TestKnownAncestorGeneratedSafety(t *testing.T) {
	rng := rand.New(rand.NewSource(760912))
	parts := []string{"src", "src-old", "a", "ab", "A", "a.b", "space here", "é", "東京", ".hidden", "[x]", "star*", "line\nbreak", "tab\tname", "back\\slash"}
	for round := 0; round < 200; round++ {
		n := rng.Intn(400)
		entries := make([]Entry, 0, n+4)
		for i := 0; i < n; i++ {
			segments := make([]string, 1+rng.Intn(8))
			for j := range segments {
				segments[j] = parts[rng.Intn(len(parts))]
			}
			rel := strings.Join(segments, "/") + fmt.Sprintf("/file%d.go", i%13)
			entries = append(entries, Entry{RelPath: rel, SizeBytes: int64(i), SizeKnown: i%2 == 0, GitVisible: true})
			if i%17 == 0 {
				entries = append(entries, entries[len(entries)-1])
			}
		}
		for order := 0; order < 3; order++ {
			if order == 1 {
				slices.Reverse(entries)
			}
			if order == 2 {
				rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
			}
			checkKnownAncestorSafety(t, entries)
		}
	}
}

func TestKnownAncestorCorpusSafety(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus safety")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		root = "/Users/chris/Desktop/catclip-test-data"
	}
	for _, binary := range []bool{false, true} {
		r := &Resolver{Cfg: command.Invocation{WorkingDir: root}, WithBinaries: binary}
		if err := r.BuildVisibleFileList(); err != nil {
			t.Fatal(err)
		}
		for _, reverse := range []bool{false, true} {
			entries := slices.Clone(r.VisibleFileList)
			if reverse {
				slices.Reverse(entries)
			}
			checkKnownAncestorSafety(t, entries)
		}
		t.Logf("with_binaries=%t full_entries=%d ordered_and_reversed_parity=true", binary, len(r.VisibleFileList))
	}
}
