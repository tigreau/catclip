package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func TestNoIgnoreOneUnionPreservesLiteralOwnershipAfterGlobMatch(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":       "internal/\n",
		"internal/main.go": "package main\n",
		"README.md":        "readme\n",
	})
	resolver := Resolver{
		Cfg:          command.Invocation{WorkingDir: project, WithBinaries: true},
		WithBinaries: true,
		NoIgnore:     true,
	}
	scope := command.ExecutionScope{Targets: []string{"*.go", "internal", "*.md"}}

	var events []search.MembershipEnumerationEvent
	restore := search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) {
		events = append(events, event)
	})
	defer restore()
	expanded, optimized, err := resolver.expandRetainedEntriesUnderNoIgnore(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !optimized {
		t.Fatal("literal/glob union unexpectedly fell back to canonical discovery")
	}
	if got, want := entryRelPathsForNoIgnoreTest(expanded), []string{"README.md", "internal/main.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded paths = %v, want %v", got, want)
	}
	for _, entry := range expanded {
		if entry.RelPath == "internal/main.go" && entry.TargetRoot != "internal" {
			t.Fatalf("glob match prevented literal target-root enrichment: %+v", entry)
		}
	}

	noIgnoreUnions := 0
	for _, event := range events {
		if event.Kind == search.MembershipEnumerationFiles && event.IgnorePolicy == search.MembershipNoIgnore && event.Context.Reason == search.MembershipReasonNoIgnoreExpansion {
			noIgnoreUnions++
		}
	}
	if noIgnoreUnions != 1 {
		t.Fatalf("no-ignore union events = %d, want 1: %+v", noIgnoreUnions, events)
	}
}

func TestNoIgnoreMixedLiteralGlobUsesFilesystemCaseIdentity(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		"internal/main.go": "package main\n",
		"README.md":        "readme\n",
	})
	typed := filepath.Join(project, "INTERNAL")
	actual := filepath.Join(project, "internal")
	typedInfo, typedErr := os.Lstat(typed)
	actualInfo, err := os.Lstat(actual)
	if err != nil {
		t.Fatal(err)
	}
	caseInsensitive := typedErr == nil && os.SameFile(typedInfo, actualInfo)

	resolver := Resolver{
		Cfg:          command.Invocation{WorkingDir: project, WithBinaries: true},
		WithBinaries: true,
		NoIgnore:     true,
	}
	scope := command.ExecutionScope{Targets: []string{"INTERNAL", "*.md"}}
	expanded, optimized, err := resolver.expandRetainedEntriesUnderNoIgnore(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !caseInsensitive {
		if optimized || expanded != nil {
			t.Fatalf("case-sensitive filesystem accepted missing literal: optimized=%v entries=%v", optimized, expanded)
		}
		return
	}
	if !optimized {
		t.Fatal("case-insensitive literal/glob union unexpectedly fell back")
	}
	if got, want := entryRelPathsForNoIgnoreTest(expanded), []string{"README.md", "internal/main.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("case-folded union paths = %v, want %v", got, want)
	}
	for _, entry := range expanded {
		if entry.RelPath == "internal/main.go" && entry.TargetRoot != "internal" {
			t.Fatalf("case alias did not preserve canonical literal ownership: %+v", entry)
		}
	}
}

func TestNoIgnoreTargetRootMergeUsesFirstNonEmptyCanonicalRoot(t *testing.T) {
	specs := []noIgnoreTargetSpec{
		{relPath: "*.go", kind: noIgnoreTargetGlob},
		{relPath: "src", kind: noIgnoreTargetDir},
		{relPath: "src/nested", kind: noIgnoreTargetDir},
	}
	root, matched := noIgnoreTargetRootForPath(specs, "src/nested/main.go")
	if !matched || root != "src" {
		t.Fatalf("root=%q matched=%v, want first non-empty canonical root src", root, matched)
	}

	root, matched = noIgnoreTargetRootForPath([]noIgnoreTargetSpec{{relPath: "src/main.go", kind: noIgnoreTargetFile}}, "src/main.go")
	if !matched || root != "src" {
		t.Fatalf("exact-file root=%q matched=%v, want parent src", root, matched)
	}
}

func TestMembershipAccountingLabelsPrimaryGenerationAndCanonicalFallback(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		"src/main.go": "package main\n",
	})
	context := search.MembershipEnumerationContext{
		ScopeIndex:   1,
		ScopeKnown:   true,
		GenerationID: 17,
	}
	resolver := Resolver{
		Cfg:                   command.Invocation{WorkingDir: project, WithBinaries: true},
		WithBinaries:          true,
		MembershipEnumeration: context,
	}
	var events []search.MembershipEnumerationEvent
	restore := search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) {
		events = append(events, event)
	})
	defer restore()

	if err := resolver.BuildVisibleFileList(); err != nil {
		t.Fatal(err)
	}
	if err := resolver.BuildVisibleFileList(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != search.MembershipEnumerationFiles || events[0].IgnorePolicy != search.MembershipVisible || events[0].Context.Reason != search.MembershipReasonPrimaryTarget || events[0].Context.ScopeIndex != 1 || events[0].Context.GenerationID != 17 {
		t.Fatalf("primary generation events = %+v", events)
	}

	events = nil
	fallback := Resolver{
		Cfg:                   command.Invocation{WorkingDir: project, WithBinaries: true},
		WithBinaries:          true,
		MembershipEnumeration: context,
	}
	if _, err := fallback.discoverVisibleFilesUnder("src"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Context.Reason != search.MembershipReasonCanonicalFallback || events[0].Context.ScopeIndex != 1 || events[0].Context.GenerationID != 17 {
		t.Fatalf("canonical fallback events = %+v", events)
	}
}

func entryRelPathsForNoIgnoreTest(entries []Entry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].RelPath
	}
	return out
}

