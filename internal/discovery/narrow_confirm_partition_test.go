package discovery

import (
	"reflect"
	"testing"
)

func TestPartitionIgnoredByIncludes(t *testing.T) {
	authorizedDocs := []Entry{
		{RelPath: "src/main.go"},
		{RelPath: "docs/readme.md", AllowedByInclude: true, BlockSource: ".gitignore"},
		{RelPath: "docs/sub/x.md", AllowedByInclude: true, BlockSource: ".gitignore"},
		{RelPath: "docs/tracked.md"},
	}
	descendants := []Entry{
		{RelPath: "docs/a.md", AllowedByInclude: true},
		{RelPath: "vendor/b.go", AllowedByInclude: true},
		{RelPath: "docs/sub/c.md", AllowedByInclude: true},
	}
	stable := []Entry{
		{RelPath: "docs/z.md", AllowedByInclude: true},
		{RelPath: "docs/a.md", AllowedByInclude: true},
		{RelPath: "docs/m.md", AllowedByInclude: true},
	}
	union := []Entry{
		{RelPath: "docs/a.md", AllowedByInclude: true},
		{RelPath: "vendor/b.go", AllowedByInclude: true},
		{RelPath: "src/c.go"},
	}
	wildcard := []Entry{
		{RelPath: "src/main.ts"},
		{RelPath: "src/debug.log", AllowedByInclude: true},
		{RelPath: "docs/generated.md", AllowedByInclude: true},
	}
	tests := []struct {
		name        string
		entries     []Entry
		includes    []string
		wantAll     []Entry
		wantIgnored []Entry
	}{
		{name: "empty entries", includes: []string{"docs"}},
		{name: "empty includes", entries: []Entry{{RelPath: "a.txt"}}, wantAll: []Entry{{RelPath: "a.txt"}}},
		{name: "dot and empty includes normalize away", entries: []Entry{{RelPath: "docs/x.md", AllowedByInclude: true}}, includes: []string{".", "", "  "}, wantAll: []Entry{{RelPath: "docs/x.md", AllowedByInclude: true}}},
		{name: "only include-authorized entries", entries: authorizedDocs, includes: []string{"docs"}, wantAll: authorizedDocs, wantIgnored: authorizedDocs[1:3]},
		{name: "only descendants of include path", entries: descendants, includes: []string{"docs"}, wantAll: descendants, wantIgnored: []Entry{descendants[0], descendants[2]}},
		{name: "path equal to include", entries: []Entry{{RelPath: "docs", AllowedByInclude: true}}, includes: []string{"docs"}, wantAll: []Entry{{RelPath: "docs", AllowedByInclude: true}}, wantIgnored: []Entry{{RelPath: "docs", AllowedByInclude: true}}},
		{name: "stable input order", entries: stable, includes: []string{"docs"}, wantAll: stable, wantIgnored: stable},
		{name: "multiple include union", entries: union, includes: []string{"docs", "vendor"}, wantAll: union, wantIgnored: union[:2]},
		{name: "wildcard selects authorized", entries: wildcard, includes: []string{"*"}, wantAll: wildcard, wantIgnored: wildcard[1:]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all, ignored := PartitionIgnoredByIncludes(tt.entries, tt.includes)
			if !reflect.DeepEqual(all, tt.wantAll) {
				t.Fatalf("all = %v, want %v", all, tt.wantAll)
			}
			if !reflect.DeepEqual(ignored, tt.wantIgnored) {
				t.Fatalf("ignored = %v, want %v", ignored, tt.wantIgnored)
			}
		})
	}
}
