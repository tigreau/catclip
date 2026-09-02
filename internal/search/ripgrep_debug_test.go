package search

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestParseRipgrepIgnoreTraceLineAcrossPinnedPlatformShapes(t *testing.T) {
	tests := []struct {
		name string
		line string
		want IgnoreTraceRecord
	}{
		{
			name: "unix directory",
			line: `rg: DEBUG|ignore::walk|crates/ignore/src/walk.rs:2011: ignoring ./node_modules: Ignore(IgnoreMatch(Gitignore(Glob { from: Some("./.gitignore"), original: "node_modules/", actual: "**/node_modules", is_whitelist: false, is_only_dir: true })))`,
			want: IgnoreTraceRecord{Path: "node_modules", Source: "./.gitignore", Pattern: "node_modules/", RuleDirectoryOnly: true},
		},
		{
			name: "windows escaped source",
			line: `rg: DEBUG|ignore::walk|crates/ignore/src/walk.rs:2011: ignoring cmd/.DS_Store: Ignore(IgnoreMatch(Gitignore(Glob { from: Some("C:\\work\\catclip\\.gitignore"), original: ".DS_Store", actual: "**/.DS_Store", is_whitelist: false, is_only_dir: false })))`,
			want: IgnoreTraceRecord{Path: "cmd/.DS_Store", Source: `C:\work\catclip\.gitignore`, Pattern: ".DS_Store"},
		},
		{
			name: "escaped pattern",
			line: `rg: DEBUG|ignore::walk|crates/ignore/src/walk.rs:2011: ignoring data/a.tmp: Ignore(IgnoreMatch(Gitignore(Glob { from: Some("./.hiss"), original: "a\\\"b", actual: "**/a", is_whitelist: false, is_only_dir: false })))`,
			want: IgnoreTraceRecord{Path: "data/a.tmp", Source: "./.hiss", Pattern: `a\"b`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRipgrepIgnoreTraceLine(tt.line)
			if !ok {
				t.Fatalf("parseRipgrepIgnoreTraceLine rejected fixture: %s", tt.line)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("record = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseRipgrepIgnoreTraceLineRejectsNonCausalDebugRecords(t *testing.T) {
	for _, line := range []string{
		`rg: DEBUG|ignore::gitignore|crates/ignore/src/gitignore.rs:409: opened gitignore file: ./.gitignore`,
		`rg: DEBUG|ignore::walk|crates/ignore/src/walk.rs:2011: ignoring ./visible: Whitelist(IgnoreMatch(Gitignore(Glob { from: Some("./.gitignore"), original: "!visible", actual: "**/visible", is_whitelist: true, is_only_dir: false })))`,
		`rg: DEBUG|globset|crates/globset/src/lib.rs:506: built glob set; 0 literals`,
	} {
		if got, ok := parseRipgrepIgnoreTraceLine(line); ok {
			t.Fatalf("non-causal line parsed as %#v: %s", got, line)
		}
	}
}

func TestMalformedCausalRipgrepDebugRecordIsRecognizedAsUnsupported(t *testing.T) {
	line := `rg: DEBUG|ignore::walk|crates/ignore/src/walk.rs:9999: ignoring cache: Ignore(IgnoreMatch(Gitignore(Glob { changed_fields: true, is_whitelist: false })))`
	if _, ok := parseRipgrepIgnoreTraceLine(line); ok {
		t.Fatal("malformed causal record unexpectedly parsed")
	}
	if !isUnsupportedRipgrepIgnoreTraceLine(line) {
		t.Fatal("malformed causal record would have been silently discarded")
	}
}

func TestRunRipgrepIgnoreTraceStreamsVisibleAndIgnoredWithoutAuthority(t *testing.T) {
	root := t.TempDir()
	writeMembershipFixture(t, root, ".gitignore", "node_modules/\n*.tmp\n")
	writeMembershipFixture(t, root, "src/main.go", "package main\n")
	writeMembershipFixture(t, root, "src/cache.tmp", "ignored\n")
	writeMembershipFixture(t, root, "node_modules/pkg/index.js", "ignored\n")

	var mu sync.Mutex
	var events []MembershipEnumerationEvent
	restore := SetMembershipEnumerationObserver(func(event MembershipEnumerationEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	defer restore()

	var visible []string
	var ignored []IgnoreTraceRecord
	counts, err := RunRipgrepIgnoreTrace(root, RipgrepFileOptions{
		Enumeration: MembershipEnumerationContext{Reason: MembershipReasonMetadataIgnoreTrace},
	}, func(path string) {
		visible = append(visible, path)
	}, func(record IgnoreTraceRecord) {
		ignored = append(ignored, record)
	})
	if err != nil {
		t.Fatalf("RunRipgrepIgnoreTrace: %v", err)
	}
	sort.Strings(visible)
	if want := []string{".gitignore", "src/main.go"}; !reflect.DeepEqual(visible, want) {
		t.Fatalf("visible = %#v, want %#v", visible, want)
	}
	if counts.Visible != len(visible) || counts.Ignored != len(ignored) {
		t.Fatalf("counts = %+v visible=%d ignored=%d", counts, len(visible), len(ignored))
	}
	paths := make([]string, 0, len(ignored))
	for _, record := range ignored {
		paths = append(paths, record.Path)
	}
	sort.Strings(paths)
	if want := []string{"node_modules", "src/cache.tmp"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("ignored paths = %#v, want %#v (records=%#v)", paths, want, ignored)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0].Kind != MembershipEnumerationIgnoreDebug || events[0].Context.Authority != MembershipAuthorityDiagnostic {
		t.Fatalf("trace enumeration event = %#v", events)
	}
}
