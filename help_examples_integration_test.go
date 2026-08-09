package catclip

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
)

type helpExampleExpectation struct {
	contains []string
	excludes []string
	ordered  []string
}

func TestHelpExamplesExecuteAgainstDeclaredProject(t *testing.T) {
	project := copyHelpExampleFixture(t)
	prepareHelpExampleFixture(t, project)

	examples, err := cli.RegisteredHelpExamples()
	if err != nil {
		t.Fatal(err)
	}
	expectations := helpExampleExpectations()

	for _, example := range examples {
		example := example
		if example.Kind == cli.HelpExampleInteractive || example.Command == "catclip --hiss" || example.Command == "catclip --help" {
			continue
		}
		t.Run(example.ID, func(t *testing.T) {
			args, hasCatclip, err := example.CatclipArgs()
			if err != nil {
				t.Fatal(err)
			}
			if !hasCatclip {
				return
			}

			if example.Kind == cli.HelpExampleStdin {
				setTestPipeStdin(t, "src/App.tsx\r\nsrc/components/Button.tsx\r\n")
			}
			if !containsArg(args, "--headless") {
				args = append(args, "--headless")
			}

			cfg := parseInProject(t, project, args)
			var stdout, stderr bytes.Buffer
			runErr := run(cfg, &stdout, &stderr)
			if example.Kind == cli.HelpExampleExpectedError {
				if runErr == nil {
					t.Fatalf("expected documented error, got success\ncommand: %s\nstdout:\n%s", example.Command, stdout.String())
				}
				return
			}
			if runErr != nil {
				t.Fatalf("documented example failed: %v\ncommand: %s\nstderr:\n%s", runErr, example.Command, stderr.String())
			}

			output := stdout.String()
			if output == "" {
				t.Fatalf("documented example produced no observable output: %s", example.Command)
			}
			for _, want := range expectations[example.ID].contains {
				if !strings.Contains(output, want) {
					t.Fatalf("output missing %q\ncommand: %s\noutput:\n%s", want, example.Command, output)
				}
			}
			for _, unwanted := range expectations[example.ID].excludes {
				if strings.Contains(output, unwanted) {
					t.Fatalf("output unexpectedly contains %q\ncommand: %s\noutput:\n%s", unwanted, example.Command, output)
				}
			}
			last := -1
			for _, want := range expectations[example.ID].ordered {
				index := strings.Index(output, want)
				if index < 0 {
					t.Fatalf("ordered output missing %q\ncommand: %s\noutput:\n%s", want, example.Command, output)
				}
				if index <= last {
					t.Fatalf("output order mismatch at %q\ncommand: %s\nwant order: %#v\noutput:\n%s", want, example.Command, expectations[example.ID].ordered, output)
				}
				last = index
			}
		})
	}
}

func containsArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func copyHelpExampleFixture(t *testing.T) string {
	t.Helper()

	source := filepath.Join("testdata", "help-react-go-project")
	destination := filepath.Join(t.TempDir(), "help-react-go-project")
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatalf("copy help fixture: %v", err)
	}
	return destination
}

func prepareHelpExampleFixture(t *testing.T, project string) {
	t.Helper()

	// These files are intentionally ignored either by this repository or by
	// the fixture project itself. Construct them here so a clean checkout does
	// not depend on force-added ignored test data.
	for rel, content := range map[string]string{
		".gitignore":                    "dist/\nnode_modules/\nsrc/generated/\n",
		"docs/api.md":                   "# API\n\nThe API serves profile data.\n",
		"docs/User Guide.md":            "# User Guide\n\nOpen the profile page and select the Button.\n",
		"docs/architecture/overview.md": "# Architecture\n\nThe React application calls the Go API.\n",
		"dist/index.html":               "<div id=\"root\"></div>\n",
		"dist/assets/index.js":          "console.log(\"built\");\n",
		"dist/assets/app.js":            "console.log(\"app\");\n",
		".env.local":                    "API_URL=https://example.test\n",
		"node_modules/react/index.js":   "export const createElement = () => null;\n",
		"src/generated/client.ts":       "export const generated = true;\n",
	} {
		writeProjectFile(t, project, rel, content)
	}

	configRoot := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", filepath.Dir(configRoot))
	hissPath := discovery.GlobalHissPath()
	if err := os.MkdirAll(filepath.Dir(hissPath), 0o755); err != nil {
		t.Fatalf("create help fixture config: %v", err)
	}
	if err := os.WriteFile(hissPath, []byte(".env.example\n"), 0o644); err != nil {
		t.Fatalf("write help fixture hiss: %v", err)
	}

	var longHandler strings.Builder
	longHandler.WriteString("package handler\n\nfunc LoadUser() string {\n\treturn \"user\"\n}\n")
	for line := 6; line <= 500; line++ {
		fmt.Fprintf(&longHandler, "// fixture line %03d\n", line)
	}
	writeProjectFile(t, project, "internal/handler/user.go", longHandler.String())

	untrackedPath := filepath.Join(project, "src", "pages", "Profile.tsx")
	untrackedContent, err := os.ReadFile(untrackedPath)
	if err != nil {
		t.Fatalf("read untracked fixture: %v", err)
	}
	if err := os.Remove(untrackedPath); err != nil {
		t.Fatalf("remove pre-commit untracked fixture: %v", err)
	}

	initGitRepo(t, project)
	writeProjectFile(t, project, "src/pages/Profile.tsx", string(untrackedContent))
	buttonPath := filepath.Join(project, "src", "components", "Button.tsx")
	button, err := os.ReadFile(buttonPath)
	if err != nil {
		t.Fatalf("read Button fixture: %v", err)
	}
	if err := os.WriteFile(buttonPath, append(button, []byte("// modified for git examples\n")...), 0o644); err != nil {
		t.Fatalf("modify Button fixture: %v", err)
	}

	base := time.Unix(1_700_000_000, 0)
	if err := filepath.WalkDir(project, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		return os.Chtimes(path, base, base)
	}); err != nil {
		t.Fatalf("normalize fixture mtimes: %v", err)
	}
	ordered := []string{
		"src/main.tsx",
		"src/App.tsx",
		"src/components/Button.test.tsx",
		"src/components/Button.tsx",
		"src/pages/Home.tsx",
		"src/pages/Profile.tsx",
		"docs/api.md",
		"docs/architecture/overview.md",
	}
	for i, rel := range ordered {
		stamp := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(project, filepath.FromSlash(rel)), stamp, stamp); err != nil {
			t.Fatalf("set fixture mtime for %s: %v", rel, err)
		}
	}
}

