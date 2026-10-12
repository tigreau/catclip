package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

func TestContentAllMatchesPreviewReusesCompletedMembership(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	project := setupTestProject(t, map[string]string{
		"hit.go":     "package sample\n\nfunc First() {\n // needle\n}\n\nfunc Second() {\n // needle\n}\n",
		"miss.go":    "package sample\n// other\n",
		"outside.go": "package sample\n// needle\n",
	})
	t.Chdir(project)
	t.Setenv("FZF_MATCH_COUNT", "2")
	for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
		t.Run(flag, func(t *testing.T) {
			checkpoint := filepath.Join(t.TempDir(), "scope.json")
			// Retained membership deliberately excludes outside.go. Memo reuse
			// must intersect it, rather than treating cached paths as new targets.
			if err := discovery.WriteCheckpoint(checkpoint, project, discovery.CheckpointData{
				Entries: []discovery.Entry{{RelPath: "hit.go"}, {RelPath: "miss.go"}},
			}); err != nil {
				t.Fatal(err)
			}
			cfg := FilePreviewConfigFromParsedCommand(parseInProject(t, project, []string{
				"--internal-file-preview", "--internal-searching-preview",
				"--internal-file-path", contentMatchAllMatchesFocusPath,
				"--internal-tree-target", contentMatchAllMatchesLabel,
				"--internal-prediscovered", checkpoint, flag, "needle",
			}))
			var want bytes.Buffer
			if err := RunInternalFilePreview(cfg, &want); err != nil {
				t.Fatal(err)
			}
			// Exercise membership-memo reuse on a frame miss; otherwise the
			// new rendered-frame hit correctly skips this layer altogether.
			if err := os.RemoveAll(filepath.Join(filepath.Dir(checkpoint), "preview-frames")); err != nil {
				t.Fatal(err)
			}
			survivor := "hit.go"
			if flag == "--not-contains" {
				survivor = "miss.go"
			}
			writeContentMatchMemo(contentMatchMemoPath(checkpoint), "needle", []string{
				filepath.Join(project, survivor), filepath.Join(project, "outside.go"),
			})
			logPath := filepath.Join(t.TempDir(), "preview.log")
			t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", logPath)
			if flag != "--snippet" {
				// A completed positive/negative match already supplies membership.
				// The preview must work even when another rg launch is impossible.
				t.Setenv("CATCLIP_RG", filepath.Join(t.TempDir(), "missing-rg"))
			}
			var got bytes.Buffer
			if err := RunInternalFilePreview(cfg, &got); err != nil {
				t.Fatal(err)
			}
			if got.String() != want.String() {
				t.Fatalf("memo changed preview\ngot: %s\nwant: %s", &got, &want)
			}
			plain := string(ansiEscape.ReplaceAll(got.Bytes(), nil))
			if !strings.Contains(plain, survivor) || strings.Contains(plain, "outside.go") || strings.Contains(plain, "Searching") {
				t.Fatalf("wrong completed preview: %s", plain)
			}
			if flag == "--snippet" && !strings.Contains(plain, "2 snippets") {
				t.Fatalf("snippet memo lost a matching block: %s", plain)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), `content_memo_hit="true"`) || strings.Contains(string(log), `event="search.rg.matches"`) {
				t.Fatalf("preview repeated membership search: %s", log)
			}
		})
	}
}

func TestContentPreviewMemoRequiresExactLiveScope(t *testing.T) {
	project := t.TempDir()
	entries := []discovery.Entry{{RelPath: "keep.go"}, {RelPath: "drop.go"}}
	base := command.ExecutionScope{
		Contains: "needle",
		Stages:   []command.Stage{{Kind: command.StageContains, Values: []string{"needle"}}},
	}
	for _, tc := range []struct {
		name    string
		pattern string
		scope   command.ExecutionScope
	}{
		{"prefix-is-not-completed", "need", base},
		{"different-query", "other", base},
		{"empty-query", "", command.ExecutionScope{Stages: []command.Stage{{Kind: command.StageContains, Values: []string{""}}}}},
		{"other-stage", "needle", command.ExecutionScope{Stages: []command.Stage{{Kind: command.StageOnly, Values: []string{"needle"}}}}},
		{"additional-filter", "needle", command.ExecutionScope{Contains: "needle", Stages: []command.Stage{
			{Kind: command.StageOnly, Values: []string{"*.go"}},
			{Kind: command.StageContains, Values: []string{"needle"}},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := prediscoveredCommandConfig{
				Invocation:       command.Invocation{WorkingDir: project},
				ContentMatchMemo: &contentMatchMemo{Pattern: tc.pattern, AbsPaths: []string{filepath.Join(project, "keep.go")}},
			}
			if _, hit := contentPreviewEntriesFromMemo(cfg, tc.scope, entries); hit {
				t.Fatal("ineligible memo replaced canonical scope evaluation")
			}
		})
	}
	cfg := prediscoveredCommandConfig{
		Invocation:       command.Invocation{WorkingDir: project},
		ContentMatchMemo: &contentMatchMemo{Pattern: "needle"},
	}
	if got, hit := contentPreviewEntriesFromMemo(cfg, base, entries); !hit || len(got) != 0 {
		t.Fatalf("completed empty result must remain empty: hit=%v entries=%v", hit, got)
	}
}
