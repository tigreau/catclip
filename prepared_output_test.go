package catclip

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

func TestPrepareFileUnitsUsesSnippetBodyBytes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "alpha\nTODO hit\nomega\n\npadding padding padding\n",
	})

	entry := discovery.Entry{
		AbsPath:        filepath.Join(project, "src/app.ts"),
		RelPath:        "src/app.ts",
		Mode:           command.EntryModeSnippet,
		SnippetPattern: "TODO",
	}

	units, err := output.PrepareFileUnits(git.Context{}, []discovery.Entry{entry})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}

	wantBody := int64(len("alpha\nTODO hit\nomega\n"))
	if got := units[0].BodyBytes; got != wantBody {
		t.Fatalf("expected snippet body bytes %d, got %d", wantBody, got)
	}
	if !strings.Contains(string(units[0].Payload), `<file path="src/app.ts" lines="1-3">`) {
		t.Fatalf("expected prepared snippet payload, got:\n%s", string(units[0].Payload))
	}
	if want := []output.SnippetRange{{Start: 1, End: 3}}; !reflect.DeepEqual(units[0].SnippetRanges, want) {
		t.Fatalf("SnippetRanges = %v, want %v", units[0].SnippetRanges, want)
	}

	report, err := output.BuildReportForPlan(git.Context{}, output.BuildPlan(units), output.ReportOptions{})
	if err != nil {
		t.Fatalf("output.BuildReportForPlan returned error: %v", err)
	}
	if got := report.Sizes["src/app.ts"]; got != wantBody {
		t.Fatalf("expected report size %d, got %d", wantBody, got)
	}

	wantHuman, wantTokens := renderpkg.FormatSizeAndTokens(wantBody, len(units))
	if got := report.HumanSize; got != wantHuman {
		t.Fatalf("expected human size %q, got %q", wantHuman, got)
	}
	if got := report.Tokens; got != wantTokens {
		t.Fatalf("expected tokens %d, got %d", wantTokens, got)
	}
}