func helpExampleExpectations() map[string]helpExampleExpectation {
	return map[string]helpExampleExpectation{
		"target.src":                          {contains: []string{"src/App.tsx"}, excludes: []string{"docs/api.md"}},
		"target.button-file":                  {contains: []string{"src/components/Button.tsx"}, excludes: []string{"Button.test.tsx"}},
		"target.button-exact-path":            {contains: []string{"src/components/Button.tsx"}, excludes: []string{"Button.test.tsx"}},
		"target.all-go-double-quoted":         {contains: []string{"cmd/api/main.go", "internal/handler/user.go"}, excludes: []string{"src/App.tsx"}},
		"target.all-tsx":                      {contains: []string{"src/App.tsx", "src/pages/Home.tsx"}, excludes: []string{"cmd/api/main.go"}},
		"target.direct-src-tsx":               {contains: []string{"src/App.tsx", "src/main.tsx"}, excludes: []string{"src/pages/Home.tsx"}},
		"target.direct-src-tsx-double-quoted": {contains: []string{"src/App.tsx", "src/main.tsx"}, excludes: []string{"src/pages/Home.tsx"}},
		"filter.exclude-css":                  {contains: []string{"src/App.tsx"}, excludes: []string{"src/styles/globals.css"}},
		"filter.only-handler":                 {contains: []string{"internal/handler/user.go"}, excludes: []string{"internal/store/store.go"}},
		"filter.exclude-handler":              {contains: []string{"internal/store/store.go"}, excludes: []string{"internal/handler/user.go"}},
		"contains.todo":                       {contains: []string{"src/hooks/useAuth.ts"}, excludes: []string{"src/App.tsx"}},
		"not-contains.todo":                   {contains: []string{"src/App.tsx"}, excludes: []string{"src/hooks/useAuth.ts"}},
		"snippet.use-auth-smart":              {contains: []string{"src/hooks/useAuth.ts", "func"}},
		"snippet.use-auth-context":            {contains: []string{"src/hooks/useAuth.ts", `lines="`}},
		"ignored.generated-target":            {contains: []string{"src/generated/client.ts"}, excludes: []string{"src/App.tsx"}},
		"ignored.generated-paths":             {contains: []string{"src/generated/client.ts"}, excludes: []string{"src/App.tsx"}},
		"ignored.generated-file":              {contains: []string{"generated = true"}, excludes: []string{"<file"}},
		"git.changed-src":                     {contains: []string{"src/components/Button.tsx", "src/pages/Profile.tsx"}, excludes: []string{"src/App.tsx"}},
		"git.unstaged-tsx-diff":               {contains: []string{`type="unstaged-diff"`, "Button.tsx"}},
		"template.changed-diff":               {contains: []string{"Button.tsx", "Profile.tsx"}},
		"stdin.git-diff-short":                {contains: []string{"src/App.tsx", "src/components/Button.tsx"}, excludes: []string{"src/pages/Home.tsx"}},
		"stdin.git-diff-headless":             {contains: []string{"src/App.tsx", "src/components/Button.tsx"}, excludes: []string{"src/pages/Home.tsx"}},
		"discouraged.cross-target-filter":     {contains: []string{"src/App.tsx"}, excludes: []string{"docs/api.md"}},
		"pipeline.combined": {
			ordered: []string{
				`<file path="src/pages/Home.tsx">`,
				`<file path="src/components/Button.tsx">`,
				`<file path="src/App.tsx">`,
			},
		},
		"recent.src-three": {
			ordered: []string{
				`<file path="src/pages/Profile.tsx">`,
				`<file path="src/pages/Home.tsx">`,
				`<file path="src/components/Button.tsx">`,
			},
		},
		"recent.combined-targets": {
			ordered: []string{
				`<file path="docs/architecture/overview.md">`,
				`<file path="docs/api.md">`,
				`<file path="src/pages/Profile.tsx">`,
				`<file path="src/pages/Home.tsx">`,
				`<file path="src/components/Button.tsx">`,
			},
		},
		"then.separate-recent": {
			ordered: []string{
				`<file path="src/pages/Profile.tsx">`,
				`<file path="src/pages/Home.tsx">`,
				`<file path="src/components/Button.tsx">`,
				`<file path="src/components/Button.test.tsx">`,
				`<file path="src/App.tsx">`,
				`<file path="docs/architecture/overview.md">`,
				`<file path="docs/api.md">`,
			},
		},
	}
}
