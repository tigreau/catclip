package render

import (
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/tigreau/catclip/internal/theme"
)

// These languages are presentation choices for the save confirmation only.
// Every variant in a family shares its language; previews still support all
// languages. The snippets are highlighted, never executed.
func themeConfirmationSource(selected *theme.Theme) (language, source string) {
	switch selected.ID {
	case theme.TerminalID:
		language, source = "Bash", `theme={{theme}}
printf '%s saved.\n' "$theme"
printf '%s\n' 'Applies to interactive mode and terminal --print.'
`
	case "xcode-dark", "xcode":
		language, source = "Swift", `let theme = {{theme}}
print("\(theme) saved.")
print("Applies to interactive mode and terminal --print.")
`
	case "darcula", "jetbrains-light":
		language, source = "Java", `String theme = {{theme}};
System.out.println(theme + " saved.");
System.out.println("Applies to interactive mode and terminal --print.");
`
	case "monokai", "monokailight":
		language, source = "Ruby", `theme = {{theme}}
puts "#{theme} saved."
puts "Applies to interactive mode and terminal --print."
`
	case "vscode-dark", "vscode-light":
		language, source = "TypeScript", `const theme: string = {{theme}};
console.log(theme, "saved.");
console.log("Applies to interactive mode and terminal --print.");
`
	case "github-dark", "github":
		language, source = "Python", `theme = {{theme}}
print(f"{theme} saved.")
print("Applies to interactive mode and terminal --print.")
`
	case "onedark", "atom-one-light":
		language, source = "JavaScript", `const theme = {{theme}};
console.log(theme, "saved.");
console.log("Applies to interactive mode and terminal --print.");
`
	case "catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha":
		language, source = "Rust", `let theme = {{theme}};
println!("{theme} saved.");
println!("Applies to interactive mode and terminal --print.");
`
	default: // Solarized uses Go; future themes also have a valid default.
		language, source = "Go", `theme := {{theme}}
fmt.Printf("%s saved.\n", theme)
fmt.Println("Applies to interactive mode and terminal --print.")
`
	}
	return language, strings.ReplaceAll(source, "{{theme}}", strconv.Quote(selected.Name))
}

// WriteThemeConfirmation uses the explicitly saved choice: Current still holds
// the theme cached before the picker opened. Only this fixed snippet is lexed.
func WriteThemeConfirmation(w io.Writer, selected *theme.Theme, color bool) (err error) {
	if color {
		defer func() {
			if err != nil {
				_, _ = io.WriteString(w, selected.Palette.Reset)
			}
		}()
	}
	language, source := themeConfirmationSource(selected)
	sample := source
	if color {
		if lexer := lexers.Get(language); lexer != nil {
			if tokens, err := lexer.Tokenise(nil, source); err == nil {
				var highlighted bytes.Buffer
				if err := selected.Formatter().Format(&highlighted, selected.Syntax, tokens); err == nil && printColorPreservesText(highlighted.String(), source) {
					sample = highlighted.String()
				}
			}
		}
	}
	if !color {
		_, err := io.WriteString(w, sample)
		return err
	}
	return WriteThemedPreview(w, []byte(sample), selected, 0, 0)
}