func TestPrepareFileUnitsNumericSnippetUsesRangesWithoutPayload(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": strings.Join([]string{
			"alpha",
			"TODO one",
			"beta",
			"gamma",
			"TODO two",
			"omega",
		}, "\n"),
	})

	entry := discovery.Entry{
		AbsPath:             filepath.Join(project, "src/app.ts"),
		RelPath:             "src/app.ts",
		Mode:                command.EntryModeSnippet,
		SnippetPattern:      "TODO",
		SnippetContextSet:   true,
		SnippetContextLines: 0,
	}

	units, err := output.PrepareFileUnits(git.Context{}, []discovery.Entry{entry})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}
	if len(units[0].Payload) != 0 {
		t.Fatalf("numeric snippet should not prebuild payload, got:\n%s", string(units[0].Payload))
	}
	wantRanges := []output.SnippetRange{{Start: 2, End: 2}, {Start: 5, End: 5}}
	if !reflect.DeepEqual(units[0].SnippetRanges, wantRanges) {
		t.Fatalf("SnippetRanges = %v, want %v", units[0].SnippetRanges, wantRanges)
	}
	if got, want := units[0].BodyBytes, int64(len("TODO one\nTODO two\n")); got != want {
		t.Fatalf("BodyBytes = %d, want %d", got, want)
	}

	var stdout bytes.Buffer
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(&stdout, output.EmitConfig{}, output.BuildPlan(units)); err != nil {
		t.Fatalf("output.WriteOutputPlanPayloadWithoutPrefetch returned error: %v", err)
	}
	want := "<file path=\"src/app.ts\" lines=\"2-2\">\nTODO one\n</file>\n\n" +
		"<file path=\"src/app.ts\" lines=\"5-5\">\nTODO two\n</file>\n\n"
	if got := stdout.String(); got != want {
		t.Fatalf("numeric snippet emit mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestPrepareNumericSnippetRangesMatchesContextResolution(t *testing.T) {
	lines := []string{
		"alpha",
		"TODO one",
		"beta",
		"gamma",
		"delta",
		"epsilon",
		"TODO two",
		"omega",
	}
	project := setupTestProject(t, map[string]string{
		"src/app.ts": strings.Join(lines, "\n"),
	})
	relPath := "src/app.ts"
	absPath := filepath.Join(project, filepath.FromSlash(relPath))
	matchedLines := []int{2, 7}
	entry := discovery.Entry{
		AbsPath:             absPath,
		RelPath:             relPath,
		Mode:                command.EntryModeSnippet,
		SnippetPattern:      "TODO",
		SnippetContextSet:   true,
		SnippetContextLines: 1,
	}

	ranges, bodyBytes, err := output.PrepareNumericSnippetRanges(entry, matchedLines)
	if err != nil {
		t.Fatalf("output.PrepareNumericSnippetRanges returned error: %v", err)
	}
	snapshot, err := output.LoadTextSnapshot(absPath, relPath)
	if err != nil {
		t.Fatalf("output.LoadTextSnapshot returned error: %v", err)
	}
	snippet, err := output.ResolveSnippetFromSnapshot(snapshot, matchedLines, output.SnippetOptions{Mode: output.SnippetBoundaryContext, Context: 1})
	if err != nil {
		t.Fatalf("output.ResolveSnippetFromSnapshot returned error: %v", err)
	}
	if !reflect.DeepEqual(ranges, snippet.Ranges) {
		t.Fatalf("ranges = %v, want %v", ranges, snippet.Ranges)
	}

	wantBody := strings.Join([]string{
		"alpha",
		"TODO one",
		"beta",
		"epsilon",
		"TODO two",
		"omega",
	}, "\n") + "\n"
	if got, want := bodyBytes, int64(len(wantBody)); got != want {
		t.Fatalf("bodyBytes = %d, want %d", got, want)
	}
}

func TestPrepareFileUnitsUsesDiffBodyBytes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"tracked.txt": "one\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "tracked.txt", "two\n")

	gitCtx := git.Detect(project)
	entry := discovery.Entry{
		AbsPath:          filepath.Join(project, "tracked.txt"),
		RelPath:          "tracked.txt",
		Mode:             command.EntryModeDiff,
		DiffWantUnstaged: true,
	}

	diffOutput, diffType, tracked, err := output.DiffEntryOutput(gitCtx, entry)
	if err != nil {
		t.Fatalf("output.DiffEntryOutput returned error: %v", err)
	}
	if !tracked {
		t.Fatal("expected tracked diff")
	}

	units, err := output.PrepareFileUnits(gitCtx, []discovery.Entry{entry})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}

	wantPayload, wantBody := output.BuildWrappedPayload("tracked.txt", diffType, []byte(diffOutput))
	if got := units[0].BodyBytes; got != wantBody {
		t.Fatalf("expected diff body bytes %d, got %d", wantBody, got)
	}
	if !bytes.Equal(units[0].Payload, wantPayload) {
		t.Fatalf("prepared diff payload mismatch:\nwant:\n%s\ngot:\n%s", string(wantPayload), string(units[0].Payload))
	}
}

func TestPrepareFileUnitsUsesKnownFullFileSizeWithoutStat(t *testing.T) {
	entry := discovery.Entry{
		AbsPath:   filepath.Join(t.TempDir(), "missing.txt"),
		RelPath:   "missing.txt",
		SizeBytes: 1234,
		SizeKnown: true,
	}

	units, err := output.PrepareFileUnits(git.Context{}, []discovery.Entry{entry})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}
	if got := units[0].BodyBytes; got != entry.SizeBytes {
		t.Fatalf("BodyBytes = %d, want known size %d", got, entry.SizeBytes)
	}
}

func TestPrepareFileUnitsPreservesKnownEmptyFileSize(t *testing.T) {
	entry := discovery.Entry{
		AbsPath:   filepath.Join(t.TempDir(), "missing-empty.txt"),
		RelPath:   "missing-empty.txt",
		SizeBytes: 0,
		SizeKnown: true,
	}

	units, err := output.PrepareFileUnits(git.Context{}, []discovery.Entry{entry})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}
	if got := units[0].BodyBytes; got != 0 {
		t.Fatalf("BodyBytes = %d, want known empty-file size 0", got)
	}
}

