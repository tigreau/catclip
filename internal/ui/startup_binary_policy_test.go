package ui

import (
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/search"
)

func TestStartupBinaryPolicyPreservesOperandsAndStageOrder(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
	}{
		{[]string{"lib.jar", "--with-binaries", "--no"}, []string{"lib.jar", "--with-binaries", "--no"}},
		{[]string{"src", "--recent", "10", "--then", "other", "-b"}, []string{"src", "--recent", "10", "--then", "other", "-b"}},
		{[]string{"src", "--contains", "--with-binaries"}, []string{"src", "--contains", "--with-binaries"}},
	} {
		globals, pending := startupBinaryPolicyArgs(tc.args)
		if len(globals) != 0 || !reflect.DeepEqual(pending, tc.want) {
			t.Fatalf("%v: globals=%v pending=%v", tc.args, globals, pending)
		}
	}
}

func TestStartupBinarySelectionRetainedHandoff(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"one/lib.jar": "PK\x03\x04\x00binary one",
		"two/lib.jar": "PK\x03\x04\x00binary two",
	})
	t.Chdir(project)
	for _, raw := range [][]string{
		{"one/lib.jar", "--with-binaries", "--no"},
		{"one/lib.jar", "-b", "--no"},
		{"one/lib.jar", "-b", "--then", "two/lib.jar", "--with-binaries", "--no"},
	} {
		t.Run(raw[0]+raw[1], func(t *testing.T) {
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			resolver, err := newStartupPickerResolverForArgs(raw)
			if err != nil {
				t.Fatal(err)
			}
			defer resolver.ReleaseRetainedTargetPreviewInventory()
			current, pending := startupBinaryPolicyArgs(raw)
			for {
				frame, doneArgs, done, err := nextStartupInteractiveFrame(resolver, current, pending)
				if err != nil {
					t.Fatal(err)
				}
				if done {
					current = doneArgs
					break
				}
				if frame.Kind != startupInteractiveFrameTarget {
					t.Fatalf("unexpected frame %s", frame.Kind)
				}
				// Use exact selected paths instead of driving fzf. The commit and
				// retained-state handoff are the same as a fuzzy picker selection.
				result, err := runStartupTargetFrame(resolver, frame)
				if err != nil {
					t.Fatal(err)
				}
				entries, metadata, ok := resolver.CommittedTargetSelection()
				if !ok || !scopeViewMemoAdoptTargetSelection(result.Args, resolver.GitCtx, entries, metadata) {
					if err := scopeViewMemoSealEvaluatedTarget(result.Args); err != nil {
						t.Fatal(err)
					}
				}
				current, pending = result.Args, result.Pending
			}
			var scans int
			restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { scans++ })
			defer restore()
			ctx, err := buildStartupSinkPickerContext(current)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"one/lib.jar"}
			if len(raw) > 4 {
				want = append(want, "two/lib.jar")
			}
			if got := ctx.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, want) {
				t.Fatalf("paths=%v want=%v", got, want)
			}
			if ctx.Config.WithBinaries || scans != 0 {
				t.Fatalf("global binary=%v rescans=%d", ctx.Config.WithBinaries, scans)
			}
		})
	}
}
