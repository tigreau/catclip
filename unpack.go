package catclip

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/ui"
	"github.com/tigreau/catclip/internal/unpack"
)

// unpackPromptYesNo is replaceable in tests; it is the shared catclip prompt.
var unpackPromptYesNo = ui.PromptYesNo

// unpackChooseChanges is replaceable in tests; it runs the fzf picker.
var unpackChooseChanges = ui.ChooseUnpackChanges

// runUnpack writes a project's files back from a catclip bundle. It reads only
// the bundle and the exact paths it names, collects every decision first, and
// checks that every chosen file can be written before writing any.
func runUnpack(opts cli.UnpackOptions, stdout, stderr io.Writer) error {
	// --headless cannot show questions, so it accepts every new and changed
	// file like --yes. The stdout report lists each file written. --print
	// only shows: stdout carries the diffs and nothing is written.
	if opts.Headless && !opts.Print {
		opts.Yes = true
	}
	report := stderr
	colors := platform.ActivePaletteForWriter(stderr)
	if opts.Headless {
		colors = platform.Palette{}
		if !opts.Print {
			report = stdout
		}
	}
	data, err := os.ReadFile(opts.Path)
	if err != nil {
		return newExitError(1, fmt.Sprintf("Error: cannot read bundle %s: %v", opts.Path, unwrapPathError(err)))
	}
	bundle, err := unpack.Parse(data)
	if errors.Is(err, unpack.ErrNotBundle) {
		return newExitError(1, fmt.Sprintf("Error: %s is not a catclip bundle: it has no <file path=\"...\"> entries.\n"+
			"  --unpack reads any text with catclip <file path=\"...\"> ... </file> entries:\n"+
			"  catclip's own output, or an agent's reply in that format saved as a .txt file.", opts.Path))
	}
	if err != nil {
		return newExitError(1, fmt.Sprintf("Error: %s could not be read as a catclip bundle: %v\n  Nothing was written.", opts.Path, err))
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	var unsafe unpack.UnsafePathError
	if err := unpack.ValidatePaths(root, bundle); errors.As(err, &unsafe) {
		return newExitError(1, fmt.Sprintf("Error: refusing to unpack %s: %v\n  Nothing was written.", opts.Path, err))
	} else if err != nil {
		return err
	}
	plan, err := unpack.BuildPlan(root, bundle)
	if err != nil {
		return err
	}
	if len(bundle.Entries) == 0 {
		if _, err := fmt.Fprintf(report, "%sNothing to unpack: %s has no full files.%s\n", colors.Warn, opts.Path, colors.Reset); err != nil {
			return err
		}
		if err := writeUnpackSkipped(report, plan.Skipped, colors); err != nil {
			return err
		}
		return newExitError(1, "")
	}
	if len(plan.Changes) == 0 {
		// --print --headless: stdout is the (empty) patch, stderr errors only.
		if opts.Print && opts.Headless {
			return nil
		}
		if err := writeUnpackLine(report, colors, fmt.Sprintf("Already up to date: %d %s identical.", plan.Identical, filesWord(plan.Identical))); err != nil {
			return err
		}
		return writeUnpackSkipped(report, plan.Skipped, colors)
	}
	if opts.Print {
		return printUnpackDiffs(plan, opts, stdout, report, colors)
	}

	chosen := plan.Changes
	if !opts.Yes {
		changed, added := plan.Counts()
		if _, err := fmt.Fprintf(stderr, "Unpacking %s: %d changed, %d new, %d already identical.\n",
			opts.Path, changed, added, plan.Identical); err != nil {
			return err
		}
		question := fmt.Sprintf("%sIs %s%s%s%s the project root? [Y/n]%s",
			colors.Prompt, colors.Reset+colors.Bold, platform.DisplayPath(root), colors.Reset, colors.Prompt, colors.Reset)
		ok, err := unpackPromptYesNo(question, true, stderr)
		if err != nil {
			return err
		}
		if !ok {
			return newExitError(1, "Unpack cancelled; nothing was written.")
		}
		indices, err := unpackChooseChanges(plan)
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			return newExitError(1, "Unpack cancelled; nothing was written.")
		}
		if err != nil {
			return err
		}
		chosen = make([]unpack.Change, 0, len(indices))
		for _, i := range indices {
			chosen = append(chosen, plan.Changes[i])
		}
		if len(chosen) == 0 {
			return newExitError(1, "No files selected; nothing was written.")
		}
	}

	if err := unpack.Check(chosen); err != nil {
		return newExitError(1, "Error: "+err.Error())
	}
	written, err := unpack.Write(chosen)
	if err != nil {
		return newExitError(1, "Error: "+err.Error())
	}
	replaced, added := 0, 0
	for _, c := range chosen {
		if c.Kind == unpack.KindNew {
			added++
		} else {
			replaced++
		}
	}
	if err := writeUnpackLine(report, colors, fmt.Sprintf("Wrote %d %s into %s (%d replaced, %d new).",
		len(written), filesWord(len(written)), platform.DisplayPath(root), replaced, added)); err != nil {
		return err
	}
	// Name every written file and whether it replaced one or is new, so a
	// person or agent knows exactly what changed in the project.
	if !opts.Quiet || opts.Headless {
		kinds := make(map[string]unpack.ChangeKind, len(chosen))
		for _, c := range chosen {
			kinds[c.Path] = c.Kind
		}
		for _, path := range written {
			if _, err := fmt.Fprintf(report, "  %-7s  %s\n", kinds[path], path); err != nil {
				return err
			}
		}
	}
	if left := len(plan.Changes) - len(chosen); left > 0 {
		if _, err := fmt.Fprintf(report, "Left out %d %s you unselected.\n", left, filesWord(left)); err != nil {
			return err
		}
	}
	return writeUnpackSkipped(report, plan.Skipped, colors)
}

