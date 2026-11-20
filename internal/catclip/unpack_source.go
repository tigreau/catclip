package catclip

import (
	"fmt"
	"os"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

// unpackReadClipboard is replaceable in tests; it reads the real clipboard.
var unpackReadClipboard = func(colors platform.Palette) (output.ClipboardContent, error) {
	return output.ReadClipboard(platform.Detect(), colors)
}

// unpackSource is the bundle --unpack works on and how to name it.
type unpackSource struct {
	data      []byte
	clipboard bool
	// name fits "X is not a catclip bundle" and "refusing to unpack X".
	name string
	// heading fits "Unpacking X: 2 changed, ...".
	heading string
}

// notBundleOnClipboard is text on the clipboard without bundle entries.
var notBundleOnClipboard = unpackProblem{
	title: "The clipboard does not hold a catclip bundle: it has no <file path=\"...\"> entries.",
	cliFix: []string{
		"Copy the agent's whole reply, then run catclip --unpack again.",
		"If the reply is not in catclip's format, the chat may not have the reply format yet:",
		"run catclip --unpack-reply-format, paste it into the chat, and ask again.",
		"To use a bundle file instead: catclip --unpack FILE",
	},
	menuTitle: "This doesn't look like a catclip reply.",
	menuFix: []string{
		"Copy the agent's whole reply, then come back\nto [clipboard].",
		"Not in catclip's format? The chat may not have\nthe reply format yet: use [reply format], paste\nit into the chat, and ask again.",
		"To use a bundle file instead, use [file].",
	},
}

// readUnpackSource reads the bundle from opts.Path, or from the clipboard when
// no file was given. --unpack applies whatever was copied: one copied file or
// the path of one is read as the bundle; other text is the bundle itself.
func readUnpackSource(opts cli.UnpackOptions, colors platform.Palette) (unpackSource, error) {
	if !opts.FromClipboard() {
		return readUnpackBundleFile(opts.Path)
	}
	content, err := unpackReadClipboard(colors)
	if err != nil {
		return unpackSource{}, unpackProblem{
			title:    err.Error(),
			noPrefix: true,
			cliFix:   []string{"To use a bundle file instead: catclip --unpack FILE"},
			menuFix:  []string{"To use a bundle file instead, use [file]."},
		}
	}
	switch {
	case len(content.Files) == 1:
		src, err := readUnpackBundleFile(content.Files[0])
		if err != nil {
			return unpackSource{}, err
		}
		src.name = fmt.Sprintf("the copied file %s", content.Files[0])
		src.heading = fmt.Sprintf("the copied file %s", content.Files[0])
		return src, nil
	case len(content.Files) > 1:
		return unpackSource{}, unpackProblem{
			title:     fmt.Sprintf("The clipboard holds %d copied files; --unpack reads one bundle.", len(content.Files)),
			cliFix:    []string{"Copy only the bundle file, or run catclip --unpack FILE"},
			menuTitle: fmt.Sprintf("The clipboard holds %d copied files.", len(content.Files)),
			menuFix:   []string{"Copy only the bundle file, then come back\nto [clipboard], or use [file]."},
		}
	}
	if strings.TrimSpace(content.Text) == "" {
		return unpackSource{}, unpackProblem{
			title: "The clipboard is empty.",
			cliFix: []string{
				"Copy the agent's whole reply, then run catclip --unpack again.",
				"To use a bundle file instead: catclip --unpack FILE",
			},
			menuFix: []string{
				"Copy the agent's whole reply, then come back\nto [clipboard].",
				"To use a bundle file instead, use [file].",
			},
		}
	}
	// A copied path (e.g. Explorer's "Copy as path") counts like a copied
	// file: --unpack applies whatever the user copied. The summary and the
	// diff still come before anything is written.
	if path, ok := clipboardHoldsFilePath(content.Text); ok {
		src, err := readUnpackBundleFile(path)
		if err != nil {
			return unpackSource{}, err
		}
		src.name = fmt.Sprintf("the copied path %s", path)
		src.heading = fmt.Sprintf("the copied path %s", path)
		return src, nil
	}
	return unpackSource{data: []byte(content.Text), clipboard: true, name: "the clipboard", heading: "from the clipboard"}, nil
}

func readUnpackBundleFile(path string) (unpackSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return unpackSource{}, unpackProblem{
			title:   fmt.Sprintf("cannot read bundle %s: %v", path, unwrapPathError(err)),
			menuFix: []string{"Check the path, or drag the file here."},
		}
	}
	return unpackSource{data: data, name: path, heading: path}, nil
}

// clipboardHoldsFilePath reports whether the clipboard text is just the path
// of an existing file (someone copied the path of reply.txt, not its
// contents). Quotes from "Copy as path" are removed.
func clipboardHoldsFilePath(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "\n") || strings.Contains(text, "<file ") {
		return "", false
	}
	if len(text) >= 2 && (text[0] == '"' && text[len(text)-1] == '"' || text[0] == '\'' && text[len(text)-1] == '\'') {
		text = text[1 : len(text)-1]
	}
	if info, err := os.Stat(text); err == nil && info.Mode().IsRegular() {
		return text, true
	}
	return "", false
}

// isReplyFormat recognises catclip's own reply format (from
// --unpack-reply-format) by its first two lines.
func isReplyFormat(data []byte) bool {
	// The guide for web agents (--unpack-reply-format), recognised by its
	// title also when the user edited the rest.
	head := strings.ReplaceAll(string(data[:min(len(data), 1024)]), "\r\n", "\n")
	return strings.Contains(head, "# catclip for web agents\n")
}
