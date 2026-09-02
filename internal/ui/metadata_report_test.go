package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
)

func TestWriteMetadataReportPlainStableLayout(t *testing.T) {
	report := &MetadataReport{
		Root:      "catclip",
		Generated: "2026-09-01T14:32:00+03:00",
		Git:       &MetadataGitSummary{Branch: "main", Commit: "abc123", Modified: 1},
		Scopes: []MetadataScope{{
			Summary:      "internal/ui --only '*.go'",
			Selected:     2,
			Visible:      4,
			VisibleKnown: true,
			Ignored: MetadataIgnoredSummary{Total: 1, Rows: []MetadataIgnoredPath{{
				Path: "internal/ui/cache", Kind: "directory", Source: ".gitignore", Pattern: "cache/",
			}}},
		}},
		Composition: []MetadataAggregate{{Label: ".go", Count: 2, Bytes: 41780, Tokens: 10445}},
		Rows: []MetadataRow{
			{Path: "internal/ui/a.go", Size: "780.00B", Tokens: "~195", Git: "M", Modified: "Today at 9:30 AM"},
			{Path: "internal/ui/deep/b.go", Size: "40.04KB", Tokens: "~10,250", Git: "-", Modified: "Mar 11, 2026 at 2:17 AM"},
		},
		TotalBytes: 41780,
		TextTokens: 10445,
	}
	var buf bytes.Buffer
	if err := WriteMetadataReport(&buf, report); err != nil {
		t.Fatalf("WriteMetadataReport: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Root: catclip\nGenerated: 2026-09-01T14:32:00+03:00\n",
		"Git: main @ abc123 · 1 modified\n",
		"Coverage: 4 raw visible files · 2 selected files\n",
		"Ignored within target scope: 1 boundary path\n",
		"internal/ui/cache [directory] · source: .gitignore · pattern: cache/",
		"Composition (largest 5 by size):",
		"2 files · 40.80KB · ~10,445 tokens\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metadata report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "shape") || strings.Contains(out, "\x1b[") {
		t.Fatalf("report must omit shape and ANSI:\n%s", out)
	}
	size, err := report.EncodedSize()
	if err != nil {
		t.Fatalf("EncodedSize: %v", err)
	}
	if size != int64(buf.Len()) {
		t.Fatalf("EncodedSize = %d, actual = %d", size, buf.Len())
	}
}

func TestWriteMetadataReportBinaryFlagAndNoIgnore(t *testing.T) {
	report := &MetadataReport{
		Root:        "catclip",
		Scopes:      []MetadataScope{{Summary: ".", NoIgnore: true, Selected: 2}},
		Composition: []MetadataAggregate{{Label: ".png", Count: 1, Bytes: 86220, BinaryCount: 1}, {Label: ".md", Count: 1, Bytes: 3277, Tokens: 819}},
		Rows:        []MetadataRow{{Path: "README.md", Size: "3.20KB", Tokens: "~819", Git: "-", Modified: "Today"}, {Path: "logo.png", Size: "84.20KB", Tokens: "—", Git: "?", Modified: "Aug 20", Flag: "binary"}},
		TotalBytes:  89497,
		TextTokens:  819,
		BinaryCount: 1,
	}
	var buf bytes.Buffer
	if err := WriteMetadataReport(&buf, report); err != nil {
		t.Fatalf("WriteMetadataReport: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Ignored") || strings.Contains(out, "Coverage") {
		t.Fatalf("--no-ignore report must omit ignore-derived facts:\n%s", out)
	}
	for _, want := range []string{"Selected: 2 files", ".png", "~0 text tokens · 1 binary", "MODIFIED  FLAGS", "logo.png", "—", "binary", "~819 text tokens · 1 binary"} {
		if !strings.Contains(out, want) {
			t.Fatalf("binary report missing %q:\n%s", want, out)
		}
	}
}

func TestWriteMetadataScopeFactsUsesSingularAndDoesNotInventUnknownIgnoreFacts(t *testing.T) {
	var singular bytes.Buffer
	if err := writeMetadataScopeFacts(&singular, MetadataScope{
		Selected: 1, Visible: 1, VisibleKnown: true,
	}, ""); err != nil {
		t.Fatalf("write singular scope facts: %v", err)
	}
	if got := singular.String(); !strings.Contains(got, "1 raw visible file · 1 selected file") {
		t.Fatalf("singular scope facts = %q", got)
	}

	var unknown bytes.Buffer
	if err := writeMetadataScopeFacts(&unknown, MetadataScope{Selected: 0}, ""); err != nil {
		t.Fatalf("write unknown scope facts: %v", err)
	}
	if got := unknown.String(); got != "" {
		t.Fatalf("unobserved scope invented facts: %q", got)
	}

	var noIgnore bytes.Buffer
	if err := writeMetadataScopeFacts(&noIgnore, MetadataScope{NoIgnore: true, Selected: 1}, ""); err != nil {
		t.Fatalf("write no-ignore scope facts: %v", err)
	}
	if got := noIgnore.String(); got != "Selected: 1 file\n" {
		t.Fatalf("singular no-ignore scope facts = %q", got)
	}
}

func TestMetadataScopeSummaryPreservesMembershipStages(t *testing.T) {
	limit := 2
	scope := command.ExecutionScope{
		Targets: []string{"internal"},
		Stages: []command.Stage{
			{Kind: command.StageOnly, Values: []string{"*.go"}},
			{Kind: command.StageDepth, Limit: &limit},
			{Kind: command.StageContains, Values: []string{"TODO"}},
		},
	}
	got := metadataScopeSummary(scope)
	for _, want := range []string{"internal", "--only", "*.go", "--depth", "2", "--contains", "TODO"} {
		if !strings.Contains(got, want) {
			t.Fatalf("scope summary %q missing %q", got, want)
		}
	}
}

func TestPopulateMetadataIgnoreTraceIsScopeOwnedAndDiagnostic(t *testing.T) {
	root := t.TempDir()
	for rel, content := range map[string]string{
		".gitignore":               "cache/\ndrafts/\nprivate/\n",
		"src/main.go":              "package main\n",
		"src/cache/generated.go":   "package cache\n",
		"docs/guide.md":            "# Guide\n",
		"docs/drafts/hidden.md":    "# Hidden\n",
		"other/private/secret.txt": "hidden\n",
	} {
		writeMetadataFixture(t, root, rel, content)
	}
	scopes := []discovery.Scope{
		{Scope: command.ExecutionScope{Targets: []string{"src"}}, Entries: []discovery.Entry{{RelPath: "src/main.go", TargetRoot: "src"}}},
		{Scope: command.ExecutionScope{Targets: []string{"docs"}}, Entries: []discovery.Entry{{RelPath: "docs/guide.md", TargetRoot: "docs"}}},
	}
	summaries := []MetadataScope{{Selected: 1}, {Selected: 1}}
	var events []search.MembershipEnumerationEvent
	restore := search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) {
		if event.Context.Reason == search.MembershipReasonMetadataIgnoreTrace {
			events = append(events, event)
		}
	})
	defer restore()
	if err := populateMetadataIgnoreTrace(root, scopes, summaries); err != nil {
		t.Fatalf("populateMetadataIgnoreTrace: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("metadata trace enumerations = %d, want 1: %#v", len(events), events)
	}
	if events[0].Kind != search.MembershipEnumerationIgnoreDebug || events[0].Context.Authority != search.MembershipAuthorityDiagnostic {
		t.Fatalf("metadata trace was not diagnostic-only: %#v", events[0])
	}
	assertMetadataScopeTrace(t, summaries[0], "src/cache", "docs/drafts", "other/private")
	assertMetadataScopeTrace(t, summaries[1], "docs/drafts", "src/cache", "other/private")
}

func TestPopulateMetadataIgnoreTraceNoIgnoreSkipsDiagnosticWalk(t *testing.T) {
	root := t.TempDir()
	writeMetadataFixture(t, root, "ignored.txt", "ignored\n")
	scopes := []discovery.Scope{{
		Scope:   command.ExecutionScope{Targets: []string{"."}, NoIgnore: true},
		Entries: []discovery.Entry{{RelPath: "ignored.txt"}},
	}}
	summaries := []MetadataScope{{NoIgnore: true, Selected: 1}}
	var scans int
	restore := search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) {
		if event.Context.Reason == search.MembershipReasonMetadataIgnoreTrace {
			scans++
		}
	})
	defer restore()
	if err := populateMetadataIgnoreTrace(root, scopes, summaries); err != nil {
		t.Fatalf("populateMetadataIgnoreTrace: %v", err)
	}
	if scans != 0 || summaries[0].VisibleKnown {
		t.Fatalf("--no-ignore metadata trace = %d scans, summary %#v", scans, summaries[0])
	}
}

