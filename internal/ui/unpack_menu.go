package ui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/theme"
)

// UnpackMenuAction is a row of the unpack menu.
type UnpackMenuAction string

const (
	UnpackActionClipboard   UnpackMenuAction = "clipboard"
	UnpackActionFile        UnpackMenuAction = "file"
	UnpackActionReplyFormat UnpackMenuAction = "reply-format"
)

// UnpackMenuHooks connects the menu to the unpack flow in the root package,
// which this package cannot import.
type UnpackMenuHooks struct {
	// ClipboardSummary describes a clipboard that holds a bundle ("3 files
	// from a chat reply"); ok is false when it holds none.
	ClipboardSummary func() (summary string, ok bool)
	// Run does the chosen action. It returns discovery.ErrSelectionCancelled
	// when the user went back (root question n, Esc in the picker).
	Run func(action UnpackMenuAction, path string) error
}

// UnpackMenu is set by the root package at startup.
var UnpackMenu UnpackMenuHooks

// StartupUnpackDone is returned, as an error, when the unpack menu finished its
// job (files written, or the reply format copied). The session ends without
// the copy flow; Args is the command to show as the resolved command.
type StartupUnpackDone struct{ Args []string }

func (*StartupUnpackDone) Error() string { return "unpack finished" }

// Replaceable in tests: the two fzf steps of the menu.
var (
	unpackMenuChooseAction = chooseUnpackAction
	unpackMenuChoosePath   = chooseUnpackPath
)

// startupUnpackChoice is the [unpack] row of the filter menu. It is the last
// input row, so it is the top row on screen, away from the cursor's start.
var startupUnpackChoice = StartupModifierChoice{
	Key:         "unpack",
	Label:       "[unpack]",
	Description: "apply a bundle or chat reply to your project",
	Mode:        startupModifierModeUnpack,
}

// startupUnpackOffered: the [unpack] row appears only before any filter has
// been applied (user, 2026-11-19), and not in a later --then scope.
func startupUnpackOffered(currentArgs []string) bool {
	return !startupFrameCurrentScopeHasModifier(currentArgs) && !argsContain(currentArgs, "--then")
}

// chooseStartupUnpack runs the unpack submenu. It never returns nil: Esc in
// the menu returns discovery.ErrSelectionCancelled (back to filter>), and a
// finished unpack returns *StartupUnpackDone.
func chooseStartupUnpack() error {
	for {
		action, err := unpackMenuChooseAction()
		if err != nil {
			return err
		}
		path := ""
		if action == UnpackActionFile {
			path, err = unpackMenuChoosePath()
			if errors.Is(err, discovery.ErrSelectionCancelled) {
				continue
			}
			if err != nil {
				return err
			}
		}
		if UnpackMenu.Run == nil {
			return errors.New("Error: unpack is not available here.")
		}
		err = UnpackMenu.Run(action, path)
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			continue
		}
		if err != nil {
			return err
		}
		return &StartupUnpackDone{Args: unpackResolvedArgs(action, path)}
	}
}

func unpackResolvedArgs(action UnpackMenuAction, path string) []string {
	switch action {
	case UnpackActionReplyFormat:
		return []string{"--unpack-reply-format"}
	case UnpackActionFile:
		return []string{"--unpack", path}
	}
	return []string{"--unpack"}
}

// unpackMenuChoices lists the rows in input order. fzf stacks the first input
// row at the bottom, so on screen [clipboard] is on top.
func unpackMenuChoices(clipboardSummary string) []StartupModifierChoice {
	clipboard := "Apply what you copied to your project"
	if clipboardSummary != "" {
		clipboard += ": " + clipboardSummary
	}
	return []StartupModifierChoice{
		{Key: string(UnpackActionReplyFormat), Label: "[reply format]", Description: "Copy the reply format to paste into a chat"},
		{Key: string(UnpackActionFile), Label: "[file]", Description: "Paste or drag the path of a bundle file"},
		{Key: string(UnpackActionClipboard), Label: "[clipboard]", Description: clipboard},
	}
}

func unpackMenuHeader() string {
	return discovery.PickerHeader(
		// Two short lines: in fullscreen the header box is about half the
		// terminal wide, and one long line was cut off (user, 2026-11-19).
		"First time? Send [reply format] to the chat once,",
		"then ask for changes. The preview explains each row.",
		"[Up/Down] move  [Enter] confirm  [Esc] back",
	)
}

func unpackFileHeader() string {
	return discovery.PickerHeader(
		"Path to the bundle file (paste it, or drag the file here).",
		"Preview shows what is in it.",
		"[Enter] continue  [Esc] back",
	)
}

