package discovery

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTargetPickerFzfFieldsDisplayMetadataAndMatchPath(t *testing.T) {
	fzf, err := FuzzyResolverBinary()
	if err != nil {
		t.Skipf("fzf unavailable: %v", err)
	}

	lines, _ := TargetMatchLabels([]TargetMatch{
		{Path: "src/components/Login.tsx", Kind: "file"},
		{Path: "src/pages/LoginForm.tsx", Kind: "file"},
		{Path: "docs", Kind: "dir"},
	})

	filter := func(query string) (string, int) {
		t.Helper()
		cmd := exec.Command(
			fzf,
			"--ansi",
			"--delimiter", "\t",
			"--with-nth", "1,2",
			"--nth", "2",
			"--filter", query,
		)
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
		out, runErr := cmd.Output()
		if runErr == nil {
			return string(out), 0
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("fzf filter %q: %v", query, runErr)
		return "", -1
	}

	out, code := filter("lgn")
	if code != 0 {
		t.Fatalf("path query returned exit %d; target picker would be empty", code)
	}
	for _, want := range []string{"src/components/Login.tsx", "src/pages/LoginForm.tsx"} {
		if !strings.Contains(out, want) {
			t.Fatalf("path query missing %q:\n%s", want, out)
		}
	}

	out, code = filter("file")
	if code != 1 || out != "" {
		t.Fatalf("metadata-only query must not match target rows; exit=%d output=%q", code, out)
	}
}
