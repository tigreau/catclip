package catclip

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	treepkg "github.com/tigreau/catclip/internal/tree"
)

func TestPrepareFileUnitsUsesSnippetBodyBytes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "alpha\nTODO hit\nomega\n\npadding padding padding\n",
	})

	entry := fileEntry{
		AbsPath:        filepath.Join(project, "src/app.ts"),
		RelPath:        "src/app.ts",
		Mode:           entryModeSnippet,
		SnippetPattern: "TODO",
	}

	units, err := prepareFileUnits(gitContext{}, []fileEntry{entry})
	if err != nil {
		t.Fatalf("prepareFileUnits returned error: %v", err)
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

	report, err := buildOutputReportForPlan(runConfig{NoTree: true}, gitContext{}, buildOutputPlan(units), nil)
	if err != nil {
		t.Fatalf("buildOutputReportForPlan returned error: %v", err)
	}
	if got := report.sizes["src/app.ts"]; got != wantBody {
		t.Fatalf("expected report size %d, got %d", wantBody, got)
	}

	wantHuman, wantTokens := treepkg.FormatSizeAndTokens(wantBody, len(units))
	if got := report.humanSize; got != wantHuman {
		t.Fatalf("expected human size %q, got %q", wantHuman, got)
	}
	if got := report.tokens; got != wantTokens {
		t.Fatalf("expected tokens %d, got %d", wantTokens, got)
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

	gitCtx := detectGitContext(project)
	entry := fileEntry{
		AbsPath:          filepath.Join(project, "tracked.txt"),
		RelPath:          "tracked.txt",
		Mode:             entryModeDiff,
		DiffWantUnstaged: true,
	}

	diffOutput, diffType, tracked, err := diffEntryOutput(gitCtx, entry)
	if err != nil {
		t.Fatalf("diffEntryOutput returned error: %v", err)
	}
	if !tracked {
		t.Fatal("expected tracked diff")
	}

	units, err := prepareFileUnits(gitCtx, []fileEntry{entry})
	if err != nil {
		t.Fatalf("prepareFileUnits returned error: %v", err)
	}
	if got, want := len(units), 1; got != want {
		t.Fatalf("expected %d prepared unit, got %d", want, got)
	}

	wantPayload, wantBody := buildWrappedPayload("tracked.txt", diffType, []byte(diffOutput))
	if got := units[0].BodyBytes; got != wantBody {
		t.Fatalf("expected diff body bytes %d, got %d", wantBody, got)
	}
	if !bytes.Equal(units[0].Payload, wantPayload) {
		t.Fatalf("prepared diff payload mismatch:\nwant:\n%s\ngot:\n%s", string(wantPayload), string(units[0].Payload))
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

	payload, bodyBytes, keep, err := buildPreparedDiffPayload(detectGitContext(project), fileEntry{
		AbsPath:          absPath,
		RelPath:          "new.txt",
		Mode:             entryModeDiff,
		DiffWantUnstaged: true,
	})
	if err != nil {
		t.Fatalf("buildPreparedDiffPayload returned error: %v", err)
	}
	if !keep {
		t.Fatal("expected untracked diff fallback payload")
	}

	wantPayload, wantBody := buildWrappedPayload("new.txt", "untracked", raw)
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

	gitCtx := detectGitContext(project)
	entry := fileEntry{
		AbsPath:          filepath.Join(project, "tracked.txt"),
		RelPath:          "tracked.txt",
		Mode:             entryModeDiff,
		DiffWantUnstaged: true,
	}

	units, err := prepareFileUnits(gitCtx, []fileEntry{entry})
	if err != nil {
		t.Fatalf("prepareFileUnits returned error: %v", err)
	}
	if len(units) != 0 {
		t.Fatalf("expected empty diff to be dropped, got %d unit(s)", len(units))
	}
}

func TestEmitFullOutputUsesPreparedPayloadWithoutRebuilding(t *testing.T) {
	unit := preparedFileUnit{
		Entry: fileEntry{
			AbsPath: "/does/not/matter",
			RelPath: "tracked.txt",
			Mode:    entryModeDiff,
		},
		Payload:   []byte("<file path=\"tracked.txt\" type=\"diff\">\nbody\n</file>\n\n"),
		BodyBytes: int64(len("body\n")),
	}

	var stdout bytes.Buffer
	_, err := emitFullOutput(runConfig{OutputMode: outputModeStdout}, []preparedFileUnit{unit}, &stdout, colorPalette{})
	if err != nil {
		t.Fatalf("emitFullOutput returned error: %v", err)
	}
	if got, want := stdout.String(), string(unit.Payload); got != want {
		t.Fatalf("expected prepared payload to be emitted as-is\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestEmitFullOutputReadsFullFilesFromDiskAfterPrepare(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "one\n",
	})

	units, err := prepareFileUnits(gitContext{}, []fileEntry{{
		AbsPath: filepath.Join(project, "a.txt"),
		RelPath: "a.txt",
	}})
	if err != nil {
		t.Fatalf("prepareFileUnits returned error: %v", err)
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
	_, err = emitFullOutput(runConfig{OutputMode: outputModeStdout}, units, &stdout, colorPalette{})
	if err != nil {
		t.Fatalf("emitFullOutput returned error: %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "two\n") || strings.Contains(got, "one\n") {
		t.Fatalf("expected emitFullOutput to read from disk after prepare, got:\n%s", got)
	}
}
