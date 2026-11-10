package ui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/search"
)

func TestScopeMetadataMenuCompatibilityAndUndo(t *testing.T) {
	metadata := StartupModifierChoice{Args: []string{"--metadata"}}
	for _, flag := range []string{"--paths", "--lines", "--snippet", "--changed-diff", "--staged-diff", "--unstaged-diff"} {
		args := []string{".", flag}
		if flag == "--snippet" {
			args = append(args, "match")
		}
		if startupModifierChoiceAllowed(args, metadata, startupCurrentScopeState{}) {
			t.Fatalf("metadata offered after %s", flag)
		}
		if startupModifierChoiceAllowed([]string{".", "--metadata"}, StartupModifierChoice{Args: []string{flag}}, startupCurrentScopeState{}) {
			t.Fatalf("%s offered after metadata", flag)
		}
		fresh := append(append([]string(nil), args...), "--then", ".")
		if !startupModifierChoiceAllowed(fresh, metadata, startupCurrentScopeState{}) {
			t.Fatalf("metadata did not reset after %s --then", flag)
		}
	}
	// Undo restores argv; there must be no global metadata bit outside it.
	if !startupModifierChoiceAllowed([]string{"."}, metadata, startupCurrentScopeState{}) {
		t.Fatal("metadata did not return on undo")
	}
	if startupModifierChoiceAllowed([]string{".", "--raw"}, metadata, startupCurrentScopeState{}) {
		t.Fatal("metadata offered with raw")
	}
	for _, choice := range startupAvailableExtras([]string{".", "--metadata", "--then", "."}) {
		if choice.Key == "raw" {
			t.Fatal("global raw offered after metadata in another scope")
		}
	}
	for _, choice := range startupExtrasChoices {
		if choice.Key == "metadata" {
			t.Fatal("metadata remains in Extras")
		}
	}
	for i, choice := range startupModifierChoices {
		if choice.Key == "metadata" && (i+1 >= len(startupModifierChoices) || startupModifierChoices[i+1].Key != "paths") {
			t.Fatal("metadata must be visually below paths in reversed fzf list")
		}
	}
	args := []string{".", "--metadata", "--then", "file.go", "--lines", "1", "2"}
	cfg, err := cli.ParseArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := command.ExecutionScopesFromSpec(cfg.Command); !got[0].Metadata || got[1].Metadata {
		t.Fatalf("metadata leaked: %+v", got)
	}
	if got := cli.FormatResolvedStartupCommand(args); strings.Index(got, "--metadata") > strings.Index(got, "--then") {
		t.Fatal(got)
	}
}

