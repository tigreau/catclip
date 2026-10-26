package ui

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/search"
)

func TestMetadataIgnoredDirectoriesSurviveDisplayCap(t *testing.T) {
	root := setupTestProject(t, map[string]string{"pkg/node_modules/lib/index.js": "ignored\n"})
	records := make([]search.IgnoreTraceRecord, 0, 31)
	for i := 0; i < 30; i++ {
		records = append(records, search.IgnoreTraceRecord{Path: fmt.Sprintf("a%02d.tmp", i), Source: ".gitignore", Pattern: "*.tmp"})
	}
	records = append(records, search.IgnoreTraceRecord{Path: "pkg/node_modules", Source: ".gitignore", Pattern: "node_modules/", RuleDirectoryOnly: true})
	var expected []MetadataIgnoredPath
	rng := rand.New(rand.NewSource(7))
	for iteration := 0; iteration < 20; iteration++ {
		rng.Shuffle(len(records), func(i, j int) { records[i], records[j] = records[j], records[i] })
		collector := metadataIgnoredCollector{workingDir: root}
		for _, record := range records {
			collector.add(record)
		}
		summary := collector.summary()
		if len(summary.Rows) != metadataIgnoredDisplayLimit || summary.Total != len(records) {
			t.Fatalf("lost count/cap: %+v", summary)
		}
		if summary.Rows[0].Path != "pkg/node_modules" || summary.Rows[0].Kind != "directory" {
			t.Fatalf("directory lost behind files: %+v", summary.Rows)
		}
		if iteration == 0 {
			expected = summary.Rows
		} else if !reflect.DeepEqual(summary.Rows, expected) {
			t.Fatal("stream arrival order changed display")
		}
	}
}

func TestMetadataIgnoredKnownParentCollapsesOnlyItsDescendants(t *testing.T) {
	root := setupTestProject(t, map[string]string{
		"cache/deep/a.tmp": "ignored", "cache2/b.tmp": "ignored", "src/one.tmp": "ignored", "src/two.tmp": "ignored", "src/visible.txt": "visible",
	})
	collector := metadataIgnoredCollector{workingDir: root}
	// A rule without a trailing slash: the existing bounded stat identifies
	// cache as a directory. No parent ignore decision exists for src.
	for _, path := range []string{"cache/deep/a.tmp", "src/two.tmp", "cache2/b.tmp", "cache", "src/one.tmp"} {
		collector.add(search.IgnoreTraceRecord{Path: path, Source: ".gitignore", Pattern: "cache"})
	}
	summary := collector.summary()
	var paths []string
	for _, row := range summary.Rows {
		paths = append(paths, row.Path)
	}
	if want := []string{"cache", "cache2/b.tmp", "src/one.tmp", "src/two.tmp"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%v want %v", paths, want)
	}
	if summary.Total != 5 {
		t.Fatal("changed underlying boundary count")
	}
}
