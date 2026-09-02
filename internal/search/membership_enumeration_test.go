package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMembershipEnumerationEventsCoverEveryDirectoryScanBoundary(t *testing.T) {
	dir := t.TempDir()
	writeMembershipFixture(t, dir, "visible.go", "package visible\n")
	writeMembershipFixture(t, dir, ".gitignore", "ignored/\n")
	writeMembershipFixture(t, dir, "ignored/hidden.go", "package hidden\n")

	var mu sync.Mutex
	var events []MembershipEnumerationEvent
	restore := SetMembershipEnumerationObserver(func(event MembershipEnumerationEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	defer restore()
	snapshot := func() []MembershipEnumerationEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]MembershipEnumerationEvent(nil), events...)
	}
	reset := func() {
		mu.Lock()
		events = nil
		mu.Unlock()
	}

	contextLabel := MembershipEnumerationContext{
		Reason:       MembershipReasonPrimaryTarget,
		ScopeIndex:   2,
		ScopeKnown:   true,
		GenerationID: 41,
	}
	paths, err := RunRipgrepFiles(dir, RipgrepFileOptions{Enumeration: contextLabel})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("visible paths = %v, want .gitignore and visible.go", paths)
	}
	got := snapshot()
	if len(got) != 1 {
		t.Fatalf("rg --files events = %d, want 1: %+v", len(got), got)
	}
	assertMembershipEvent(t, got[0], MembershipEnumerationFiles, MembershipVisible, MembershipReasonPrimaryTarget)
	if got[0].Context.ScopeIndex != 2 || !got[0].Context.ScopeKnown || got[0].Context.GenerationID != 41 || got[0].Failed || got[0].Cancelled {
		t.Fatalf("rg --files lifecycle labels = %+v", got[0])
	}

	reset()
	visibleContext := contextLabel.WithReason(MembershipReasonIgnoreAttribution)
	if _, err := ResolveVisibleFileSet(dir, "", []string{"."}, visibleContext); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveVisibleFileSet(dir, "", []string{"."}, visibleContext); err != nil {
		t.Fatal(err)
	}
	got = snapshot()
	if len(got) != 1 {
		t.Fatalf("visible-set cache miss/hit events = %d, want only one actual scan: %+v", len(got), got)
	}
	assertMembershipEvent(t, got[0], MembershipEnumerationVisibleSet, MembershipVisible, MembershipReasonIgnoreAttribution)

	reset()
	if _, err := ClassifyTextPaths(dir, []string{"visible.go"}); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("explicit-path content classification counted as membership: %+v", got)
	}

	reset()
	benchLog := filepath.Join(t.TempDir(), "bench.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", benchLog)
	if _, err := RunRipgrepDirectMatchLines(dir, ".", "package", ""); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("content-root scan counted as membership authority: %+v", got)
	}
	logBytes, err := os.ReadFile(benchLog)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	for _, want := range []string{`event="search.rg.direct_match_lines"`, `scan_class="content-root"`, `membership_authority="false"`} {
		if !strings.Contains(logText, want) {
			t.Fatalf("content-root scan log missing %s:\n%s", want, logText)
		}
	}
}

func TestMembershipEnumerationMarksCancelledSubprocess(t *testing.T) {
	dir := t.TempDir()
	writeMembershipFixture(t, dir, "visible.go", "package visible\n")

	var events []MembershipEnumerationEvent
	restoreObserver := SetMembershipEnumerationObserver(func(event MembershipEnumerationEvent) {
		events = append(events, event)
	})
	defer restoreObserver()
	if _, err := RunRipgrepFiles(dir, RipgrepFileOptions{Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Cancelled || events[0].Failed {
		t.Fatalf("successful timeout-guarded membership event = %+v, want ordinary success", events)
	}
	events = nil

	saved := reloadCancelCtx
	defer func() { reloadCancelCtx = saved }()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	reloadCancelCtx = cancelled
	if _, err := RunRipgrepFiles(dir, RipgrepFileOptions{}); err == nil {
		t.Fatal("cancelled membership subprocess returned nil error")
	}
	if len(events) != 1 || !events[0].Cancelled || !events[0].Failed {
		t.Fatalf("cancelled membership event = %+v, want one cancelled failure", events)
	}
}

func assertMembershipEvent(t *testing.T, event MembershipEnumerationEvent, kind MembershipEnumerationKind, policy MembershipIgnorePolicy, reason MembershipEnumerationReason) {
	t.Helper()
	if event.Kind != kind || event.IgnorePolicy != policy || event.Context.Reason != reason {
		t.Fatalf("membership event = %+v, want kind=%q policy=%q reason=%q", event, kind, policy, reason)
	}
	if event.Duration <= 0 {
		t.Fatalf("membership event has no duration: %+v", event)
	}
}

func writeMembershipFixture(t *testing.T, root, rel, contents string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
