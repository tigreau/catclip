package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/theme"
)

func tokenLimitPresets() []int64 {
	return []int64{4000, 8000, 16000, 32000, 64000, command.DefaultTokenLimit, 200000, 500000}
}

func isTokenLimitPreset(n int64) bool {
	for _, preset := range tokenLimitPresets() {
		if n == preset {
			return true
		}
	}
	return false
}

func chooseStartupTokenLimit(args []string) (int64, error) {
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return 0, err
	}
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return 0, err
	}
	result, err := picker.Run(bin, tokenLimitPickerRequest(self, cfg.TokenLimit))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return 0, discovery.ErrSelectionCancelled
	}
	if err != nil {
		return 0, err
	}
	if len(result.Matches) != 1 {
		return 0, discovery.ErrSelectionCancelled
	}
	n, err := strconv.ParseInt(result.Matches[0], 10, 64)
	if err != nil || !isTokenLimitPreset(n) {
		return 0, fmt.Errorf("invalid token limit preset: %q", result.Matches[0])
	}
	return n, nil
}

func tokenLimitPickerRequest(self string, current int64) picker.Request {
	presets := tokenLimitPresets()
	var lines []string
	focus := 1
	// Reverse input so the visible menu reads from the smallest amount at
	// the top to the largest at the bottom, with the shared default focused.
	for i := len(presets) - 1; i >= 0; i-- {
		n := presets[i]
		value := strconv.FormatInt(n, 10)
		// Keep the searchable amount identical to the CLI value. Commas in
		// the visible label prevent an exact numeric query such as 64000.
		label := value + " tokens"
		if n == command.DefaultTokenLimit {
			label += " (default)"
		}
		if n == current {
			label += " (current)"
		}
		lines = append(lines, label+"\t"+value)
		if n == current || !isTokenLimitPreset(current) && n == command.DefaultTokenLimit {
			focus = len(lines)
		}
	}
	return themedFzfRequest(picker.Request{
		Prompt: "token-limit> ", WithNth: "1", Nth: "1", NoSort: true, Exact: true, Sync: true,
		Lines:          lines,
		Header:         discovery.PickerHeader("Choose a token limit.", "One budget for the whole command.", "Type to find a preset.", "[Up/Down] move  [Enter] confirm  [Esc] back"),
		Bindings:       []string{fmt.Sprintf("load:pos(%d)", focus)},
		PreviewCommand: tokenLimitPreviewCommand(self), PreviewWindow: picker.DefaultPreviewWindow,
	})
}

func tokenLimitPreviewCommand(self string) string {
	return strings.Join([]string{picker.CommandExecutable(self), "--internal-token-limit-preview", "{2}"}, " ")
}

func formatTokenLimitAmount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func RunInternalTokenLimitPreview(n int64, stdout io.Writer) error {
	if !isTokenLimitPreset(n) {
		return fmt.Errorf("invalid token limit preset")
	}
	colors := theme.PreviewPalette()
	// Illustrate bytes/4 using five-letter English words plus a space.
	// Round to hundreds; this comparison needs no source scan or tokenizer.
	words := (n*4/6 + 50) / 100 * 100
	text := colors.Bold + colors.Value + "Limit: about " + formatTokenLimitAmount(n) + " tokens" + colors.Reset + "\n" +
		"Roughly " + formatTokenLimitAmount(words) + " average English words. Code varies.\n\n" +
		"One limit for all selected files, including --then.\n" +
		"If needed, stops inside a file and tells you where.\n\n" +
		"The limit counts the text inside your files.\n" +
		"Added filenames, line numbers and the final notice\n" +
		"do not count, so the complete message can be larger.\n\n" +
		"With --paths or --metadata, the limit counts\n" +
		"the list or report instead of file contents.\n"
	width, _ := strconv.Atoi(os.Getenv("FZF_PREVIEW_COLUMNS"))
	height, _ := strconv.Atoi(os.Getenv("FZF_PREVIEW_LINES"))
	return render.WriteThemedPreview(stdout, []byte(text), theme.Current(), width, height)
}

// Replace only the global budget; fixed-arity operands can themselves look
// like flags. Other argument and scope state remains owned by its frame.
func appendStartupGlobalOptions(current, extra []string) []string {
	out := make([]string, 0, len(current)+len(extra))
	for i := 0; i < len(current); i++ {
		arg := current[i]
		if argsContain(extra, "--token-limit") && (arg == "--token-limit" || arg == "-tl") {
			_, next, _ := cli.ConsumeTokenLimit(current, i+1)
			i = next - 1
			continue
		}
		end := min(len(current), i+1+cli.FixedValueCount(arg))
		out = append(out, current[i:end]...)
		i = end - 1
	}
	return append(out, extra...)
}
