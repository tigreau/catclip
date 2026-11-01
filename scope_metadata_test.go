package catclip

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/search"
)

func TestScopeMetadataHeadlessComposition(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/auth.go": "alpha\nbravo\ncharlie\n", "src/helper.go": "helper\n"})
	for _, tc := range []struct {
		name          string
		args          []string
		metadataFirst bool
		units         int
	}{
		{"metadata then snippet", []string{"src", "--metadata", "--then", "src/auth.go", "--snippet", "alpha", "0"}, true, 1},
		{"snippet then metadata", []string{"src/auth.go", "--snippet", "alpha", "0", "--then", "src", "--metadata"}, false, 1},
		{"metadata then lines", []string{"src", "--metadata", "--then", "src/auth.go", "--lines", "1", "1"}, true, 1},
		{"empty snippet preserves metadata", []string{"src", "--metadata", "--then", "src/auth.go", "--snippet", "NEVER_MATCH"}, true, 0},
		{"empty range preserves metadata", []string{"src", "--metadata", "--then", "src/auth.go", "--lines", "99", "100"}, true, 0},
		{"adjacent metadata dedup", []string{"src", "--metadata", "--then", "src/auth.go", "--metadata"}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseInProject(t, project, append(tc.args, "--headless"))
			var stdout, stderr bytes.Buffer
			if err := run(cfg, &stdout, &stderr); err != nil {
				t.Fatalf("%v\n%s", err, stderr.String())
			}
			text := stdout.String()
			if strings.Count(text, "Root:") != 1 || !strings.Contains(text, "src/helper.go") || strings.Count(text, "<file ") != tc.units {
				t.Fatalf("unexpected composition: %s", text)
			}
			if tc.units > 0 && (strings.Index(text, "Root:") < strings.Index(text, "<file ")) != tc.metadataFirst {
				t.Fatal("scope order lost")
			}
			if strings.Contains(text, "bravo") || strings.Contains(text+stderr.String(), "\x1b") || strings.Contains(stderr.String(), "Proceed?") {
				t.Fatalf("unexpected fallback, color or prompt: %s\n%s", text, stderr.String())
			}
			if strings.Contains(stderr.String(), "full files omitted") {
				t.Fatal("metadata incorrectly treated as whole-file fallback")
			}
		})
	}
}

func TestScopeMetadataAlongsidePatchAndFullBody(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	project := setupTestProject(t, map[string]string{"auth.go": "before\n"})
	initGitRepo(t, project)
	writeProjectFile(t, project, "auth.go", "after\n")
	for _, mode := range []string{"", "--unstaged-diff"} {
		args := []string{"auth.go", "--metadata", "--then", "auth.go"}
		if mode != "" {
			args = append(args, mode)
		}
		cfg := parseInProject(t, project, append(args, "--headless"))
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("%s: %v %s", mode, err, stderr.String())
		}
		text := stdout.String()
		if !strings.Contains(text, "PATH") || strings.Count(text, "<file ") != 1 || !strings.Contains(text, "after") {
			t.Fatalf("lost metadata or source: %s", text)
		}
		if mode != "" && !strings.Contains(text, `type="unstaged-diff"`) {
			t.Fatal(text)
		}
	}
}

func TestScopeMetadataNeverEmitSkipsReportMaterialization(t *testing.T) {
	project := setupTestProject(t, map[string]string{"auth.go": "alpha\nbravo\n"})
	scans := 0
	restore := search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) {
		if event.Context.Reason == search.MembershipReasonMetadataIgnoreTrace {
			scans++
		}
	})
	defer restore()
	cfg := parseInProject(t, project, []string{"auth.go", "--metadata", "--then", "auth.go", "--lines", "1", "1", "--no"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if scans != 0 || strings.Contains(stdout.String(), "Root:") || strings.Contains(stdout.String(), "<file ") {
		t.Fatalf("never-emit materialized metadata: scans=%d %s", scans, stdout.String())
	}
}
