package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/unpack"
)

// ChooseUnpackChanges shows every new and changed file selected, with a diff
// preview, and returns the indices the user kept. Esc cancels. The picker
// only collects choices; nothing is written here.
func ChooseUnpackChanges(p unpack.Plan) ([]int, error) {
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	stateDir, err := os.MkdirTemp("", "catclip-unpack-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stateDir)
	if err := writeUnpackPreviewState(stateDir, p); err != nil {
		return nil, err
	}
	lines := unpackPickerLines(p)
	changed, added := p.Counts()
	header := unpackPickerHeader(changed, added, p.Identical)
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:  "unpack> ",
		WithNth: "1,3",
		Nth:     "1,3",
		Header:  header,
		Multi:   true,
		NoSort:  true,
		// multi:refresh-preview (in the shared bindings) keeps the tree view
		// in step with every select/unselect.
		Bindings:       append(discovery.MultiSelectPickerBindings(), "load:select-all", unpackToggleBinding(stateDir)),
		PreviewCommand: unpackPreviewCommand(stateDir),
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, discovery.ErrSelectionCancelled
	}
	if err != nil {
		return nil, err
	}
	return unpackChosenIndices(result.Matches, len(p.Changes)), nil
}

// unpackPickerHeader is four rows: the counts, then three key-hint rows in the
// [Key] style of catclip's other multi-select pickers. The destination folder
// was confirmed by the root question just before.
func unpackPickerHeader(changed, added, identical int) string {
	return discovery.PickerHeader(
		fmt.Sprintf("%d changed · %d new · %d already identical", changed, added, identical),
		fmt.Sprintf("[Tab] unselect or select a file  [%s] unselect or select all", platform.MultiSelectToggleAllKey()),
		"[Ctrl-T] switch the preview between diff and tree",
		"[Enter] write the selected files  [Esc] cancel, nothing is written",
	)
}

// unpackPickerLines follows catclip's picker row convention: the second
// tab-separated field is the stable key fzf hands back (here the change
// index). The kind and path in fields 1 and 3 are what the user sees.
func unpackPickerLines(p unpack.Plan) []string {
	lines := make([]string, len(p.Changes))
	for i, c := range p.Changes {
		lines[i] = fmt.Sprintf("[%s]\t%d\t%s%s", c.Kind, i, c.Path, unpackRowNote(c))
	}
	return lines
}

// unpackChosenIndices turns picker keys back into change indices.
func unpackChosenIndices(keys []string, count int) []int {
	var chosen []int
	seen := map[int]bool{}
	for _, key := range keys {
		i, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || i < 0 || i >= count || seen[i] {
			continue
		}
		seen[i] = true
		chosen = append(chosen, i)
	}
	return chosen
}

// The preview helper runs once per focused file in a separate process. It
// gets the bundle copy from the state folder and reads only that one local
// file, so the preview reflects the project at hover time.
func writeUnpackPreviewState(dir string, p unpack.Plan) error {
	if p.Notes != "" {
		if err := os.WriteFile(unpackNotesPath(dir), []byte(p.Notes), 0o600); err != nil {
			return err
		}
	}
	var manifest strings.Builder
	for i, c := range p.Changes {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".body"), c.Body, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, "%d\t%s\t%s\t%s\n", i, c.Kind, c.Path, c.Abs)
	}
	if err := os.WriteFile(unpackModePath(dir), []byte("diff\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.tsv"), []byte(manifest.String()), 0o600)
}

func unpackModePath(stateDir string) string { return filepath.Join(stateDir, "mode") }

// unpackPreviewCommand passes the focused row's key ({2}) for the diff and
// fzf's selection file ({+f}) for the tree. A file, not arguments, keeps
// thousands of selected rows within Windows' command-line limit.
func unpackPreviewCommand(stateDir string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}
	return strings.Join([]string{
		picker.CommandExecutable(self),
		"--internal-unpack-preview", picker.CommandArg(stateDir), "{2}", picker.SelectionFilePlaceholder(),
	}, " ")
}

func unpackToggleBinding(stateDir string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "ctrl-t:ignore"
	}
	toggle := strings.Join([]string{picker.CommandExecutable(self), "--internal-unpack-toggle", picker.CommandArg(stateDir)}, " ")
	return "ctrl-t:execute-silent(" + toggle + ")+refresh-preview"
}

// RunInternalUnpackToggle switches the preview between diff and tree.
func RunInternalUnpackToggle(stateDir string) error {
	mode, err := os.ReadFile(unpackModePath(stateDir))
	if err != nil {
		return err
	}
	next := "tree\n"
	if strings.TrimSpace(string(mode)) == "tree" {
		next = "diff\n"
	}
	return os.WriteFile(unpackModePath(stateDir), []byte(next), 0o600)
}

