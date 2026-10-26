package discovery

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func TestTargetGlobNarrowingPreservesClassifiedMembership(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":            "hidden/\n",
		"README.md":             "readme\n",
		"src/a.probe":           "text\n",
		"src/b.probe":           "binary\x00tail",
		"src/nested/c.probe":    "nested\n",
		"src/empty.png":         "",
		"src/image.png":         "\x89PNG\x00data",
		"hidden/a.probe":        "ignored text\n",
		"local/a.probe":         "hiss text\n",
		"other/unrelated.probe": "other\n",
	})
	hissPath := filepath.Join(project, "config", "catclip", ".hiss")
	if err := os.MkdirAll(filepath.Dir(hissPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hissPath, []byte("local/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, noIgnore := range []bool{false, true} {
		for _, binaries := range []bool{false, true} {
			r := Resolver{Cfg: command.Invocation{WorkingDir: project}, NoIgnore: noIgnore, WithBinaries: binaries}
			baseline := r
			var all []Entry
			var err error
			if noIgnore {
				all, err = baseline.discoverFilesUnderNoIgnore(".")
			} else {
				all, err = baseline.discoverVisibleFilesUnder(".")
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, pattern := range []string{"*.probe", "src/*.probe", "src/[ab].probe", "*a.probe", "*.png", "hidden/*", "local/*", "README.*", "missing*"} {
				var want []Entry
				for _, entry := range all {
					baseMatch, _ := path.Match(pattern, path.Base(entry.RelPath))
					pathMatch, _ := path.Match(pattern, entry.RelPath)
					if baseMatch || pathMatch {
						want = append(want, entry)
					}
				}
				got, err := r.discoverGlobCandidateFiles(pattern)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
					t.Fatalf("noIgnore=%v binaries=%v pattern=%q\ngot  %+v\nwant %+v", noIgnore, binaries, pattern, got, want)
				}
			}
		}
	}
}

func TestTargetGlobClassifiesOnlyMatchingPaths(t *testing.T) {
	for _, noIgnore := range []bool{false, true} {
		t.Run(platform.InternalBenchBool(noIgnore), func(t *testing.T) {
			project := writeNoIgnoreTargetFixture(t, map[string]string{
				"keep/a.probe": "text\n", "keep/b.probe": "binary\x00",
				"elsewhere/other.probe": "unrelated\n",
				"elsewhere/other.md":    "unrelated known text\n",
			})
			logPath := filepath.Join(t.TempDir(), "scan.log")
			t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", logPath)
			r := Resolver{Cfg: command.Invocation{WorkingDir: project}, NoIgnore: noIgnore}
			got, err := r.discoverGlobCandidateFiles("keep/*.probe")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].RelPath != "keep/a.probe" {
				t.Fatalf("entries = %+v", got)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, line := range strings.Split(string(log), "\n") {
				if strings.Contains(line, `event="search.classify.paths" `) {
					if found || !strings.Contains(line, ` paths="2" `) || !strings.Contains(line, ` residue_scan_count="2" `) {
						t.Fatalf("unexpected classification work: %s", log)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("missing classification measurement: %s", log)
			}
		})
	}
}

func TestTargetGlobDoesNotPublishIncompleteTextSet(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		"one/a.probe": "first\n", "two/b.probe": "second\n",
	})
	r := Resolver{Cfg: command.Invocation{WorkingDir: project}, NoIgnore: true}
	if _, err := r.discoverGlobCandidateFiles("one/*"); err != nil {
		t.Fatal(err)
	}
	if r.textFileSetReady {
		t.Fatal("glob subset published as complete classification")
	}
	text, err := r.classifyTextFile("two/b.probe", filepath.Join(project, "two", "b.probe"))
	if err != nil || !text {
		t.Fatalf("later target excluded: text=%v err=%v", text, err)
	}
	beforeText, beforeBinary := r.textFileSet, r.binaryFileSet
	if _, err := r.discoverGlobCandidateFiles("one/*"); err != nil {
		t.Fatal(err)
	}
	if !r.textFileSetReady || !reflect.DeepEqual(r.textFileSet, beforeText) || !reflect.DeepEqual(r.binaryFileSet, beforeBinary) {
		t.Fatal("glob replaced the existing complete classification")
	}
}

func TestTargetGlobInvalidSyntaxDoesNotEnumerate(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, nil)
	var events []search.MembershipEnumerationEvent
	restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })
	defer restore()
	r := Resolver{Cfg: command.Invocation{WorkingDir: project}, NoIgnore: true}
	_, _, _, _, err := r.resolveGlobTarget(0, "broken[", platform.Palette{})
	if err == nil || !strings.Contains(err.Error(), "Invalid glob pattern") {
		t.Fatalf("invalid glob error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("enumerated for malformed glob: %+v", events)
	}
}
