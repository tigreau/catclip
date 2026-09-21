package picker

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type commandShell uint8

const (
	commandPOSIX commandShell = iota
	commandCmd
	commandPowerShell
)

// Match fzf's automatic executor selection, not the user's terminal application.
// Do not set --with-shell: pinned fzf takes a different quoting branch there.
func currentCommandShell() commandShell {
	if runtime.GOOS != "windows" {
		return commandPOSIX
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		return commandCmd
	}
	base := filepath.Base(shell)
	switch {
	case strings.HasPrefix(base, "cmd"):
		return commandCmd
	case strings.HasPrefix(base, "powershell"), strings.HasPrefix(base, "pwsh"):
		return commandPowerShell
	default:
		return commandPOSIX
	}
}

// CommandExecutable quotes a fixed executable and, where necessary, invokes
// it. This is a command prefix, not an argument for exec.Command.
func CommandExecutable(path string) string {
	if currentCommandShell() == commandCmd {
		// cmd parses the executable position differently from native operands.
		// PrepareCommand supplies this path through a quoted environment value;
		// expansion is once-only, so percent characters in the path stay literal.
		return "__catclip_exe_" + base64.RawURLEncoding.EncodeToString([]byte(path)) + "__ " + commandContextFlag
	}
	quoted := CommandArg(path)
	if currentCommandShell() == commandPowerShell {
		return "& " + quoted + " " + commandContextFlag
	}
	return quoted + " " + commandContextFlag
}

// fzf parses templates before the shell sees them, including inside quotes.
// Escape only recognized placeholder forms; an ordinary brace is literal.
var commandPlaceholder = regexp.MustCompile(`\{(?:[+*sfr]*[0-9,-.]*|q(?::s?[0-9,-.]+)?|fzf:(?:query|action|prompt)|[+*]?f?nf?)\}`)

// CommandArg protects a fixed operand across both fzf template substitution
// and shell parsing. Ordinary dynamic placeholders must NOT use this function.
func CommandArg(value string) string {
	if value == "" && currentCommandShell() == commandPowerShell && strings.HasPrefix(filepath.Base(os.Getenv("SHELL")), "powershell") {
		// Windows PowerShell's legacy native binder drops an empty string.
		return `'""'`
	}
	quoted := quoteCommandArg(value, currentCommandShell())
	return commandPlaceholder.ReplaceAllStringFunc(quoted, func(s string) string { return `\` + s })
}

// QuerySourceMarker preserves fzf's {q} refresh semantics while allowing the
// helper to recover exact query bytes from FZF_QUERY. PowerShell 5 drops empty
// native arguments; PowerShell 7's binder can preserve fzf's escape backslashes.
const QuerySourceMarker = "--internal-query-env"

// SelectionFilePlaceholder is fzf's raw filename exception. Unlike ordinary
// fields, it is not shell-quoted by fzf. PrepareCommand keeps the substituted
// name relative on Unix. Windows makes it absolute again: these quotes protect
// spaces/backslashes but cannot protect every shell-special temp-root character.
func SelectionFilePlaceholder() string { return `"{+f}"` }

func quoteCommandArg(value string, shell commandShell) string {
	if shell == commandPowerShell {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `"`, `\"`), "'", "''") + "'"
	}
	if shell == commandCmd {
		// Native argv encoding followed by cmd metacharacter escaping, matching
		// fzf 0.74.1's Windows QuoteEntry boundary. Go string literals are not
		// Windows command-line literals: ordinary backslashes must stay single.
		var b strings.Builder
		b.WriteByte('"')
		slashes := 0
		for _, c := range value {
			if c == '"' {
				b.WriteString(strings.Repeat(`\`, slashes+1))
			}
			b.WriteRune(c)
			if c == '\\' {
				slashes++
			} else {
				slashes = 0
			}
		}
		b.WriteString(strings.Repeat(`\`, slashes))
		b.WriteByte('"')
		return escapeCmd(b.String())
	}
	if value == "" {
		return `""`
	}
	plain := true
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./:-", c)) {
			plain = false
			break
		}
	}
	if plain {
		return value
	}
	if !strings.ContainsAny(value, "$`\\\"!\r\n") {
		return `"` + value + `"`
	}
	return "'" + strings.NewReplacer("'", `'"'"'`, "\\", `'"\\"'`).Replace(value) + "'"
}

func escapeCmd(value string) string {
	var b strings.Builder
	for _, c := range value {
		if strings.ContainsRune(`&|<>()^%!"`, c) {
			b.WriteByte('^')
		}
		b.WriteRune(c)
	}
	return b.String()
}