type unpackManifestRow struct {
	kind, path, abs string
}

func readUnpackManifest(stateDir string) (map[string]unpackManifestRow, error) {
	f, err := os.Open(filepath.Join(stateDir, "manifest.tsv"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows := map[string]unpackManifestRow{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "\t", 4)
		if len(fields) == 4 {
			rows[fields[0]] = unpackManifestRow{kind: fields[1], path: fields[2], abs: fields[3]}
		}
	}
	return rows, scanner.Err()
}

// RunInternalUnpackPreview prints the colored diff for the focused row, or in
// tree mode the tree of every selected file. selectionPath is fzf's {+f}
// file; like Enter, it holds the focused row when nothing is selected.
func RunInternalUnpackPreview(stateDir, index, selectionPath string, stdout io.Writer) error {
	rows, err := readUnpackManifest(stateDir)
	if err != nil {
		return err
	}
	// The agent's caveats ("I assumed User has an Email field") stay on
	// screen above every file while the user decides what to write.
	if notes, err := os.ReadFile(unpackNotesPath(stateDir)); err == nil && len(notes) > 0 {
		if _, err := io.WriteString(stdout, unpackNotesBlock(string(notes), platform.ActivePaletteForWriter(stdout))); err != nil {
			return err
		}
	}
	if mode, _ := os.ReadFile(unpackModePath(stateDir)); strings.TrimSpace(string(mode)) == "tree" && selectionPath != "" {
		return writeUnpackSelectionTree(stateDir, rows, selectionPath, stdout)
	}
	key := strings.TrimSpace(index)
	row, ok := rows[key]
	if !ok {
		return fmt.Errorf("unpack preview: no entry %q", index)
	}
	body, err := os.ReadFile(filepath.Join(stateDir, key+".body"))
	if err != nil {
		return err
	}
	c := unpack.Change{Kind: unpack.ChangeKind(row.kind), Path: row.path, Abs: row.abs, Body: body}
	if c.Kind == unpack.KindChanged {
		if c.Local, err = os.ReadFile(c.Abs); err != nil {
			return err
		}
	}
	_, err = io.WriteString(stdout, renderpkg.HighlightPrintSource(c.Path, unpack.UnifiedDiff(c), true))
	return err
}

// writeUnpackSelectionTree shows what Enter would write: each selected file
// with [new] or [changed]. It uses the same tree document, renderer, preview
// options and Count/Size summary as catclip's other tree previews; sizes are
// the bundle's file sizes, and tokens do not apply to writing files.
func writeUnpackSelectionTree(stateDir string, rows map[string]unpackManifestRow, selectionPath string, stdout io.Writer) error {
	data, err := os.ReadFile(selectionPath)
	if err != nil {
		return err
	}
	var entries []treeDocumentEntry
	sizes := map[string]int64{}
	var total int64
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || seen[fields[1]] {
			continue
		}
		row, ok := rows[strings.TrimSpace(fields[1])]
		if !ok {
			continue
		}
		seen[fields[1]] = true
		entry := treeDocumentEntry{Path: row.path, ModeTag: row.kind}
		if info, err := os.Stat(filepath.Join(stateDir, fields[1]+".body")); err == nil {
			size := info.Size()
			entry.Size = &size
			sizes[row.path] = size
			total += size
		}
		entries = append(entries, entry)
	}
	human, _ := renderpkg.FormatSizeAndTokens(total, len(entries))
	opts := FzfFilterTreeRenderOptions()
	opts.ShowGitStatus, opts.ShowTokens = false, false
	return renderTreeDocument(stdout, treeDocument{
		Mode:    treeDocumentModeTree,
		Entries: entries,
		Summary: renderpkg.BuildSummary(sizes, human, 0, ""),
	}, opts, platform.ANSIPalette())
}

// unpackRowNote marks a file changed by excerpt edits rather than replaced
// whole, so the user knows only those lines change.
func unpackRowNote(c unpack.Change) string {
	if c.LineEdits {
		return "  (line edits)"
	}
	return ""
}

func unpackNotesPath(stateDir string) string { return filepath.Join(stateDir, "notes") }

// unpackNotesLimit keeps a long chat explanation from pushing the diff out of
// the preview; the full notes are in the report after writing.
const unpackNotesLimit = 12

// unpackNotesBlock renders the reply's notes above a preview.
func unpackNotesBlock(notes string, colors platform.Palette) string {
	lines := strings.Split(strings.TrimSpace(notes), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "%sNotes from the reply:%s\n", colors.Bold, colors.Reset)
	for i, line := range lines {
		if i == unpackNotesLimit {
			fmt.Fprintf(&b, "%s  ... %d more lines, shown after writing%s\n", colors.Dim, len(lines)-i, colors.Reset)
			break
		}
		fmt.Fprintf(&b, "  %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}
