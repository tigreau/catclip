package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/ui"
	"github.com/tigreau/catclip/internal/unpack"
)

// The interactive unpack menu lives in internal/ui, which cannot import this
// package. These hooks give it the unpack flow that lives here.
func init() {
	ui.UnpackMenu = ui.UnpackMenuHooks{
		ClipboardSummary: unpackClipboardSummary,
		Run:              runUnpackMenuAction,
	}
}

// unpackClipboardSummary describes a clipboard that holds a bundle, for the
// [clipboard] row; ok is false when it holds none.
func unpackClipboardSummary() (string, bool) {
	prep, err := prepareUnpack(cli.UnpackOptions{}, platform.Palette{})
	if err != nil || len(prep.bundle.Entries) == 0 {
		return "", false
	}
	n := len(prep.bundle.Entries)
	from := "a chat reply"
	if !prep.src.clipboard {
		from = "a copied file"
	}
	return fmt.Sprintf("%d %s from %s", n, filesWord(n), from), true
}

// runUnpackMenuAction runs what the user chose in the unpack menu. Going back
// (the root question answered n, Esc in the picker) returns
// discovery.ErrSelectionCancelled; the menu then shows itself again.
func runUnpackMenuAction(action ui.UnpackMenuAction, path string) error {
	if action == ui.UnpackActionReplyFormat {
		return runUnpackReplyFormat(cli.ReplyFormatOptions{}, os.Stdout, os.Stderr)
	}
	opts := cli.UnpackOptions{CanGoBack: true}
	if action == ui.UnpackActionFile {
		opts.Path = path
	}
	return runUnpack(opts, os.Stdout, os.Stderr)
}

// unpackMenuSessionEnded handles the error the unpack menu returns when it is
// done: the session ends without the copy flow, and the unpack command is
// shown like any resolved command.
func unpackMenuSessionEnded(err error, stderr io.Writer) (bool, error) {
	var done *ui.StartupUnpackDone
	if !errors.As(err, &done) {
		return false, nil
	}
	return true, writeResolvedStartupCommand(stderr, done.Args)
}

// writeUnpackMenuHowItWorks ends the [clipboard] and [file] previews.
func writeUnpackMenuHowItWorks(w io.Writer, c platform.Palette) {
	fmt.Fprintf(w, "\n%sHow unpacking works%s\n"+
		"  • It %snever deletes%s files; a move only renames one.\n"+
		"  • You review the diffs and choose which files to write.\n"+
		"  • Nothing is written until you press %sEnter%s in that list.\n"+
		"  • There is %sno undo%s after writing: commit first.\n",
		c.Bold+c.Value, c.Reset, c.Bold, c.Reset, c.Bold+c.Prompt, c.Reset, c.Bold, c.Reset)
}

