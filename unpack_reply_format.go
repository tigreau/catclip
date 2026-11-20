package catclip

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/ui"
)

// unpackCopyText is replaceable in tests; it writes the real clipboard.
var unpackCopyText = func(text []byte, colors platform.Palette) error {
	return output.CopyText(platform.Detect(), text, colors)
}

// unpackPromptChoice is replaceable in tests; it asks on the terminal.
var unpackPromptChoice = ui.PromptChoice

// unpackReplyFormatCanAsk is replaceable in tests: questions need a terminal.
var unpackReplyFormatCanAsk = func() bool { return platform.IsConsoleInput(os.Stdin) }

// unpackReplyFormatPath is the user's editable copy, next to .hiss.
func unpackReplyFormatPath() string {
	return filepath.Join(filepath.Dir(discovery.GlobalHissPath()), cli.UnpackReplyFormatFile)
}

// replyFormatSource is the text to copy and where it came from.
type replyFormatSource struct {
	text   string
	path   string // "" when the config folder cannot be written
	custom bool   // the user's copy, which differs from the built-in text
}

// loadUnpackReplyFormat returns the user's copy of the reply format,
// creating it from the built-in text the first time. When the copy differs
// from the built-in text (edited by the user, or written by an older
// catclip), it asks every time which one to use; Enter takes catclip's
// version and replaces the copy (user, 2026-11-19).
// It asks only when copying at a terminal; a question nobody answers
// (input ended) keeps the copy. --print and scripts use the copy silently.
// useBuiltIn (--yes) answers 1 without asking. hint (--headless, agents)
// keeps the copy and points to --yes on stderr.
func loadUnpackReplyFormat(ask, useBuiltIn, hint bool, stderr io.Writer) (replyFormatSource, error) {
	builtIn := cli.UnpackReplyFormatDefault
	path := unpackReplyFormatPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.MkdirAll(filepath.Dir(path), 0o755) != nil || os.WriteFile(path, []byte(builtIn), 0o644) != nil {
			return replyFormatSource{text: builtIn}, nil
		}
		return replyFormatSource{text: builtIn, path: path}, nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if text == builtIn {
		return replyFormatSource{text: builtIn, path: path}, nil
	}
	if useBuiltIn {
		if err := os.WriteFile(path, []byte(builtIn), 0o644); err != nil {
			return replyFormatSource{}, err
		}
		return replyFormatSource{text: builtIn, path: path}, nil
	}
	keep := func() (replyFormatSource, error) {
		if hint {
			fmt.Fprintf(stderr, "Note: your reply format (%s) differs from catclip's; using yours.\n"+
				"  catclip --unpack-reply-format --headless --yes uses catclip's version and replaces yours.\n",
				platform.DisplayPath(path))
		}
		return replyFormatSource{text: text, path: path, custom: true}, nil
	}
	if !ask {
		return keep()
	}
	// Wording chosen by the user (2026-11-19): why it differs, two plain
	// choices, Enter takes catclip's version.
	question := fmt.Sprintf("Your reply format is different from catclip's.\n"+
		"You may have edited your copy, or catclip was updated with a newer version.\n"+
		"  Your copy: %s\n\n"+
		"  1. Use catclip's version (your copy is replaced)\n"+
		"  2. Keep using your copy\n\n"+
		"Choose 1 or 2 [1]:", platform.DisplayPath(path))
	choice, err := unpackPromptChoice(question, 2, 1, stderr)
	if err != nil {
		return replyFormatSource{}, err
	}
	switch choice {
	case 1:
		if err := os.WriteFile(path, []byte(builtIn), 0o644); err != nil {
			return replyFormatSource{}, err
		}
		return replyFormatSource{text: builtIn, path: path}, nil
	case 0: // nobody answered (input ended): an agent, never replace
		return keep()
	}
	return replyFormatSource{text: text, path: path, custom: true}, nil
}

// runUnpackReplyFormat copies the reply format for agents and says what to
// do with it and where to edit it. --print writes the user's copy to stdout
// instead, without asking. --headless (agents) writes it to stdout with no
// question and no next steps for the user, only the --yes hint when the copy
// differs. --yes takes catclip's version without asking.
func runUnpackReplyFormat(opts cli.ReplyFormatOptions, stdout, stderr io.Writer) error {
	toStdout := opts.Print || opts.Headless
	src, err := loadUnpackReplyFormat(!toStdout && unpackReplyFormatCanAsk(), opts.Yes, opts.Headless, stderr)
	if err != nil {
		return err
	}
	if toStdout {
		_, err := io.WriteString(stdout, src.text)
		return err
	}
	colors := platform.ActivePaletteForWriter(stderr)
	if err := unpackCopyText([]byte(src.text), colors); err != nil {
		return err
	}
	which := ""
	if src.custom {
		which = " (your edited version)"
	}
	where := "The text is built into catclip."
	if src.path != "" {
		where = fmt.Sprintf("The text is %s%s%s: you can edit it.", colors.Bold, platform.DisplayPath(src.path), colors.Reset)
	}
	_, err = fmt.Fprintf(stderr, "%sCopied the reply format for agents%s to the clipboard.%s\n"+
		"Paste it once into the chat's custom instructions or a Project, or at the start\n"+
		"of a new chat together with your code and request. If replies stop following\n"+
		"the format, paste it again. When a reply comes back, copy it and run: catclip --unpack\n"+
		"%s\n", colors.OK, which, colors.Reset, where)
	return err
}