func BenchmarkScopeMetadataPreparationAndReuse(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			entries := make([]discovery.Entry, n)
			now := time.Now()
			for i := range entries {
				entries[i] = discovery.Entry{RelPath: fmt.Sprintf("src/file-%06d.go", i), SizeKnown: true, SizeBytes: 1024, ModTime: now}
			}
			meta := command.ExecutionScope{Targets: []string{"src"}, NoIgnore: true, Metadata: true}
			paths := command.ExecutionScope{Targets: []string{"src"}, Paths: true}
			scopes := []discovery.Scope{{Scope: meta, Entries: entries}, {Scope: paths, Entries: entries[:1]}}
			plan, err := output.BuildPlanForResolvedScopes(git.Context{}, []command.ExecutionScope{meta, paths}, []output.EvaluatedScope{{Entries: entries}, {Paths: true, Entries: entries[:1]}}, nil)
			if err != nil {
				b.Fatal(err)
			}
			report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{})
			if err != nil {
				b.Fatal(err)
			}
			b.Run("prepare", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, _, err := PrepareScopeMetadata("unused", git.Context{}, scopes, plan, report); err != nil {
						b.Fatal(err)
					}
				}
			})
			prepared, ready, err := PrepareScopeMetadata("unused", git.Context{}, scopes, plan, report)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("reuse", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, _, err := PrepareScopeMetadata("unused", git.Context{}, scopes, prepared, ready); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func TestScopeMetadataPreparedReuseAndPreview(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	wd := t.TempDir()
	// This path intentionally does not exist: retained facts suffice for metadata,
	// and no body preparation/open is allowed for its contribution.
	entry := discovery.Entry{RelPath: "missing.go", AbsPath: wd + "/missing.go", SizeKnown: true, SizeBytes: 1000000, ModTime: time.Now()}
	meta := command.ExecutionScope{Targets: []string{"missing.go"}, NoIgnore: true, Metadata: true}
	paths := command.ExecutionScope{Targets: []string{"missing.go"}, Paths: true}
	scopes := []discovery.Scope{{Scope: meta, Entries: []discovery.Entry{entry}}, {Scope: paths, Entries: []discovery.Entry{entry}}}
	plan, err := output.BuildPlanForResolvedScopes(git.Context{}, []command.ExecutionScope{meta, paths}, []output.EvaluatedScope{{Entries: scopes[0].Entries}, {Paths: true, Entries: scopes[1].Entries}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, report, err = PrepareScopeMetadata(wd, git.Context{}, scopes, plan, report)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := PrepareScopeMetadata(wd, git.Context{}, scopes, plan, report)
	if err != nil || !plan.SameInstance(again) {
		t.Fatalf("prepared report rebuilt: %v", err)
	}
	var plain bytes.Buffer
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(&plain, output.EmitConfig{}, plan); err != nil {
		t.Fatal(err)
	}
	_, size := plan.PayloadSizes()
	// One newline separates metadata and paths.
	if size+1 != int64(plain.Len()) || size > 10000 {
		t.Fatalf("size=%d emitted=%d; counted represented contents", size, plain.Len())
	}
	preview, err := readSinkOutputTextPreview(context.Background(), plan, output.EmitConfig{}, output.PreviewByteLimit, false)
	if err != nil || !bytes.Contains(preview.Body, []byte("\x1b[")) {
		t.Fatalf("metadata preview lost colors: %v\n%s", err, preview.Body)
	}
	m := measureOutputForSinkMenu(plan, output.EmitConfig{})
	if m.Err != nil || m.Bytes != int64(plain.Len()) || m.RawPreview != nil {
		t.Fatalf("measurement=%+v emitted=%d", m, plain.Len())
	}
}

func TestScopeMetadataRetainedHandoffAndUndo(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "alpha\nbravo\n", "src/b.go": "second\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	base := []string{"src", "--no-ignore"}
	if _, err := resolvedCurrentScopeViewForArgs(base); err != nil {
		t.Fatal(err)
	}
	meta := append(append([]string(nil), base...), "--metadata")
	walks := 0
	restore := search.SetMembershipEnumerationObserver(func(_ search.MembershipEnumerationEvent) { walks++ })
	defer restore()
	if _, err := resolvedCurrentScopeViewForArgs(meta); err != nil {
		t.Fatal(err)
	}
	ctx, err := buildStartupSinkPickerContext(meta)
	if err != nil || ctx.Metadata == nil {
		t.Fatalf("metadata handoff: %v", err)
	}
	view, err := resolvedCurrentScopeViewForArgs(base)
	if err != nil || view.Scope.Metadata {
		t.Fatalf("undo retained metadata: %v", err)
	}
	if walks != 0 {
		t.Fatalf("metadata stage/undo repeated membership %d times", walks)
	}
	choice := StartupModifierChoice{Args: []string{"--snippet", "alpha"}}
	if !startupModifierChoiceAllowed(base, choice, startupCurrentScopeState{}) {
		t.Fatal("undo failed to restore snippet")
	}
	if startupModifierChoiceAllowed(meta, choice, startupCurrentScopeState{}) {
		t.Fatal("metadata still offers snippet")
	}
}
