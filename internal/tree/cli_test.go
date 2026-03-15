package tree

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// runCLIForTest exercises the standalone catclip-tree surface with a neutral
// palette resolver so these tests stay independent from root catclip styling.
func runCLIForTest(t *testing.T, args []string, stdin string) (string, string, error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := RunCLI(args, strings.NewReader(stdin), &stdout, &stderr, CLIConfig{
		Version: "test",
		ResolvePalette: func(mode string, w io.Writer) (Palette, error) {
			return Palette{}, nil
		},
	})
	return stdout.String(), stderr.String(), err
}

func TestRunCLIRendersRichTree(t *testing.T) {
	payload := strings.Join([]string{
		`{"type":"meta","version":1,"mode":"tree","root":"src/components"}`,
		`{"type":"entry","path":"src/components/Editor/Editor.tsx","kind":"file","size":73216,"git":"M"}`,
		`{"type":"entry","path":"src/components/Editor/Toolbar.tsx","kind":"file","size":3276}`,
		`{"type":"summary","count":2,"bytes":76492,"human_size":"74.74KB","tokens":19123,"file_word":"files"}`,
		"",
	}, "\n")

	stdout, stderr, err := runCLIForTest(t, nil, payload)
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if !strings.Contains(stdout, "Editor.tsx (71.5KB) [M]") {
		t.Fatalf("expected rich file line, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Count:") || !strings.Contains(stdout, "Tokens:") {
		t.Fatalf("expected summary section, got:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got:\n%s", stderr)
	}
}

func TestRunCLIBareHidesMetadata(t *testing.T) {
	payload := strings.Join([]string{
		`{"type":"meta","version":1,"mode":"tree"}`,
		`{"type":"target","path":"src/components/Editor","kind":"dir","state":"ok"}`,
		`{"type":"entry","path":"src/components/Editor/Editor.tsx","kind":"file","size":73216,"git":"M","target_root":"src/components/Editor"}`,
		`{"type":"summary","count":1,"bytes":73216,"human_size":"71.53KB","tokens":18311,"file_word":"file"}`,
		"",
	}, "\n")

	stdout, _, err := runCLIForTest(t, []string{"--bare"}, payload)
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if strings.Contains(stdout, "(71.5KB)") {
		t.Fatalf("expected bare output to omit size, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "[M]") {
		t.Fatalf("expected bare output to omit git status, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "Count:") {
		t.Fatalf("expected bare output to omit summary, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "(src/components/Editor/)") {
		t.Fatalf("expected bare output to omit right-side path hints, got:\n%s", stdout)
	}
}

func TestRunCLIBareStartsAtTargetParentContext(t *testing.T) {
	payload := strings.Join([]string{
		`{"type":"meta","version":1,"mode":"tree"}`,
		`{"type":"target","path":"src/vs/platform/launch","kind":"dir","state":"ok"}`,
		`{"type":"entry","path":"src/vs/platform/launch/adapter/debug.ts","kind":"file","target_root":"src/vs/platform/launch"}`,
		"",
	}, "\n")

	stdout, _, err := runCLIForTest(t, []string{"--bare"}, payload)
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if !strings.Contains(stdout, "platform/") {
		t.Fatalf("expected bare output to start from the target parent context, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "launch/") {
		t.Fatalf("expected bare output to keep the hovered target directory, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "src/\n") || strings.Contains(stdout, "vs/\n") {
		t.Fatalf("expected bare output to trim ancestors above the target parent, got:\n%s", stdout)
	}
}

func TestRunCLIFilePreviewTruncatesToMaxLines(t *testing.T) {
	payload := strings.Join([]string{
		`{"type":"meta","version":1,"mode":"file"}`,
		`{"type":"file","path":"src/app.ts","content":"one\ntwo\nthree\n"}`,
		"",
	}, "\n")

	stdout, _, err := runCLIForTest(t, []string{"--max-lines", "2"}, payload)
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if !strings.Contains(stdout, "src/app.ts") {
		t.Fatalf("expected file preview heading, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 │ one") || !strings.Contains(stdout, "2 │ two") {
		t.Fatalf("expected numbered lines, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "… truncated") {
		t.Fatalf("expected truncation marker, got:\n%s", stdout)
	}
}

func TestRunCLIBareEmptyPayloadShowsFriendlyMessage(t *testing.T) {
	stdout, stderr, err := runCLIForTest(t, []string{"--bare"}, "")
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if !strings.Contains(stdout, "No previewable text files here.") {
		t.Fatalf("expected bare empty-state message, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Directory may be empty or contain only binary files.") {
		t.Fatalf("expected bare empty-state detail, got:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got:\n%s", stderr)
	}
}

func TestRunCLIBareRendersEmptyDirectoryTargetState(t *testing.T) {
	payload := strings.Join([]string{
		`{"type":"meta","version":1,"mode":"tree"}`,
		`{"type":"target","path":"src/cache","kind":"dir","state":"empty"}`,
		"",
	}, "\n")

	stdout, _, err := runCLIForTest(t, []string{"--bare"}, payload)
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if !strings.Contains(stdout, "cache/") {
		t.Fatalf("expected bare empty directory label, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "empty directory") {
		t.Fatalf("expected empty directory state, got:\n%s", stdout)
	}
}

func TestRunCLIBareRendersNoTextDirectoryTargetState(t *testing.T) {
	payload := strings.Join([]string{
		`{"type":"meta","version":1,"mode":"tree"}`,
		`{"type":"target","path":"src/.git/objects","kind":"dir","state":"no_text_children"}`,
		"",
	}, "\n")

	stdout, _, err := runCLIForTest(t, []string{"--bare"}, payload)
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if !strings.Contains(stdout, "objects/") {
		t.Fatalf("expected bare no-text directory label, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no previewable text files") {
		t.Fatalf("expected no-text directory state, got:\n%s", stdout)
	}
}

func TestRunCLIRichEmptyPayloadStillErrors(t *testing.T) {
	_, _, err := runCLIForTest(t, nil, "")
	if !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("expected empty tree payload error, got: %v", err)
	}
}
