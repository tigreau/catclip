package render

import (
	"path"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// These exact basenames have a known format that filename matching otherwise
// misses. An empty hint deliberately keeps data/ignore files plain, even if
// their first line resembles a script. This changes display only.
func previewFilenameHint(relPath string) (string, bool) {
	switch path.Base(strings.TrimSpace(relPath)) {
	case ".clang-format", "_clang-format", ".clang-tidy":
		return "config.yaml", true
	case "gradlew":
		return "script.sh", true
	case ".mailmap", ".gitignore", ".ignore", ".rgignore", ".dockerignore", ".prettierignore", ".hiss":
		return "", true
	default:
		return "", false
	}
}

func lexerForPreview(relPath, content string) chroma.Lexer {
	if lexer := lexerForPath(relPath); lexer != nil {
		return lexer
	}
	if _, known := previewFilenameHint(relPath); known {
		return nil
	}
	if hint := previewShebangHint(content); hint != "" {
		// Cache the lexer by its known type, never the shebang decision by the
		// unknown filename: another body with that name may be a different script.
		return lexerForPath(hint)
	}
	return nil
}

var previewVersionedInterpreter = regexp.MustCompile(`^(python|pypy|ruby|perl)([0-9]+(\.[0-9]+)*)?$`)

// Recognize an explicit, bounded first-line interpreter declaration rather
// than running every language's content analyser. No command is executed.
// The optional leading LF is the separator following a <file> preview tag.
func previewShebangHint(content string) string {
	content = strings.TrimPrefix(content, "\n")
	if !strings.HasPrefix(content, "#!") {
		return ""
	}
	const maxShebangBytes = 512
	if len(content) > maxShebangBytes {
		content = content[:maxShebangBytes]
		if !strings.Contains(content, "\n") {
			return ""
		}
	}
	line, _, _ := strings.Cut(content, "\n")
	fields := strings.Fields(line[2:])
	if len(fields) == 0 {
		return ""
	}
	interpreter := path.Base(fields[0])
	if interpreter == "env" {
		fields = fields[1:]
		if len(fields) > 0 && (fields[0] == "-S" || fields[0] == "--split-string") {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			return ""
		}
		interpreter = path.Base(fields[0])
	}
	if match := previewVersionedInterpreter.FindStringSubmatch(interpreter); match != nil {
		switch match[1] {
		case "python", "pypy":
			return "script.py"
		case "ruby":
			return "script.rb"
		case "perl":
			return "script.pl"
		}
	}
	switch interpreter {
	case "sh", "bash", "dash", "ash", "ksh", "zsh":
		return "script.sh"
	case "node", "nodejs", "bun", "deno":
		return "script.js"
	case "fish":
		return "script.fish"
	case "pwsh", "powershell":
		return "script.ps1"
	}
	return ""
}