func TestPrepareFileUnitsStatsWhenSizeUnknown(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"known-later.txt": "abc\n",
	})

	units, err := output.PrepareFileUnits(git.Context{}, []discovery.Entry{{
		AbsPath: filepath.Join(project, "known-later.txt"),
		RelPath: "known-later.txt",
	}})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}
	if got, want := units[0].BodyBytes, int64(len("abc\n")); got != want {
		t.Fatalf("BodyBytes = %d, want stat-backed size %d", got, want)
	}
}

func TestBuildPreparedDiffPayloadPreservesUntrackedRawBytes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"tracked.txt": "tracked\n",
	})
	initGitRepo(t, project)

	raw := []byte("alpha\nbad:\xffz")
	absPath := filepath.Join(project, "new.txt")
	if err := os.WriteFile(absPath, raw, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	payload, bodyBytes, keep, err := output.BuildPreparedDiffPayload(git.Detect(project), discovery.Entry{
		AbsPath:          absPath,
		RelPath:          "new.txt",
		Mode:             command.EntryModeDiff,
		DiffWantUnstaged: true,
	})
	if err != nil {
		t.Fatalf("output.BuildPreparedDiffPayload returned error: %v", err)
	}
	if !keep {
		t.Fatal("expected untracked diff fallback payload")
	}

	wantPayload, wantBody := output.BuildWrappedPayload("new.txt", "untracked", raw)
	if got := bodyBytes; got != wantBody {
		t.Fatalf("BodyBytes = %d, want %d", got, wantBody)
	}
	if !bytes.Equal(payload, wantPayload) {
		t.Fatalf("payload mismatch:\nwant: %q\ngot:  %q", string(wantPayload), string(payload))
	}
}

func TestPrepareFileUnitsDropsEmptyTrackedDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"tracked.txt": "one\n",
	})
	initGitRepo(t, project)

	gitCtx := git.Detect(project)
	entry := discovery.Entry{
		AbsPath:          filepath.Join(project, "tracked.txt"),
		RelPath:          "tracked.txt",
		Mode:             command.EntryModeDiff,
		DiffWantUnstaged: true,
	}

	units, err := output.PrepareFileUnits(gitCtx, []discovery.Entry{entry})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if len(units) != 0 {
		t.Fatalf("expected empty diff to be dropped, got %d unit(s)", len(units))
	}
}

func TestEmitFullOutputUsesPreparedPayloadWithoutRebuilding(t *testing.T) {
	unit := output.PreparedFileUnit{
		Entry: discovery.Entry{
			AbsPath: "/does/not/matter",
			RelPath: "tracked.txt",
			Mode:    command.EntryModeDiff,
		},
		Payload:   []byte("<file path=\"tracked.txt\" type=\"diff\">\nbody\n</file>\n\n"),
		BodyBytes: int64(len("body\n")),
	}

	var stdout bytes.Buffer
	_, err := output.EmitFullOutput(output.EmitConfig{OutputMode: command.OutputModeStdout}, output.RuntimeEnvironment{}, []output.PreparedFileUnit{unit}, &stdout, platform.Palette{})
	if err != nil {
		t.Fatalf("output.EmitFullOutput returned error: %v", err)
	}
	if got, want := stdout.String(), string(unit.Payload); got != want {
		t.Fatalf("expected prepared payload to be emitted as-is\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestEmitFullOutputReadsFullFilesFromDiskAfterPrepare(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "one\n",
	})

	units, err := output.PrepareFileUnits(git.Context{}, []discovery.Entry{{
		AbsPath: filepath.Join(project, "a.txt"),
		RelPath: "a.txt",
	}})
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}
	if len(units[0].Payload) != 0 {
		t.Fatalf("expected full-file unit to keep Payload nil, got %q", string(units[0].Payload))
	}

	writeProjectFile(t, project, "a.txt", "two\n")
	t.Setenv("CATCLIP_READ_WORKERS", "1")

	var stdout bytes.Buffer
	_, err = output.EmitFullOutput(output.EmitConfig{OutputMode: command.OutputModeStdout}, output.RuntimeEnvironment{}, units, &stdout, platform.Palette{})
	if err != nil {
		t.Fatalf("output.EmitFullOutput returned error: %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "two\n") || strings.Contains(got, "one\n") {
		t.Fatalf("expected output.EmitFullOutput to read from disk after prepare, got:\n%s", got)
	}
}