// runUnpackMenuPreview prints the preview of one unpack menu row. args are the
// row key and, in the [file] path input, the typed path ({q}). It runs in the
// fzf preview child process, in the theme's preview colors.
func runUnpackMenuPreview(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("unpack menu preview: missing row")
	}
	c := ui.UnpackPreviewColors()
	var b bytes.Buffer
	switch ui.UnpackMenuAction(args[0]) {
	case ui.UnpackActionReplyFormat:
		src, err := loadUnpackReplyFormat(cli.ReplyFormatOptions{Print: true}, false, io.Discard)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%sPress %sEnter%s%s to copy this text to the clipboard.%s\n"+
			"Paste it into the chat %sonce%s, then ask for changes.\n\n%s%s%s",
			c.Bold+c.Value, c.Prompt, c.Reset, c.Bold+c.Value, c.Reset, c.Bold, c.Reset, c.Dim, src.text, c.Reset)
	case ui.UnpackActionFile:
		path := ""
		if len(args) > 1 {
			path = ui.CleanUnpackPath(args[1])
		}
		switch {
		case len(args) == 1:
			// The unpack> menu: nothing can be typed here yet.
			fmt.Fprintf(&b, "%sPress %sEnter%s%s to open the path input.%s\n\n"+
				"On the %snext screen%s, paste the path of a bundle file,\n"+
				"or drag the file into the terminal. This pane then shows\n"+
				"what is in it.\n\n"+
				"The file can be anywhere. %s~%s and paths relative to this\n"+
				"folder work.\n",
				c.Bold+c.Value, c.Prompt, c.Reset, c.Bold+c.Value, c.Reset, c.Bold, c.Reset, c.OK, c.Reset)
			writeUnpackMenuHowItWorks(&b, c)
		case path == "":
			fmt.Fprintf(&b, "%sPaste or drag the path of the bundle file.%s\n\n"+
				"The file can be anywhere. %s~%s and paths relative to this\n"+
				"folder work. Press %sEnter%s to continue, %sEsc%s to go back.\n",
				c.Bold+c.Value, c.Reset, c.OK, c.Reset, c.Prompt, c.Reset, c.Prompt, c.Reset)
			writeUnpackMenuHowItWorks(&b, c)
		default:
			writeUnpackMenuSummary(&b, c, cli.UnpackOptions{Path: path})
			writeUnpackMenuHowItWorks(&b, c)
		}
	case ui.UnpackActionClipboard:
		writeUnpackMenuSummary(&b, c, cli.UnpackOptions{})
		writeUnpackMenuHowItWorks(&b, c)
	default:
		return fmt.Errorf("unpack menu preview: unknown row %q", args[0])
	}
	return ui.WriteUnpackPreview(stdout, b.String())
}

// writeUnpackMenuSummary shows what unpacking would do, or why it cannot, in
// short lines for the half-width preview pane.
func writeUnpackMenuSummary(w io.Writer, c platform.Palette, opts cli.UnpackOptions) {
	prep, err := prepareUnpack(opts, platform.Palette{})
	if err != nil {
		// Problems carry a fix worded for the menu ("use [file]"), not the
		// command line ("run catclip --unpack FILE").
		var problem unpackProblem
		if errors.As(err, &problem) {
			problem.writeMenu(w, c)
			return
		}
		fmt.Fprintf(w, "%s%s%s\n", c.Warn, strings.TrimSpace(err.Error()), c.Reset)
		return
	}
	plan := prep.plan
	if blocked := plan.Blocked(); len(blocked) > 0 {
		fmt.Fprintf(w, "%sUnpacking would stop%s\n  Nothing would be written, because of these files:\n", c.Bold+c.Warn, c.Reset)
		writeBlockedReply(w, blocked, len(plan.Changes), c, true)
		return
	}
	if len(prep.bundle.Entries) == 0 {
		fmt.Fprintf(w, "%sNothing to unpack:%s %s has no files to write.\n", c.Bold+c.Warn, c.Reset, prep.src.name)
		return
	}
	changed, added := plan.Counts()
	moved := ""
	if n := plan.Moved(); n > 0 {
		moved = fmt.Sprintf(" · %s%d moved%s", c.Prompt, n, c.Reset)
	}
	fmt.Fprintf(w, "%sFound in %s%s\n  %s%d changed%s · %s%d new%s%s · %d already identical\n\n",
		c.Bold+c.Value, prep.src.name, c.Reset,
		c.Prompt, changed, c.Reset, c.OK, added, c.Reset, moved, plan.Identical)
	const listLimit = 20
	for i, ch := range plan.Changes {
		if i == listLimit {
			fmt.Fprintf(w, "  … %d more\n", len(plan.Changes)-i)
			break
		}
		kind := c.Prompt
		if ch.Kind == unpack.KindNew {
			kind = c.OK
		}
		note := ""
		if ch.LineEdits {
			note = c.Dim + "  (line edits)" + c.Reset
		}
		if ch.Kind == unpack.KindMoved {
			note = c.Dim + "  (from " + ch.From + ")" + c.Reset
		}
		fmt.Fprintf(w, "  %s%-7s%s  %s%s\n", kind, ch.Kind, c.Reset, ch.Path, note)
	}
	_ = writeUnpackSkipped(w, plan.Skipped, c)
	_ = writeUnpackNotes(w, plan.Notes, c)
}
