package output

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

func TestPlainModeTagsStillRecognizeLaterRepresentations(t *testing.T) {
	full := newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go", Mode: command.EntryModeFull}})
	for _, tc := range []struct {
		name string
		last PlanItem
		want string
	}{
		{"full", full, ""},
		{"implicit full", newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go"}}), ""},
		{"paths", newPathOutputPlanItem(discovery.Entry{RelPath: "a.go"}), "path + file"},
		{"metadata", PlanItem{kind: SectionKindMetadata, relPath: "a.go"}, "metadata + file"},
		{"snippet", newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go", Mode: command.EntryModeSnippet}}), "snippet"},
		{"untracked", newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go", Mode: command.EntryModeFull}, sourceType: "untracked"}), "untracked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, items := range [][]PlanItem{{full, tc.last}, {tc.last, full}} {
				got := (Plan{items: items}).PreviewModeTags(nil)
				want := map[string]string{}
				if tc.want != "" {
					want["a.go"] = tc.want
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v, want %v", got, want)
				}
			}
		})
	}
}

func BenchmarkPreviewModeTagsFullFiles(b *testing.B) {
	for _, n := range []int{256, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			units := make([]PreparedFileUnit, n)
			for i := range units {
				units[i] = PreparedFileUnit{Entry: discovery.Entry{RelPath: fmt.Sprintf("src/file%06d.go", i), Mode: command.EntryModeFull}, BodyBytes: 100}
			}
			plan := BuildPlan(units)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if len(plan.PreviewModeTags(nil)) != 0 {
					b.Fatal("unexpected tags")
				}
			}
		})
	}
}