// TestNoIgnoreMixedUnionCorpusTiming is the opt-in post-implementation gate
// for the one-union mixed literal/glob path. It exercises the production
// ownership loop (including a filesystem case alias when the corpus volume is
// case-insensitive) rather than carrying a second benchmark-only algorithm.
func TestNoIgnoreMixedUnionCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("set CATCLIP_RUN_CORPUS_TESTS=1 to run the external corpus timing test")
	}
	corpus := filepath.Join(os.Getenv("HOME"), "Desktop", "catclip-test-data")
	actualLiteral := "linux-master"
	actualInfo, err := os.Lstat(filepath.Join(corpus, actualLiteral))
	if err != nil {
		t.Skipf("corpus literal unavailable: %v", err)
	}
	typedLiteral := actualLiteral
	if upperInfo, upperErr := os.Lstat(filepath.Join(corpus, "LINUX-MASTER")); upperErr == nil && os.SameFile(upperInfo, actualInfo) {
		typedLiteral = "LINUX-MASTER"
	}
	run := func(literal string) ([]Entry, time.Duration) {
		t.Helper()
		scope := command.ExecutionScope{Targets: []string{literal, "*.md"}}
		resolver := Resolver{
			Cfg:          command.Invocation{WorkingDir: corpus, WithBinaries: true},
			WithBinaries: true,
			NoIgnore:     true,
			MembershipEnumeration: search.MembershipEnumerationContext{
				Reason:       search.MembershipReasonNoIgnoreExpansion,
				ScopeIndex:   0,
				ScopeKnown:   true,
				GenerationID: 1,
			},
		}
		started := time.Now()
		entries, optimized, runErr := resolver.expandRetainedEntriesUnderNoIgnore(scope, nil)
		elapsed := time.Since(started)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !optimized {
			t.Fatal("corpus mixed union unexpectedly used canonical fallback")
		}
		if len(entries) < 50_000 {
			t.Fatalf("corpus selection unexpectedly small: %d", len(entries))
		}
		if !sort.SliceIsSorted(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath }) {
			t.Fatal("corpus mixed union is not canonically ordered")
		}
		return entries, elapsed
	}

	// Warm process caches before measuring the steady-state one-union path.
	warm, _ := run(actualLiteral)
	if typedLiteral != actualLiteral {
		caseWarm, _ := run(typedLiteral)
		if got, want := entryRelPathsForNoIgnoreTest(caseWarm), entryRelPathsForNoIgnoreTest(warm); !reflect.DeepEqual(got, want) {
			t.Fatal("case-alias warmup membership differs from exact-case membership")
		}
	}
	const trials = 7
	exactDurations := make([]time.Duration, 0, trials)
	aliasDurations := make([]time.Duration, 0, trials)
	measure := func(literal, label string, trial int) time.Duration {
		entries, elapsed := run(literal)
		if got, want := entryRelPathsForNoIgnoreTest(entries), entryRelPathsForNoIgnoreTest(warm); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s trial %d membership changed", label, trial)
		}
		return elapsed
	}
	for i := 0; i < trials; i++ {
		if typedLiteral == actualLiteral {
			exactDurations = append(exactDurations, measure(actualLiteral, "exact-case", i+1))
			continue
		}
		if i%2 == 0 {
			exactDurations = append(exactDurations, measure(actualLiteral, "exact-case", i+1))
			aliasDurations = append(aliasDurations, measure(typedLiteral, "case-alias", i+1))
		} else {
			aliasDurations = append(aliasDurations, measure(typedLiteral, "case-alias", i+1))
			exactDurations = append(exactDurations, measure(actualLiteral, "exact-case", i+1))
		}
	}
	sort.Slice(exactDurations, func(i, j int) bool { return exactDurations[i] < exactDurations[j] })
	t.Logf("corpus=%d selected files exact_literal=%q one-union median=%s trials=%v", len(warm), actualLiteral, exactDurations[len(exactDurations)/2], exactDurations)
	if len(aliasDurations) > 0 {
		sort.Slice(aliasDurations, func(i, j int) bool { return aliasDurations[i] < aliasDurations[j] })
		exactMedian := exactDurations[len(exactDurations)/2]
		aliasMedian := aliasDurations[len(aliasDurations)/2]
		t.Logf("typed_literal=%q one-union median=%s trials=%v alias_overhead=%.1f%%", typedLiteral, aliasMedian, aliasDurations, 100*(float64(aliasMedian)/float64(exactMedian)-1))
	}
}