// unpackMenuPreviewCommand runs the root package's preview for one row: the
// focused row's key ({2}) when fixedKey is empty, otherwise fixedKey;
// withQuery adds the typed text ({q}). Placeholders stay standalone words so
// fzf quotes them safely for the Windows shell (architecture guard).
func unpackMenuPreviewCommand(fixedKey string, withQuery bool) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}
	key := "{2}"
	if fixedKey != "" {
		key = fixedKey
	}
	parts := []string{picker.CommandExecutable(self), "--internal-unpack-menu-preview", key}
	if withQuery {
		parts = append(parts, "{q}")
	}
	return strings.Join(parts, " ")
}

func chooseUnpackAction() (UnpackMenuAction, error) {
	summary, hasBundle := "", false
	if UnpackMenu.ClipboardSummary != nil {
		summary, hasBundle = UnpackMenu.ClipboardSummary()
	}
	choices := unpackMenuChoices(summary)
	lines, index := startupModifierChoiceLines(choices)
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return "", err
	}
	req := picker.Request{
		Prompt:         "unpack> ",
		WithNth:        "1,3",
		Nth:            "1",
		Header:         unpackMenuHeader(),
		NoSort:         true,
		Lines:          lines,
		PreviewCommand: unpackMenuPreviewCommand("", false),
	}
	if hasBundle {
		// [clipboard] is the last input row; start on it.
		req.Bindings = append(req.Bindings, "load:pos("+strconv.Itoa(len(choices))+")")
	}
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(req))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return "", discovery.ErrSelectionCancelled
	}
	if err != nil {
		return "", err
	}
	if len(result.Matches) == 0 {
		return "", discovery.ErrSelectionCancelled
	}
	choice, ok := index[result.Matches[0]]
	if !ok {
		return "", discovery.ErrSelectionCancelled
	}
	return UnpackMenuAction(choice.Key), nil
}

// chooseUnpackPath is a single path input: fzf with search off, one row to
// press Enter on, and a preview that follows the typed text.
func chooseUnpackPath() (string, error) {
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return "", err
	}
	for {
		platform.StopActiveSpinner()
		result, err := picker.Run(bin, themedFzfRequest(picker.Request{
			Prompt:         "file> ",
			WithNth:        "1",
			Header:         unpackFileHeader(),
			Disabled:       true,
			PrintQuery:     true,
			NoSort:         true,
			Lines:          []string{"[continue]\tgo"},
			PreviewCommand: unpackMenuPreviewCommand("file", true),
			Bindings:       []string{"change:refresh-preview"},
		}))
		if errors.Is(err, picker.ErrSelectionCancelled) {
			return "", discovery.ErrSelectionCancelled
		}
		if err != nil {
			return "", err
		}
		if path := CleanUnpackPath(result.Query); path != "" {
			return path, nil
		}
	}
}

// CleanUnpackPath turns what the user typed, pasted or dragged into a path:
// quotes (Windows Terminal drag-and-drop) and, outside Windows, backslash
// escapes (macOS "My\ File.txt") are removed, and a leading ~ is the home
// folder. Relative paths stay relative to the current folder.
func CleanUnpackPath(raw string) string {
	p := strings.TrimSpace(raw)
	if len(p) >= 2 && (p[0] == '"' && p[len(p)-1] == '"' || p[0] == '\'' && p[len(p)-1] == '\'') {
		p = p[1 : len(p)-1]
	}
	if runtime.GOOS != "windows" && strings.Contains(p, `\`) {
		var b strings.Builder
		for i := 0; i < len(p); i++ {
			if p[i] == '\\' && i+1 < len(p) {
				i++
			}
			b.WriteByte(p[i])
		}
		p = b.String()
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	return p
}

// UnpackPreviewColors are the colors of the unpack menu previews: the theme's
// preview palette, like every other catclip preview (fzf renders the ANSI
// codes even though the preview's stdout is not a terminal), or none with
// NO_COLOR.
func UnpackPreviewColors() platform.Palette {
	if os.Getenv("NO_COLOR") != "" {
		return platform.Palette{}
	}
	return theme.PreviewPalette()
}

// WriteUnpackPreview writes a finished unpack preview, on the theme's
// background when the theme has one, as file previews do.
func WriteUnpackPreview(w io.Writer, text string) error {
	if os.Getenv("FZF_PREVIEW_COLUMNS") != "" && os.Getenv("NO_COLOR") == "" && theme.Current().ID != theme.TerminalID {
		width, _ := strconv.Atoi(os.Getenv("FZF_PREVIEW_COLUMNS"))
		height, _ := strconv.Atoi(os.Getenv("FZF_PREVIEW_LINES"))
		return renderpkg.WriteThemedPreview(w, []byte(text), theme.Current(), width, height)
	}
	_, err := io.WriteString(w, text)
	return err
}
