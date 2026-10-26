package catclip

import (
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/platform"
)

// Error rendering also runs before parsing succeeds. Honor the real headless
// flag without mistaking a regex/internal fixed-arity operand for that flag.
func diagnosticHeadless(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--headless" {
			return true
		}
		i += min(cli.FixedValueCount(args[i]), len(args)-i-1)
	}
	return false
}

// styleDiagnosticMessage adds presentation only, preserving wording, spacing,
// exit semantics and existing hand-styled diagnostics. A zero palette also
// removes Catclip SGR styling inherited from a producer on a plain-text route.
func styleDiagnosticMessage(message string, colors platform.Palette, isError bool) string {
	if colors.Reset == "" {
		return stripDiagnosticSGR(message)
	}
	if strings.Contains(message, "\x1b[") {
		return message
	}
	lines := strings.Split(message, "\n")
	first := true
	for i, line := range lines {
		text := strings.TrimLeft(line, " \t")
		if text == "" {
			continue
		}
		indent := line[:len(line)-len(text)]
		switch {
		case strings.HasPrefix(text, "Error:"):
			lines[i] = indent + colors.Bold + colors.Err + "Error:" + colors.Reset + colors.Err + text[len("Error:"):] + colors.Reset
		case strings.HasPrefix(text, "Warning:"):
			lines[i] = indent + colors.Warn + text + colors.Reset
		case first:
			color := colors.Warn
			if isError {
				color = colors.Err
			}
			lines[i] = indent + color + text + colors.Reset
		default:
			commandAt := -1
			if strings.HasPrefix(text, "catclip ") {
				commandAt = 0
			} else {
				for _, label := range []string{"Try:", "Example:", "See every active rule:"} {
					if strings.HasPrefix(text, label) {
						rest := strings.TrimLeft(text[len(label):], " \t")
						if strings.HasPrefix(rest, "catclip ") {
							commandAt = len(text) - len(rest)
						}
					}
				}
			}
			if commandAt >= 0 {
				lines[i] = indent + colors.Dim + text[:commandAt] + colors.Reset + colors.OK + text[commandAt:] + colors.Reset
			} else {
				lines[i] = indent + colors.Dim + text + colors.Reset
			}
		}
		first = false
	}
	return strings.Join(lines, "\n")
}

// Strip SGR color/style sequences only. Keep all ordinary message bytes and
// avoid a new terminal-formatting dependency or regex work on successful runs.
func stripDiagnosticSGR(message string) string {
	if !strings.Contains(message, "\x1b[") {
		return message
	}
	var b strings.Builder
	b.Grow(len(message))
	for i := 0; i < len(message); {
		if message[i] == '\x1b' && i+1 < len(message) && message[i+1] == '[' {
			end := i + 2
			for end < len(message) && ((message[end] >= '0' && message[end] <= '9') || message[end] == ';' || message[end] == ':') {
				end++
			}
			if end < len(message) && message[end] == 'm' {
				i = end + 1
				continue
			}
		}
		b.WriteByte(message[i])
		i++
	}
	return b.String()
}

func styleErrorMessage(message string, args []string, colors platform.Palette) string {
	if diagnosticHeadless(args) {
		colors = platform.Palette{}
	}
	return styleDiagnosticMessage(message, colors, true)
}
