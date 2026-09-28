package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

// Previous production validator, retained as an independent correctness and
// performance oracle. Do not replace this with the optimized field comparator.
func sameCheckpointScopeProjection(got, want resolvedScopeView) bool {
	if !reflect.DeepEqual(got.Invocation, want.Invocation) ||
		!reflect.DeepEqual(got.GitContext, want.GitContext) ||
		got.ScopeIndex != want.ScopeIndex ||
		!reflect.DeepEqual(got.Scope, want.Scope) ||
		!reflect.DeepEqual(got.Scopes, want.Scopes) ||
		len(got.Entries) != len(want.Entries) {
		return false
	}
	for i := range got.Entries {
		left, right := got.Entries[i], want.Entries[i]
		left.AbsPath, right.AbsPath = "", ""
		left.ModTime, right.ModTime = time.Time{}, time.Time{}
		left.SizeBytes, right.SizeBytes = 0, 0
		left.SizeKnown, right.SizeKnown = false, false
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

func TestCheckpointValidationMatchesMaterializedOracle(t *testing.T) {
	for _, scope := range []command.ExecutionScope{
		{}, {Paths: true}, {Lines: true, LinesStart: 2, LinesEnd: 8},
		{Snippet: true, SnippetPattern: "needle", SnippetContextSet: true, SnippetContextLines: 3},
		{Diff: true, Staged: true}, {Diff: true, Unstaged: true},
	} {
		t.Run(fmt.Sprintf("%+v", scope), func(t *testing.T) {
			entry := scopeViewMemoEntry{
				inventory: &scopeViewInventory{entries: []discovery.Entry{
					{RelPath: "src/a.go", TargetRoot: "src", SizeKnown: true, SizeBytes: 10},
					{RelPath: "ignored/b.go", IgnoreBypassed: true, BlockSource: ".hiss", GitVisible: true},
				}},
				fileIDs:           []uint32{1, 0, 1},
				view:              resolvedScopeView{Scope: scope, Scopes: []command.ExecutionScope{scope}},
				snippetMatchLines: map[uint32][]int{1: {2, 5}, 0: {}},
			}
			want := materializeScopeView(entry)
			if !checkpointMatchesRetainedScope(want, entry) {
				t.Fatal("valid materialized view rejected")
			}
			// Reflect over EVERY field so future Entry additions cannot silently
			// escape validation. Metadata mutations must still be accepted.
			for i := 0; i < reflect.TypeOf(discovery.Entry{}).NumField(); i++ {
				got := materializeScopeView(entry)
				field := reflect.ValueOf(&got.Entries[0]).Elem().Field(i)
				name := reflect.TypeOf(discovery.Entry{}).Field(i).Name
				switch field.Kind() {
				case reflect.String:
					field.SetString(field.String() + "changed")
				case reflect.Bool:
					field.SetBool(!field.Bool())
				case reflect.Int, reflect.Int64:
					field.SetInt(field.Int() + 1)
				case reflect.Slice:
					if field.Type() != reflect.TypeOf([]int{}) {
						t.Fatalf("add mutation coverage for %s", name)
					}
					field.Set(reflect.ValueOf([]int{999}))
				case reflect.Struct:
					if field.Type() != reflect.TypeOf(time.Time{}) {
						t.Fatalf("add mutation coverage for %s", name)
					}
					field.Set(reflect.ValueOf(time.Unix(1, 0)))
				default:
					t.Fatalf("add mutation coverage for %s", name)
				}
				if old, optimized := sameCheckpointScopeProjection(got, want), checkpointMatchesRetainedScope(got, entry); old != optimized {
					t.Errorf("field %s: oracle=%v optimized=%v", name, old, optimized)
				}
			}
			for _, mutate := range []func(*resolvedScopeView){
				func(v *resolvedScopeView) { v.Scope.Paths = !v.Scope.Paths },
				func(v *resolvedScopeView) { v.Scopes[0].NoIgnore = !v.Scopes[0].NoIgnore },
				func(v *resolvedScopeView) { v.Invocation.WorkingDir = "changed" },
				func(v *resolvedScopeView) { v.GitContext.Root = "changed" },
				func(v *resolvedScopeView) { v.ScopeIndex++ },
				func(v *resolvedScopeView) { v.fileIDs[0] = 0 },
				func(v *resolvedScopeView) { v.inventory = &scopeViewInventory{} },
				func(v *resolvedScopeView) { v.Entries = v.Entries[:1] },
				func(v *resolvedScopeView) { v.Entries[0].SnippetMatchLines = nil },
			} {
				got := materializeScopeView(entry)
				mutate(&got)
				if checkpointMatchesRetainedScope(got, entry) {
					t.Fatal("accepted corrupted command, IDs, inventory, or snippet lines")
				}
			}
			if !reflect.DeepEqual(materializeScopeView(entry), want) {
				t.Fatal("validation mutated retained state")
			}
		})
	}
}

// Uses real full-corpus membership, not a sampled or repeated synthetic file
// list. Discovery and completed metadata are outside each timed validator.
func BenchmarkCheckpointValidationCorpus(b *testing.B) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		b.Skip("opt-in full corpus")
	}
	corpus := os.Getenv("CATCLIP_TEST_CORPUS")
	if corpus == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			b.Fatal(err)
		}
		corpus = filepath.Join(home, "Desktop", "catclip-test-data")
	}
	b.Chdir(corpus)
	for _, scenario := range []struct {
		name string
		args []string
	}{
		{"visible", []string{"--quiet", "--print", "."}},
		{"no-ignore-binaries", []string{"--quiet", "--print", ".", "--no-ignore", "--with-binaries"}},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			view, err := resolvedCurrentScopeViewForArgs(scenario.args)
			if err != nil {
				b.Fatal(err)
			}
			if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
				b.Fatal("metadata incomplete")
			}
			_, key := scopeViewMemoKey(scenario.args)
			scopeViewMemoMu.Lock()
			entry := scopeViewMemoValues[key]
			scopeViewMemoMu.Unlock()
			b.Logf("entries=%d", len(view.Entries))
			for _, optimized := range []bool{false, true} {
				name := "materialized"
				if optimized {
					name = "direct"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var valid bool
						if optimized {
							valid = checkpointMatchesRetainedScope(view, entry)
						} else {
							valid = sameCheckpointScopeProjection(view, materializeScopeView(entry))
						}
						if !valid {
							b.Fatal("valid checkpoint rejected")
						}
					}
				})
			}
		})
	}
}
