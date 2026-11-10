package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/theme"
)

// ChooseTheme only returns a confirmed ID. The explicit top-level action owns
// persistence; Escape and failed sessions cannot change the saved preference.
func ChooseTheme() (selectedID string, chooseErr error) {
	if !platform.IsTerminalFile(os.Stdin) || !platform.IsTerminalFile(os.Stderr) {
		return "", fmt.Errorf("catclip --theme needs an interactive terminal")
	}
	bin, ok := platform.BundledToolBinary("CATCLIP_FZF", "fzf")
	if !ok {
		return "", fmt.Errorf("this catclip install is missing bundled fzf; reinstall catclip with its packaged tools")
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	current := theme.Current().ID
	paletteID, query, position := current, "", 1
	// A theme change relaunches fzf. Hold raw input across the entire loop so
	// arrows do not echo and Enter/Ctrl-C are not translated or handled by the
	// terminal driver between children. fzf remains the only input reader.
	restoreInput, err := platform.BeginPickerInputSession(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("prepare theme picker input: %w", err)
	}
	defer func() {
		if err := restoreInput(); err != nil {
			chooseErr = errors.Join(chooseErr, fmt.Errorf("restore theme picker input: %w", err))
		}
	}()
	for {
		req := themePickerRequest(executable, current)
		selected, _ := theme.Lookup(paletteID)
		if len(req.ColorSpecs) > 0 {
			req.ColorSpecs = selected.InteractiveColorSpecs(true)
		}
		// Sync is limited to this fixed catalog so focus restoration sees all
		// rows. Ordinary project pickers retain their streaming list behavior.
		req.Query, req.PrintQuery, req.Sync = query, true, true
		focusCommand := themeFocusCommand(executable, paletteID)
		if len(req.ColorSpecs) == 0 {
			focusCommand += " inactive"
		}
		action := "bg-cancel+bg-transform(" + focusCommand + ")"
		req.Bindings = []string{
			"enter:bg-cancel+transform(" + themeFocusCommand(executable, paletteID) + " save)",
			"change:bg-cancel",
			// Reset the debounce even at list/preview boundaries, where
			// navigation does not change focus. During save, the list has
			// one fixed row and these events postpone accepting it.
			"up:up+" + action, "down:down+" + action,
			"scroll-up:up+" + action, "scroll-down:down+" + action,
			"preview-scroll-up:preview-up+" + action, "preview-scroll-down:preview-down+" + action,
		}
		if len(req.ColorSpecs) > 0 {
			req.Bindings = append(req.Bindings, "focus:"+action, "result:"+action,
				fmt.Sprintf("load:pos(%d)+bg-cancel", position))
		}
		result, err := picker.Run(bin, req)
		if errors.Is(err, picker.ErrSelectionCancelled) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		id, nextPosition, reload, err := parseThemePickerResult(result)
		if err != nil {
			return "", err
		}
		if !reload {
			return id, nil
		}
		paletteID, position, query = id, nextPosition, result.Query
	}
}

func themePickerRequest(executable, current string) picker.Request {
	choices := theme.Choices()
	lines := make([]string, 0, len(choices))
	for _, c := range choices {
		if c.ID == current {
			lines = append(lines, c.Name+" (current)\t"+c.ID)
		}
	}
	for _, c := range choices {
		if c.ID != current {
			lines = append(lines, c.Name+"\t"+c.ID)
		}
	}
	req := themedFzfRequest(picker.Request{
		Prompt: "theme> ", WithNth: "1", Nth: "", NoSort: true, Lines: lines,
		Header:         "Choose a theme.\nUsed in interactive mode and --print.\n[Up/Down] move  [Enter] save\n[Esc] cancel",
		PreviewCommand: themePreviewCommand(executable),
		PreviewWindow:  "right:70%:wrap:border-rounded:noinfo",
	})
	return req
}
func themePreviewCommand(executable string) string {
	return picker.CommandExecutable(executable) + " --internal-theme-preview {2}"
}

const themeReloadMarker = "__catclip_theme_reload__"
const themeSaveMarker = "__catclip_theme_save__"
const themeSavingPrompt = "saving> "

func themeFocusCommand(executable, paletteID string) string {
	return picker.CommandExecutable(executable) + " --internal-theme-focus " + picker.CommandArg(paletteID) + " {2}"
}

func themeFixedCommand(executable, paletteID, selectedID, mode string) string {
	return picker.CommandExecutable(executable) + " --internal-theme-focus " + picker.CommandArg(paletteID) + " " + picker.CommandArg(selectedID) + " " + picker.CommandArg(mode)
}

func parseThemePickerResult(result picker.Result) (id string, position int, reload bool, err error) {
	// Background focus/result events can queue multiple markers. Coalesce them
	// and use the final accepted row, not an earlier notification's row.
	confirmed := ""
	for _, row := range result.Matches {
		if strings.HasPrefix(row, themeSaveMarker) {
			choice := strings.TrimPrefix(row, themeSaveMarker)
			if !validThemeChoice(choice) || confirmed != "" && confirmed != choice {
				return "", 0, false, fmt.Errorf("invalid confirmed theme")
			}
			confirmed = choice
			continue
		}
		if strings.HasPrefix(row, themeReloadMarker) {
			reload = true
			position, err = strconv.Atoi(strings.TrimPrefix(row, themeReloadMarker))
			if err != nil || position < 1 || position > len(theme.Choices()) {
				return "", 0, false, fmt.Errorf("invalid theme picker position")
			}
			continue
		}
		if !validThemeChoice(row) {
			return "", 0, false, fmt.Errorf("unknown selected theme %q", row)
		}
		id = row
	}
	if confirmed != "" {
		if id == "" {
			return "", 0, false, fmt.Errorf("theme picker saved without a selected row")
		}
		return confirmed, 0, false, nil
	}
	if reload && id == "" {
		return "", 0, false, fmt.Errorf("theme picker relaunched without a selected theme")
	}
	return
}

func validThemeChoice(id string) bool {
	for _, c := range theme.Choices() {
		if c.ID == id {
			return true
		}
	}
	return false
}

// RunInternalThemeFocus debounces palette changes without blocking fzf input.
// fzf cancels this background helper whenever the query/focus changes. It reads
// no settings or files and never persists a theme; Enter remains the save action.
func RunInternalThemeFocus(args []string, stdout io.Writer) error {
	if len(args) < 2 || len(args) > 3 || !validThemeChoice(args[0]) || args[1] != "" && !validThemeChoice(args[1]) {
		return fmt.Errorf("invalid theme focus IDs")
	}
	mode := ""
	if len(args) == 3 {
		mode = args[2]
	}
	switch mode {
	case "", "inactive", "confirm":
	case "row":
		for _, choice := range theme.Choices() {
			if choice.ID == args[1] {
				_, err := fmt.Fprintf(stdout, "%s\t%s\n", choice.Name, choice.ID)
				return err
			}
		}
		return fmt.Errorf("missing theme save row")
	case "save":
		if args[1] == "" {
			return nil
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		confirm := "bg-cancel+bg-transform(" + themeFixedCommand(executable, args[0], args[1], "confirm") + ")"
		if os.Getenv("FZF_PROMPT") == themeSavingPrompt {
			_, err := io.WriteString(stdout, confirm)
			return err
		}
		// Freeze the Enter choice as a single built-in row. fzf continues
		// capturing wheel input, which restarts the quiet-period timer, while
		// Escape/Ctrl-C can still cancel. Nothing is persisted by this helper.
		_, err = fmt.Fprintf(stdout, "unbind(focus,result,change,load)+print(%s%s)+clear-query+disable-search+change-prompt(%s)+hide-input+change-header(Setting theme…\n[Esc] cancel)+reload-sync(%s)+%s",
			themeSaveMarker, args[1], themeSavingPrompt, themeFixedCommand(executable, args[0], args[1], "row"), confirm)
		return err
	default:
		return fmt.Errorf("invalid theme focus mode")
	}
	if mode == "confirm" || os.Getenv("FZF_PROMPT") == themeSavingPrompt {
		// Keep fzf capturing input until navigation settles, so queued arrows
		// or trackpad momentum do not escape into the shell after saving.
		time.Sleep(250 * time.Millisecond)
		_, err := io.WriteString(stdout, "accept")
		return err
	}
	if mode == "inactive" {
		return nil
	}
	if args[1] == "" || args[0] == args[1] {
		return nil
	}
	position, err := strconv.Atoi(os.Getenv("FZF_POS"))
	if err != nil || position < 1 || position > len(theme.Choices()) {
		return fmt.Errorf("invalid theme focus position")
	}
	// Give pending query events time to drain before requesting a relaunch.
	// fzf reports physical mouse events (including wheel input) as click keys.
	// Allow a longer pause for trackpad momentum; only this cancellable helper
	// waits, while the picker retains mouse capture and responds to input.
	delay := 80 * time.Millisecond
	if strings.HasSuffix(os.Getenv("FZF_KEY"), "click") {
		delay = 250 * time.Millisecond
	}
	time.Sleep(delay)
	_, err = fmt.Fprintf(stdout, "print(%s%d)+accept", themeReloadMarker, position)
	return err
}
