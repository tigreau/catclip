package catclip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// unpackCanAsk is replaceable in tests: whether anyone can answer the root
// question and use the picker (stdin is a console, or one can be opened).
var unpackCanAsk = platform.CanPromptInteractively

// unpackPrepared is a bundle read, parsed and compared with the project: what
// --unpack and the interactive unpack menu's previews both start from.
type unpackPrepared struct {
	src    unpackSource
	bundle unpack.Bundle
	plan   unpack.Plan
	root   string
}

// prepareUnpack reads the bundle and checks it against the project without
// asking or writing anything. Its errors are the user-facing messages of
// --unpack.
func prepareUnpack(opts cli.UnpackOptions, colors platform.Palette) (unpackPrepared, error) {
	src, err := readUnpackSource(opts, colors)
	if err != nil {
		return unpackPrepared{}, err
	}
	data := src.data
	// The reply format is itself a valid bundle (its examples are entries).
	// Unpacking it, e.g. after copying the format but not the agent's answer,
	// would create and edit the files its examples name.
	if isReplyFormat(data) {
		return unpackPrepared{}, unpackProblem{
			title: fmt.Sprintf("%s holds catclip's reply format, not a reply.", src.name),
			cliFix: []string{
				"Paste the format into the chat, ask for changes, then copy the agent's reply",
				"and run catclip --unpack. Nothing was written.",
			},
			menuFix: []string{"Paste it into the chat and ask for changes,\nthen copy the agent's reply and come back\nto [clipboard]."},
		}
	}
	bundle, err := unpack.Parse(data)
	if errors.Is(err, unpack.ErrNotBundle) {
		// An agent that needs more code replies without code and asks for a
		// catclip command (as the reply format tells it to): point to it.
		if cmds := requestedCatclipCommands(bundle.Notes); len(cmds) > 0 {
			// Each catclip run replaces the clipboard, so with several
			// commands the user pastes after each one.
			how := "paste the output into the chat"
			if len(cmds) > 1 {
				how = "one at a time, pasting each output into the chat"
			}
			return unpackPrepared{}, unpackProblem{
				title:    "The reply has no code yet: it asks for more of your code first. Run:",
				noPrefix: true,
				detail:   cmds,
				cliFix:   []string{how + ", then copy the new reply and run catclip --unpack again.", "Nothing was written."},
				menuFix:  []string{"Quit catclip and run them, pasting each output\ninto the chat. Then copy the agent's new reply\nand unpack it."},
			}
		}
		if src.clipboard {
			return unpackPrepared{}, notBundleOnClipboard
		}
		return unpackPrepared{}, unpackProblem{
			title: fmt.Sprintf("%s is not a catclip bundle: it has no <file path=\"...\"> entries.", src.name),
			detail: []string{
				"--unpack reads any text with catclip <file path=\"...\"> ... </file> entries:",
				"catclip's own output, or an agent's reply in that format saved as a .txt file.",
			},
			cliFix:  []string{"For a chat reply, send the chat the reply format first: catclip --unpack-reply-format"},
			menuFix: []string{"For a chat reply, send the chat\n[reply format] first."},
		}
	}
	if err != nil {
		return unpackPrepared{}, newExitError(1, fmt.Sprintf("Error: %s could not be read as a catclip bundle: %v\n  Nothing was written.", src.name, err))
	}
	root, err := os.Getwd()
	if err != nil {
		return unpackPrepared{}, err
	}
	var unsafe unpack.UnsafePathError
	if err := unpack.ValidatePaths(root, bundle); errors.As(err, &unsafe) {
		return unpackPrepared{}, newExitError(1, fmt.Sprintf("Error: refusing to unpack %s: %v\n  Nothing was written.", src.name, err))
	} else if err != nil {
		return unpackPrepared{}, err
	}
	// Bundle paths are relative to the folder the copy was made in. When none
	// of them exists here but they do in a nearby folder (frontend/ in a
	// repository unpacked from its root), point there instead of creating the
	// files in the wrong place.
	if home, ok := unpack.FindBundleHome(root, bundle); ok {
		rel, relErr := filepath.Rel(root, home)
		if relErr != nil {
			rel = home
		}
		return unpackPrepared{}, unpackProblem{
			title:   fmt.Sprintf("None of the files in %s exist here, but they do in %s.", src.name, platform.DisplayPath(home)),
			cliFix:  []string{"Paths in a bundle are relative to the folder it was copied in. Run catclip --unpack there:", "  cd " + rel, "Nothing was written."},
			menuFix: []string{"Paths are relative to the folder the copy was\nmade in. Quit catclip and unpack there:\n  cd " + rel},
		}
	}
	plan, err := unpack.BuildPlan(root, bundle)
	if err != nil {
		return unpackPrepared{}, err
	}
	return unpackPrepared{src: src, bundle: bundle, plan: plan, root: root}, nil
}

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
	prep, err := prepareUnpack(opts, colors)
	if err != nil {
		return err
	}
	src, bundle, plan, root := prep.src, prep.bundle, prep.plan, prep.root
	// A reply is one change (user, 2026-11-19): if any entry meant to be
	// written cannot be (changed since the excerpt was copied, missing here,
	// a folder or binary in the way), write nothing. Applying the rest could
	// leave code that no longer builds. --print still shows the diffs.
	blocked := plan.Blocked()
	if len(blocked) > 0 {
		if opts.Print {
			if err := writeUnpackDiffs(plan, opts, stdout); err != nil {
				return err
			}
		}
		return newExitError(1, blockedReplyMessage(blocked, len(plan.Changes), colors))
	}
	if len(bundle.Entries) == 0 {
		if _, err := fmt.Fprintf(report, "%sNothing to unpack: %s has no files to write.%s\n", colors.Warn, src.name, colors.Reset); err != nil {
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
		if plan.Identical > 0 {
			if err := writeUnpackLine(report, colors, fmt.Sprintf("Already up to date: %d %s identical.", plan.Identical, filesWord(plan.Identical))); err != nil {
				return err
			}
		}
		return writeUnpackSkipped(report, plan.Skipped, colors)
	}
	if opts.Print {
		return printUnpackDiffs(plan, opts, stdout, report, colors)
	}

	chosen := plan.Changes
	if !opts.Yes {
		// Nobody can answer (CI, cron, ssh without -t): say what to run
		// instead, like bare catclip and catclip -- do.
		if !unpackCanAsk() {
			return newExitError(1, "Error: --unpack asks before writing anything.\n"+
				"  Run catclip --unpack in an interactive terminal, or add --yes to write every change (--print only shows them).\n"+
				"  Nothing was written.")
		}
		if _, err := fmt.Fprintf(stderr, "Unpacking %s: %s.\n", src.heading, unpackCountsText(plan)); err != nil {
			return err
		}
		question := fmt.Sprintf("%sIs %s%s%s%s the project root? [Y/n]:%s",
			colors.Prompt, colors.Reset+colors.Bold, platform.DisplayPath(root), colors.Reset, colors.Prompt, colors.Reset)
		ok, err := unpackPromptYesNo(question, true, stderr)
		if err != nil {
			return err
		}
		if !ok {
			// From the interactive menu there is a menu to go back to.
			if opts.CanGoBack {
				return discovery.ErrSelectionCancelled
			}
			return newExitError(1, "Unpack cancelled; nothing was written.")
		}
		indices, err := unpackChooseChanges(plan)
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			if opts.CanGoBack {
				return err
			}
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
	replaced, added, moved := 0, 0, 0
	for _, c := range chosen {
		switch c.Kind {
		case unpack.KindNew:
			added++
		case unpack.KindMoved:
			moved++
		default:
			replaced++
		}
	}
	movedText := ""
	if moved > 0 {
		movedText = fmt.Sprintf(", %d moved", moved)
	}
	if err := writeUnpackLine(report, colors, fmt.Sprintf("Wrote %d %s into %s (%d replaced, %d new%s).",
		len(written), filesWord(len(written)), platform.DisplayPath(root), replaced, added, movedText)); err != nil {
		return err
	}
	// Name every written file and whether it replaced one or is new, so a
	// person or agent knows exactly what changed in the project.
	if !opts.Quiet || opts.Headless {
		byPath := make(map[string]unpack.Change, len(chosen))
		for _, c := range chosen {
			byPath[c.Path] = c
		}
		for _, path := range written {
			note := ""
			if byPath[path].LineEdits {
				note = "  (line edits)"
			}
			if byPath[path].Kind == unpack.KindMoved {
				note = "  (from " + byPath[path].From + ")"
			}
			if _, err := fmt.Fprintf(report, "  %-7s  %s%s\n", byPath[path].Kind, path, note); err != nil {
				return err
			}
		}
	}
	if left := len(plan.Changes) - len(chosen); left > 0 {
		if _, err := fmt.Fprintf(report, "Left out %d %s you unselected.\n", left, filesWord(left)); err != nil {
			return err
		}
	}
	if err := writeUnpackSkipped(report, plan.Skipped, colors); err != nil {
		return err
	}
	// The agent's caveats, also for --yes and --headless runs that never
	// showed the picker.
	return writeUnpackNotes(report, plan.Notes, colors)
}

// writeUnpackNotes prints the reply's text outside the entries, in full.
func writeUnpackNotes(w io.Writer, notes string, colors platform.Palette) error {
	if strings.TrimSpace(notes) == "" {
		return nil
	}
	if _, err := fmt.Fprintf(w, "%sNotes from the reply:%s\n", colors.Bold, colors.Reset); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(notes), "\n") {
		if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

// requestedCatclipCommands returns the catclip commands a reply asks the user
// to run (an agent that needs more code before it can answer), one per line.
func requestedCatclipCommands(notes []string) []string {
	var cmds []string
	for _, line := range notes {
		t := strings.Trim(strings.TrimSpace(line), "`$> ")
		if strings.HasPrefix(t, "catclip ") && !strings.HasPrefix(t, "catclip --unpack") {
			cmds = append(cmds, t)
		}
	}
	return cmds
}

// printUnpackDiffs writes the unified diff of every new and changed file to
// stdout and writes nothing to the project. Diffs are coloured only where
// catclip's other --print output is (a terminal, no --headless/--quiet,
// colours enabled), so pipes and redirects get a plain patch. The summary
// goes to stderr, keeping stdout to the diffs alone.
func printUnpackDiffs(plan unpack.Plan, opts cli.UnpackOptions, stdout, report io.Writer, colors platform.Palette) error {
	if err := writeUnpackDiffs(plan, opts, stdout); err != nil {
		return err
	}
	// --headless keeps catclip's contract: stdout is the payload (here the
	// plain diffs) and stderr carries errors only.
	if opts.Quiet || opts.Headless {
		return nil
	}
	if _, err := fmt.Fprintf(report, "%s%s. Nothing was written; run without --print to unpack.%s\n",
		colors.Dim, unpackCountsText(plan), colors.Reset); err != nil {
		return err
	}
	if err := writeUnpackSkipped(report, plan.Skipped, colors); err != nil {
		return err
	}
	return writeUnpackNotes(report, plan.Notes, colors)
}

func writeUnpackLine(w io.Writer, colors platform.Palette, text string) error {
	_, err := fmt.Fprintf(w, "%s%s%s\n", colors.OK, text, colors.Reset)
	return err
}

func writeUnpackSkipped(w io.Writer, skipped []unpack.Skipped, colors platform.Palette) error {
	if len(skipped) == 0 {
		return nil
	}
	text := fmt.Sprintf("Skipped %d entries:", len(skipped))
	if len(skipped) == 1 {
		text = "Skipped 1 entry:"
	}
	if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Warn, text, colors.Reset); err != nil {
		return err
	}
	var b strings.Builder
	writeSkippedEntries(&b, skipped, colors)
	_, err := io.WriteString(w, b.String())
	return err
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

// blockedReplyMessage explains, in plain words, why a reply was not applied
// and what to do (user, 2026-11-19: the earlier "1 entry ... can't be applied,
// and a reply is one change" meant nothing to a reader). others is the number
// of files in the reply that could have been written.
func blockedReplyMessage(blocked []unpack.Skipped, others int, colors platform.Palette) string {
	var b strings.Builder
	b.WriteString("Error: Nothing was written, because of these files:\n")
	writeBlockedReply(&b, blocked, others, colors, false)
	return strings.TrimRight(b.String(), "\n")
}

// writeBlockedReply lists what is wrong with each file and how to fix it,
// then why the reply's other files were not written (would not be, in the
// menu preview), and the last step.
func writeBlockedReply(w io.Writer, blocked []unpack.Skipped, others int, colors platform.Palette, preview bool) {
	writeSkippedEntries(w, blocked, colors)
	if others > 0 {
		subject, verb := "other file", "was"
		if others != 1 {
			subject, verb = fmt.Sprintf("other %d files", others), "were"
		}
		if preview {
			verb = "would be"
		}
		fmt.Fprintf(w, "\n  catclip applies a reply all at once or not at all, so the\n"+
			"  %s in it %s not written either.\n", subject, verb)
	}
	fmt.Fprintf(w, "\n  Then copy the new reply or bundle and unpack it.\n")
}

// problemFix is one thing to do about a problem: text, an optional command
// on its own line (copied as is), and text after it.
type problemFix struct{ text, command, after string }

// problemTexts explains each kind of problem once, for you and for a chat
// you paste the message into (user, 2026-11-20).
var problemTexts = []struct {
	kind  unpack.Problem
	title string
	fixes func(paths []string) []problemFix
}{
	{unpack.ProblemChanged, "These lines were cut from a different version of each file, so their line numbers no longer point at the same code:",
		func(paths []string) []problemFix {
			return []problemFix{
				{text: "From a chat: paste this message into the chat, then run", command: catclipCommandFor(paths),
					after: "and paste that too, so it redoes the change on the current code."},
				{text: "From a teammate: ask them to send the whole files."},
			}
		}},
	{unpack.ProblemMissing, "These excerpts change files that don't exist here:",
		func([]string) []problemFix {
			return []problemFix{{text: "Run catclip --unpack in the folder you copied them from."}}
		}},
	{unpack.ProblemShortened, "The code was shortened, which would delete what it stands for:",
		func([]string) []problemFix {
			return []problemFix{{text: "Paste this message into the chat; it names the code left out."}}
		}},
	{unpack.ProblemMoveSource, "These moves start from files that don't exist here:",
		func([]string) []problemFix {
			return []problemFix{{text: "Run catclip --unpack in the folder you copied them from. If you already moved them, paste this message into the chat so it uses the new paths."}}
		}},
	{unpack.ProblemMoveSourceType, "These moves require a regular file at the source path:", nil},
	{unpack.ProblemMoveTarget, "These moves would replace a file that is already there:",
		func([]string) []problemFix {
			return []problemFix{{text: "catclip never replaces a file by moving another onto it. Paste this message into the chat so it picks another path, or move that file away first."}}
		}},
	{unpack.ProblemFolder, "There is a folder with this name here, not a file:", nil},
	{unpack.ProblemBinary, "Your copy of these files is binary, so a text reply can't change them:", nil},
}

// problemListLimit caps the files listed per problem; the rest are counted.
const problemListLimit = 20

// writeSkippedEntries groups the entries by problem: each problem is
// explained once, its files are listed with what is specific to each, and
// the fixes follow once, so 100 stale excerpts stay readable. Entries that
// are only listed and ignored (Git patches, binary descriptions) follow as
// path and reason. Lines are wrapped by hand to keep their indent in the
// half-width menu preview.
func writeSkippedEntries(w io.Writer, skipped []unpack.Skipped, colors platform.Palette) {
	for _, p := range problemTexts {
		var rows [][2]string
		var paths []string
		for _, s := range skipped {
			if s.Problem == p.kind {
				rows = append(rows, [2]string{s.Path, s.Detail})
				paths = append(paths, s.Path)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprint(w, "\n")
		for _, line := range wrapWords(p.title, 68) {
			fmt.Fprintf(w, "  %s%s%s\n", colors.Warn, line, colors.Reset)
		}
		writePathRows(w, rows, colors)
		if p.fixes == nil {
			continue
		}
		for _, fix := range p.fixes(paths) {
			for i, line := range wrapWords(fix.text, 64) {
				mark := "  "
				if i == 0 {
					mark = "→ "
				}
				fmt.Fprintf(w, "  %s%s%s%s\n", colors.OK, mark, line, colors.Reset)
			}
			if fix.command != "" {
				fmt.Fprintf(w, "      %s%s%s\n", colors.Bold, fix.command, colors.Reset)
			}
			for _, line := range wrapWords(fix.after, 64) {
				fmt.Fprintf(w, "    %s%s%s\n", colors.OK, line, colors.Reset)
			}
		}
	}
	var listed [][2]string
	for _, s := range skipped {
		if s.Problem == "" {
			listed = append(listed, [2]string{s.Path, s.Reason})
		}
	}
	if len(listed) > 0 {
		fmt.Fprint(w, "\n")
		writePathRows(w, listed, colors)
	}
}

// writePathRows lists path and detail in two columns, at most
// problemListLimit rows, then "… and N more".
func writePathRows(w io.Writer, rows [][2]string, colors platform.Palette) {
	width := 0
	for i, r := range rows {
		if i == problemListLimit {
			break
		}
		if n := len([]rune(r[0])); n > width && n <= 32 {
			width = n
		}
	}
	for i, r := range rows {
		if i == problemListLimit {
			fmt.Fprintf(w, "    … and %d more\n", len(rows)-i)
			break
		}
		if r[1] == "" {
			fmt.Fprintf(w, "    %s%s%s\n", colors.Bold, r[0], colors.Reset)
			continue
		}
		pad := width - len([]rune(r[0]))
		if pad < 0 {
			pad = 0
		}
		fmt.Fprintf(w, "    %s%s%s%s   %s\n", colors.Bold, r[0], colors.Reset, strings.Repeat(" ", pad), r[1])
	}
}

// catclipCommandFor is one catclip command that copies the given files, to
// paste the current code into the chat. Paths with spaces are quoted.
func catclipCommandFor(paths []string) string {
	parts := []string{"catclip"}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if strings.ContainsAny(p, " \t'\"") {
			p = `"` + p + `"`
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

// wrapWords splits text into lines of at most width characters, breaking
// between words; a single longer word gets a line of its own.
func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// writeUnpackDiffs writes the unified diff of every new and changed file.
func writeUnpackDiffs(plan unpack.Plan, opts cli.UnpackOptions, stdout io.Writer) error {
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
	return nil
}

// unpackCountsText summarizes a plan: "2 changed, 1 new, 3 already
// identical", with "N moved" when the reply moves files.
func unpackCountsText(plan unpack.Plan) string {
	changed, added := plan.Counts()
	text := fmt.Sprintf("%d changed, %d new", changed, added)
	if moved := plan.Moved(); moved > 0 {
		text += fmt.Sprintf(", %d moved", moved)
	}
	return text + fmt.Sprintf(", %d already identical", plan.Identical)
}
