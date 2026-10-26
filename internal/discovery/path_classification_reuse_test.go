package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestOverlappingSelectionsReuseClassificationWithoutSharingVisibility(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":  "hidden/\n",
		"src/a.audit": "text", "src/b.audit": "binary\x00",
		"hidden/c.audit": "hidden text",
	})
	logPath := filepath.Join(t.TempDir(), "classification.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", logPath)
	broad := Resolver{Cfg: command.Invocation{WorkingDir: project}, NoIgnore: true}
	all, err := broad.discoverFilesUnderNoIgnore(".")
	if err != nil || len(all) != 3 {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	visible := Resolver{Cfg: command.Invocation{WorkingDir: project}}
	entries, err := visible.discoverVisibleFilesUnder("src")
	if err != nil || len(entries) != 1 || entries[0].RelPath != "src/a.audit" {
		t.Fatalf("visible=%+v err=%v", entries, err)
	}
	entries, err = visible.discoverGlobCandidateFiles("*.audit")
	if err != nil || len(entries) != 1 || entries[0].RelPath != "src/a.audit" {
		t.Fatalf("cached hidden/binary evidence leaked into visible glob: %+v %v", entries, err)
	}
	entries, err = broad.discoverGlobCandidateFiles("*.audit")
	if err != nil || len(entries) != 2 {
		t.Fatalf("no-ignore glob lost hidden text: %+v %v", entries, err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var scans, reused int
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, `event="search.classify.residue" `) {
			continue
		}
		if strings.Contains(line, ` scanned="3" `) {
			scans++
		} else if strings.Contains(line, ` scanned="0" `) {
			reused++
		} else {
			t.Fatalf("unexpected repeated classification: %s", line)
		}
	}
	if scans != 1 || reused != 3 {
		t.Fatalf("scan batches=%d reused batches=%d\n%s", scans, reused, data)
	}
}
