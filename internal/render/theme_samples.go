package render

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/tigreau/catclip/internal/theme"
)

// Fixed, small examples: opening the theme picker never reads the project.
var themeSamples = []struct{ language, source string }{
	{"Go", "// A small greeting\nfunc greet(name string) string {\n    return fmt.Sprintf(\"Hello, %s!\", name)\n}\n"},
	{"TypeScript", "type User = { name: string; active: boolean };\nexport const greet = (user: User): string => {\n    return `Hello, ${user.name}!`;\n};\n"},
	{"JavaScript", "// Load a user asynchronously\nasync function loadUser(id = 42) {\n    const response = await fetch(`/users/${id}`);\n    return response.json();\n}\n"},
	{"Python", "# Keep active users\ndef names(users: list[dict]) -> list[str]:\n    return [u[\"name\"] for u in users if u[\"active\"]]\n"},
	{"Java", "public record User(String name, boolean active) {\n    public String greet() {\n        return \"Hello, \" + name;\n    }\n}\n"},
	{"C#", "public record User(string Name, bool Active);\nvar names = users.Where(u => u.Active)\n                 .Select(u => u.Name).ToList();\n"},
	{"C++", "#include <vector>\n// Sum a collection\nint sum(const std::vector<int>& values) {\n    int total = 0;\n    for (auto value : values) total += value;\n    return total;\n}\n"},
	{"Rust", "fn greet(name: &str) -> String {\n    format!(\"Hello, {}!\", name)\n}\nlet names: Vec<_> = users.iter().map(greet).collect();\n"},
	{"Swift", "struct User {\n    let name: String\n    var active = true\n}\nlet greeting = \"Hello, \\(user.name)!\"\n"},
	{"Kotlin", "data class User(val name: String, val active: Boolean)\nval names = users.filter { it.active }.map { it.name }\n"},
	{"Ruby", "# A friendly greeting\ndef greet(name)\n  puts \"Hello, #{name}!\"\nend\ngreet(\"world\")\n"},
	{"PHP", "<?php\nfunction greet(string $name): string {\n    return \"Hello, {$name}!\";\n}\n"},
	{"Bash", "#!/usr/bin/env bash\nname=\"world\"\nif [[ -n \"$name\" ]]; then\n    printf 'Hello, %s!\\n' \"$name\"\nfi\n"},
	{"SQL", "SELECT name, COUNT(*) AS total\nFROM users\nWHERE active = TRUE\nGROUP BY name ORDER BY total DESC;\n"},
	{"HTML", "<!-- A simple page -->\n<article class=\"greeting\">\n  <h1>Hello, world!</h1>\n  <a href=\"/users\">Browse users</a>\n</article>\n"},
	{"CSS", ".greeting {\n  color: #4078f2;\n  padding: 1rem;\n  display: grid;\n}\n"},
	{"JSON", "{\n  \"name\": \"catclip\",\n  \"enabled\": true,\n  \"languages\": [\"Go\", \"TypeScript\", \"Python\"]\n}\n"},
	{"YAML", "name: catclip\nenabled: true\nlanguages:\n  - Go\n  - TypeScript\n"},
	{"Markdown", "# Project notes\n\nRead **important details** and *examples*.\n- Copy code with `catclip src`.\n- Visit [the project](https://github.com/tigreau/catclip).\n\n> A small example for your next prompt.\n"},
}

// WriteThemeSample renders fixed tree and language examples for the picker.
// fzf honors the erase-to-end-of-line control as a full-line background marker.
// Let it fill the actual viewport width instead of padding rows with spaces.
// Resizes rerun the helper with new FZF_PREVIEW_COLUMNS/LINES values.
func WriteThemeSample(w io.Writer, selected *theme.Theme, width, height int, color bool) error {
	var body bytes.Buffer
	fmt.Fprintf(&body, "%s\nScroll to compare tree and code.\n\n", selected.Name)
	body.WriteString("Tree preview\n")
	treeColors := Palette{}
	if color {
		treeColors = Palette(selected.Palette)
	}
	if err := RenderDocument(&body, themeTreeSample(), DefaultRenderOptions(), treeColors); err != nil {
		return err
	}
	body.WriteByte('\n')
	for _, sample := range themeSamples {
		fmt.Fprintf(&body, "%s\n", sample.language)
		lexer := lexers.Get(sample.language)
		if !color || lexer == nil {
			body.WriteString(sample.source)
		} else {
			tokens, err := lexer.Tokenise(nil, sample.source)
			if err != nil {
				return err
			}
			if err := selected.Formatter().Format(&body, selected.Syntax, tokens); err != nil {
				return err
			}
		}
		body.WriteByte('\n')
	}
	if !color {
		_, err := w.Write(body.Bytes())
		return err
	}
	return WriteThemedPreview(w, body.Bytes(), selected, width, height)
}

// WriteThemedPreview paints a bounded body preview independently of the tree
// pane's terminal background. It never participates in final output.
func WriteThemedPreview(w io.Writer, body []byte, selected *theme.Theme, width, height int) error {
	if selected.ID == theme.TerminalID {
		_, err := w.Write(body)
		return err
	}
	return paintThemedPreview(w, body, selected, width, height)
}

// Bound preview output including ANSI expansion; this cap never clips exports.
const previewCanvasLimit = 256 * 1024

// Width is deliberately handled by fzf. Literal padding can wrap when the
// viewport changes, producing alternating strips of the underlying pane color.
func paintThemedPreview(w io.Writer, body []byte, selected *theme.Theme, _ int, height int) error {
	if height > 1000 {
		height = 1000
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	if _, err := io.WriteString(w, selected.FG+selected.BG); err != nil {
		return err
	}
	written := len(selected.FG) + len(selected.BG)
	for i, line := range lines {
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+selected.FG+selected.BG)
		cost := len(selected.BG) + len("\x1b[0K") + len(line) + 1
		if cost > previewCanvasLimit-written-64 {
			_, err := io.WriteString(w, "\x1b[0m"+selected.FG+selected.BG+"\x1b[0K[preview truncated]\x1b[0m\n")
			return err
		}
		written += cost
		end := "\n"
		if i == len(lines)-1 {
			// A reset after the final newline becomes an extra uncolored row
			// in fzf. Reset within the last painted row instead.
			end = "\x1b[0m\n"
		}
		if _, err := io.WriteString(w, selected.BG+"\x1b[0K"+line+end); err != nil {
			return err
		}
	}
	return nil
}

// A fixed document exercises the same tree renderer and palette as real
// interactive previews, without touching a project or querying Git status.
func themeTreeSample() Document {
	readme, main, settings, tests := int64(870), int64(1536), int64(256), int64(768)
	return Document{
		Mode: DocumentModeTree,
		Entries: []DocumentEntry{
			{Path: "README.md", Size: &readme, GitStatus: "S"},
			{Path: "src/main.go", Size: &main, GitStatus: "M"},
			{Path: "src/settings.json", Size: &settings, GitStatus: "?"},
			{Path: "tests/main_test.go", Size: &tests},
		},
		Summary: &DocumentSummary{Count: 4, Bytes: readme + main + settings + tests, Tokens: 858},
	}
}
