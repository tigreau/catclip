// Package transporttest exercises the real fzf executor, not a reimplementation
// of its quoting. --sync load actions accept before drawing the UI, but Linux
// fzf still needs a controlling terminal (provided by the CI step). These
// shell-boundary checks do not replace interactive UI tests.
package transporttest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
)

type probeResult struct {
	Args       []string
	Selection  string
	Error      string
	WorkingDir string
}

func TestMain(m *testing.M) {
	if resultPath := os.Getenv("CATCLIP_FZF_ARGV_PROBE"); resultPath != "" {
		args, contextErr := picker.RestoreCommandContext(os.Args[1:])
		result := probeResult{Args: args}
		if contextErr != nil {
			result.Error = contextErr.Error()
		}
		result.WorkingDir, _ = os.Getwd()
		for i, arg := range result.Args {
			if arg == "--internal-target-selection" && i+1 < len(result.Args) {
				data, err := os.ReadFile(picker.SelectionFilePath(result.Args[i+1]))
				result.Selection = string(data)
				if err != nil {
					result.Error = err.Error()
				}
			}
		}
		data, err := json.Marshal(result)
		if err == nil {
			err = os.WriteFile(resultPath, data, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestFzfShellTransport(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_FZF_TRANSPORT_TESTS") != "1" {
		t.Skip("real-fzf shell matrix is enabled explicitly by CI")
	}
	bin := os.Getenv("CATCLIP_FZF")
	if !filepath.IsAbs(bin) {
		t.Fatal("CATCLIP_FZF must name the absolute pinned fzf executable")
	}
	version, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("required fzf unavailable: %v: %s", err, version)
	}
	versionFields := strings.Fields(string(version))
	if len(versionFields) == 0 {
		t.Fatal("fzf returned an empty version")
	}
	if want := os.Getenv("FZF_VERSION"); want != "" && versionFields[0] != want {
		t.Fatalf("fzf version = %q, want %s", version, want)
	}
	shellName := os.Getenv("CATCLIP_TEST_FZF_SHELL")
	if shellName == "" {
		t.Fatal("CATCLIP_TEST_FZF_SHELL is required; do not infer it from the workflow shell")
	}
	shell, err := exec.LookPath(shellName)
	if err != nil {
		t.Fatalf("required shell %q unavailable: %v", shellName, err)
	}
	t.Logf("native OS=%s; fzf=%s; SHELL=%s", runtime.GOOS, strings.TrimSpace(string(version)), shell)
	// Exercise fzf's automatic shell-specific executor. A nonempty --with-shell
	// follows a different branch in 0.74.1 and must not be silently substituted.
	t.Setenv("SHELL", shell)
	t.Setenv("FZF_DEFAULT_OPTS", "")
	t.Setenv("FZF_DEFAULT_OPTS_FILE", "")
	t.Setenv("CATCLIP_EXPAND", "must-not-expand")

	for _, name := range []string{"spaces", "literal_metacharacters", "literal_placeholders", "temp_root_only"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			component := "launcher with spaces"
			if name == "literal_metacharacters" {
				component += " 'é' & $CATCLIP_EXPAND %CATCLIP_EXPAND% !CATCLIP_EXPAND!"
			} else if name == "literal_placeholders" {
				component += " {q} {2} {+f}"
			}
			launcherDir := filepath.Join(root, component)
			if err := os.Mkdir(launcherDir, 0o700); err != nil {
				t.Fatal(err)
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(self)
			if err != nil {
				t.Fatal(err)
			}
			probe := filepath.Join(launcherDir, "catclip-probe")
			if runtime.GOOS == "windows" {
				probe += ".exe"
			}
			if err := os.WriteFile(probe, data, 0o700); err != nil {
				t.Fatal(err)
			}
			// Use actual production builders. Only relocate the executable so its
			// fixed path contains the characters under test; argv is recorded by
			// TestMain above before the test flag parser runs.
			relocate := func(command string) string {
				prefix := picker.CommandExecutable(self)
				if !strings.HasPrefix(command, prefix+" ") {
					t.Fatalf("builder did not produce expected executable prefix: %s", command)
				}
				return picker.CommandExecutable(probe) + strings.TrimPrefix(command, prefix)
			}
			checkpoint := filepath.Join(launcherDir, "scope.json")
			path := "src/space 'é' & $CATCLIP_EXPAND %CATCLIP_EXPAND% !literal! [x].go"
			query := "--changed-diff | dollar $CATCLIP_EXPAND `literal` 'quote' \"double\" \\ end"
			t.Run("content_query_and_focus", func(t *testing.T) {
				command := relocate(discovery.FzfContentPreviewCommand("--contains", checkpoint))
				got := runTransport(t, bin, command, query, []string{"label\tkey\t" + path}, false)
				want := []string{"--quiet", "--internal-file-preview", "--internal-searching-preview", "--internal-file-path", path,
					"--internal-tree-target", "label", "--internal-prediscovered", checkpoint, "--contains", query}
				if !reflect.DeepEqual(got.Args, want) {
					t.Fatalf("argv changed:\ngot  %q\nwant %q", got.Args, want)
				}
			})
			t.Run("empty_query", func(t *testing.T) {
				command := relocate(discovery.FzfContentSearchingPreviewCommand("--snippet"))
				got := runTransport(t, bin, command, "", []string{"label\tkey\t" + path}, false)
				want := []string{"--quiet", "--internal-file-preview", "--internal-searching-preview", "--internal-file-path", "", "--snippet", ""}
				if !reflect.DeepEqual(got.Args, want) {
					t.Fatalf("empty arguments changed: got %q, want %q", got.Args, want)
				}
			})
			t.Run("large_target_selection", func(t *testing.T) {
				var matches []discovery.TargetMatch
				for i := 0; i < 10000; i++ {
					matches = append(matches, discovery.TargetMatch{Path: fmt.Sprintf("selected/%05d %s", i, path), Kind: "file", State: "text"})
				}
				rows, _ := discovery.TargetMatchLabels(matches)
				command := relocate(discovery.FzfPreviewCommandWithInventory(checkpoint))
				got := runTransport(t, bin, command, "", rows, true)
				// fzf orders selected items by selection time, not input position.
				// Check exact row membership including multiplicity; select-all may
				// assign equal timestamps on Windows and return tied rows in any order.
				selected := strings.Split(strings.TrimSuffix(got.Selection, "\n"), "\n")
				wantRows := slices.Clone(rows)
				slices.Sort(selected)
				slices.Sort(wantRows)
				if !strings.HasSuffix(got.Selection, "\n") || !slices.Equal(selected, wantRows) {
					t.Fatalf("selection lost, duplicated, or changed rows: got %d rows (%d bytes), want %d rows (%d bytes)", len(selected), len(got.Selection), len(rows), len(strings.Join(rows, "\n"))+1)
				}
				if len(got.Args) != 12 {
					t.Fatalf("target argv changed or selection expanded: %q", got.Args)
				}
				if runtime.GOOS != "windows" && (filepath.IsAbs(got.Args[11]) || filepath.Base(got.Args[11]) != filepath.Clean(got.Args[11])) {
					t.Fatalf("fzf must pass only a safe relative filename, got %q", got.Args[11])
				}
				want := []string{"--quiet", "--internal-tree-preview", "--internal-target-inventory", checkpoint,
					"--internal-tree-target", matches[0].Path, "--internal-tree-kind", "file", "--internal-tree-state", "text",
					"--internal-target-selection", got.Args[11]}
				if !reflect.DeepEqual(got.Args, want) {
					t.Fatalf("target argv changed: got %q, want %q", got.Args, want)
				}
			})
		})
	}
}

func runTransport(t *testing.T, bin, command, query string, rows []string, selectAll bool) probeResult {
	t.Helper()
	dir := t.TempDir()
	// Verify the raw file-placeholder exception with an actual spaced temp path.
	tempDir := filepath.Join(dir, "fzf temp with spaces")
	if strings.Contains(t.Name(), "/temp_root_only/") {
		tempDir = filepath.Join(dir, "fzf temp 'é' $CATCLIP_EXPAND %CATCLIP_EXPAND% !bang! {q}")
	}
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, tempDir)
	}
	resultPath := filepath.Join(dir, "received.json")
	actions := "load:"
	if selectAll {
		actions += "select-all+"
	}
	actions += "execute-silent(" + command + ")+accept"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--sync", "--disabled", "--multi", "--no-sort", "--delimiter", "\t", "--query", query, "--bind", actions)
	cmd.Env = append(os.Environ(), "CATCLIP_FZF_ARGV_PROBE="+resultPath)
	cmd.Stdin = strings.NewReader(strings.Join(rows, "\n") + "\n")
	cmd.WaitDelay = 3 * time.Second
	wantDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := picker.PrepareCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	configureTransportCancellation(t, cmd)
	out, runErr := cmd.CombinedOutput()
	data, readErr := os.ReadFile(resultPath)
	if artifacts := os.Getenv("CATCLIP_TRANSPORT_ARTIFACTS"); artifacts != "" {
		artifactDir := filepath.Join(artifacts, strings.ReplaceAll(t.Name(), "/", "_"))
		if err := os.MkdirAll(artifactDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string][]byte{"fzf-output.txt": out, "received.json": data, "command.txt": []byte(command)} {
			if err := os.WriteFile(filepath.Join(artifactDir, name), body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if runErr != nil || readErr != nil {
		// The complete output is retained in CI artifacts. Do not flood the
		// job log with all 10,000 selected rows when the helper fails to start.
		if len(out) > 4096 {
			out = append(out[:4096:4096], []byte("\n[output truncated; see fzf-output.txt artifact]\n")...)
		}
		t.Fatalf("fzf handoff failed: run=%v result=%v timeout=%v\n%s", runErr, readErr, ctx.Err(), out)
	}
	var got probeResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" {
		t.Fatalf("helper could not read fzf selection file: %s", got.Error)
	}
	// Canonicalize aliases on Windows (short paths) and macOS (/var).
	wantInfo, err := os.Stat(wantDir)
	if err != nil {
		t.Fatal(err)
	}
	gotInfo, err := os.Stat(got.WorkingDir)
	if err != nil || !os.SameFile(wantInfo, gotInfo) {
		t.Fatalf("helper cwd = %q, want %q: %v", got.WorkingDir, wantDir, err)
	}
	return got
}
