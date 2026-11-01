package catclip

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunBinaryDescriptionsAndRawBytes(t *testing.T) {
	const binary = "PRIVATE\x00BYTES"
	project := setupTestProject(t, map[string]string{"blob.fixture": binary, "note.txt": "ordinary text\n"})
	for _, tc := range []struct {
		name string
		args []string
		raw  bool
	}{
		{"direct", []string{"blob.fixture"}, false},
		{"mixed", []string{".", "-b"}, false},
		{"then", []string{"note.txt", "--then", "blob.fixture"}, false},
		{"raw", []string{"blob.fixture", "--raw"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(tc.args, "--headless", "--print")
			if err := run(parseInProject(t, project, args), &stdout, &stderr); err != nil {
				t.Fatalf("%v: %s", err, stderr.String())
			}
			if tc.raw {
				if stdout.String() != binary {
					t.Fatalf("raw bytes changed: %q", stdout.String())
				}
				return
			}
			if strings.Contains(stdout.String(), "PRIVATE") || !strings.Contains(stdout.String(), `type="binary"`) || !strings.Contains(stdout.String(), "Binary contents omitted.") {
				t.Fatalf("unexpected binary output: %q", stdout.String())
			}
			if (tc.name == "mixed" || tc.name == "then") && !strings.Contains(stdout.String(), "ordinary text") {
				t.Fatal("lost text sibling")
			}
		})
	}
}