func TestMetadataIgnoredCollectorCapsRowsButRetainsExactTotal(t *testing.T) {
	collector := metadataIgnoredCollector{workingDir: t.TempDir()}
	for i := 0; i < metadataIgnoredDisplayLimit+7; i++ {
		collector.add(search.IgnoreTraceRecord{Path: fmt.Sprintf("ignored-%02d", i), Pattern: "ignored-*"})
	}
	summary := collector.summary()
	if summary.Total != metadataIgnoredDisplayLimit+7 || len(summary.Rows) != metadataIgnoredDisplayLimit {
		t.Fatalf("ignored summary = total %d, rows %d", summary.Total, len(summary.Rows))
	}
}

func TestSortedMetadataAggregatesKeepsLargestFive(t *testing.T) {
	groups := map[string]*MetadataAggregate{}
	for i := 0; i < 7; i++ {
		addMetadataAggregate(groups, fmt.Sprintf(".%d", i), int64(i+1), int64(i), false)
	}
	got := sortedMetadataAggregates(groups, metadataAggregateLimit)
	if len(got) != 5 || got[0].Label != ".6" || got[4].Label != ".2" {
		t.Fatalf("largest-five aggregates = %#v", got)
	}
}

func TestMetadataGitSummaryCountsOnlySelectedEntries(t *testing.T) {
	summary := buildMetadataGitSummary(t.TempDir(), git.Context{Enabled: true}, []discovery.Entry{
		{RelPath: "selected.go"},
	}, map[string]string{
		"selected.go": "M",
		"outside.go":  "?",
	})
	if summary.Modified != 1 || summary.Untracked != 0 {
		t.Fatalf("selected Git summary = %#v", summary)
	}
}

func writeMetadataFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertMetadataScopeTrace(t *testing.T, summary MetadataScope, want string, unwanted ...string) {
	t.Helper()
	if !summary.VisibleKnown || summary.Visible != 1 {
		t.Fatalf("scope coverage = %#v, want one raw visible file", summary)
	}
	if summary.Ignored.Total != 1 || len(summary.Ignored.Rows) != 1 {
		t.Fatalf("scope ignored summary = %#v, want one boundary", summary.Ignored)
	}
	row := summary.Ignored.Rows[0]
	if row.Path != want || !strings.HasSuffix(filepath.ToSlash(row.Source), ".gitignore") {
		t.Fatalf("scope ignored row = %#v, want path %q caused by .gitignore", row, want)
	}
	for _, value := range unwanted {
		if row.Path == value {
			t.Fatalf("scope ignored row leaked %q: %#v", value, row)
		}
	}
}