// printUnpackDiffs writes the unified diff of every new and changed file to
// stdout and writes nothing to the project. Diffs are coloured only where
// catclip's other --print output is (a terminal, no --headless/--quiet,
// colours enabled), so pipes and redirects get a plain patch. The summary
// goes to stderr, keeping stdout to the diffs alone.
func printUnpackDiffs(plan unpack.Plan, opts cli.UnpackOptions, stdout, report io.Writer, colors platform.Palette) error {
	color := output.TerminalPrintPalette(output.EmitConfig{OutputMode: command.OutputModeStdout}, opts.Headless, opts.Quiet, stdout).Reset != ""
	for _, c := range plan.Changes {
		diff := unpack.UnifiedDiff(c)
		if color {
			diff = render.HighlightPrintSource(c.Path, diff, true)
		}
		if _, err := io.WriteString(stdout, diff); err != nil {
			return err
		}
	}
	// --headless keeps catclip's contract: stdout is the payload (here the
	// plain diffs) and stderr carries errors only.
	if opts.Quiet || opts.Headless {
		return nil
	}
	changed, added := plan.Counts()
	if _, err := fmt.Fprintf(report, "%s%d changed, %d new, %d already identical. Nothing was written; run without --print to unpack.%s\n",
		colors.Dim, changed, added, plan.Identical, colors.Reset); err != nil {
		return err
	}
	return writeUnpackSkipped(report, plan.Skipped, colors)
}

func writeUnpackLine(w io.Writer, colors platform.Palette, text string) error {
	_, err := fmt.Fprintf(w, "%s%s%s\n", colors.OK, text, colors.Reset)
	return err
}

func writeUnpackSkipped(w io.Writer, skipped []unpack.Skipped, colors platform.Palette) error {
	if len(skipped) == 0 {
		return nil
	}
	text := fmt.Sprintf("Skipped %d entries that are not full files:", len(skipped))
	if len(skipped) == 1 {
		text = "Skipped 1 entry that is not a full file:"
	}
	if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Warn, text, colors.Reset); err != nil {
		return err
	}
	for _, s := range skipped {
		if _, err := fmt.Fprintf(w, "  %s — %s\n", s.Path, s.Reason); err != nil {
			return err
		}
	}
	return nil
}

func filesWord(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
