package render

import (
	"regexp"
	"strings"
	"testing"
)

func TestPreviewLexerUsesNamesAndExplicitShebangs(t *testing.T) {
	cases := []struct{ path, body, want string }{
		{"main.go", "package main\nfunc main() {}\n", "Go"},
		{"src/.clang-format", "---\nIndentWidth: 4\n", "YAML"},
		{"_clang-format", "IndentWidth: 4\n", "YAML"},
		{".clang-tidy", "Checks: '*'\n", "YAML"},
		{"gradlew", "echo starting\n", "Bash"},
		{".mailmap", "var load tool <someone@example.invalid>\n", ""},
		{".gitignore", "tools/\npreload/\nexport/\n", ""},
		{".prettierignore", "#! /bin/sh\nnode_modules/\n", ""},
		{".hiss", "const\nload\n", ""},
		{"notes.unknown", "func _ready():\n    var value = load('x')\n", ""},
		{"launch.unknown", "#!/bin/sh\necho hello\n", "Bash"},
		{"launch.unknown", "#!/usr/bin/env python3\nprint('hello')\n", "Python"},
		{"launch.unknown", "\n#!/usr/bin/env -S python3.13 -u\nprint('hello')\n", "Python"},
		{"launch.unknown", "#!/usr/bin/env --split-string ruby3.4 -w\nputs 'hello'\n", "Ruby"},
		{"launch.unknown", "#!/usr/bin/env node\nconsole.log('hello')\n", "JavaScript"},
		{"launch.unknown", "#!/usr/bin/env fish\necho hello\n", "Fish"},
		{"launch.unknown", "#!/usr/bin/pwsh\nWrite-Output 'hello'\n", "PowerShell"},
		{"launch.unknown", "#!/usr/bin/perl5.40\nprint 'hello';\n", "Perl"},
		{"launch.unknown", "#!/usr/bin/env pypy3\nprint('hello')\n", "Python"},
		{"launch.unknown", "#!/usr/bin/env mysterious\nvar load tool\n", ""},
		{"launch.unknown", "#!/usr/bin/env\nload tool\n", ""},
		{"launch.unknown", "#!\nload tool\n", ""},
		{"launch.unknown", "notes first\n#!/bin/sh\n", ""},
		{"launch.unknown", "#!/usr/bin/env python-malicious\n", ""},
		{"launch.unknown", "#!/bin/sh " + strings.Repeat("x", 600) + "\n", ""},
		{"launch.unknown", "var load tool\n", ""},
	}
	stripANSI := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, tc := range cases {
		t.Run(tc.path+"/"+tc.want, func(t *testing.T) {
			got := lexerForPreview(tc.path, tc.body)
			name := ""
			if got != nil {
				name = got.Config().Name
			}
			if name != tc.want {
				t.Fatalf("lexer=%q want=%q", name, tc.want)
			}
			out := HighlightFilePreviewWithMatches(tc.path, tc.body, "", RenderOptions{PreviewTheme: "fzf-dark"})
			if stripANSI.ReplaceAllString(out, "") != tc.body {
				t.Fatal("highlighting changed body text")
			}
			if tc.want == "" && out != tc.body {
				t.Fatal("uncertain/plain file received syntax colors")
			}
		})
	}
}

func TestPreviewShebangAcceptsCRLF(t *testing.T) {
	if got := previewShebangHint("#!/usr/bin/env -S python3.13 -u\r\nprint('hello')\r\n"); got != "script.py" {
		t.Fatalf("CRLF shebang hint=%q", got)
	}
}

func TestPreviewFilenameOverridesDoNotPoisonSuffixCache(t *testing.T) {
	for _, pair := range [][2]string{{".clang-format", "notes.clang-format"}, {".gitignore", "notes.gitignore"}, {".mailmap", "notes.mailmap"}} {
		if lexerCacheKey(pair[0]) == lexerCacheKey(pair[1]) {
			t.Fatalf("override shares cache key: %v", pair)
		}
		for _, order := range [][2]string{pair, {pair[1], pair[0]}} {
			resetLexerCache()
			for _, p := range order {
				got := lexerForPath(p)
				if p == ".clang-format" {
					if got == nil || got.Config().Name != "YAML" {
						t.Fatalf("lost YAML override in order %v", order)
					}
				} else if got != nil {
					t.Fatalf("unexpected lexer for %s in order %v", p, order)
				}
			}
		}
	}
}

func TestPlainPreviewStillHighlightsContentMatches(t *testing.T) {
	body := "preload/\ntools/\n"
	got := HighlightFilePreviewWithMatches(".gitignore", body, "tools", RenderOptions{})
	if !strings.Contains(got, previewMatchStart+"tools"+previewMatchEnd) {
		t.Fatalf("missing match emphasis: %q", got)
	}
	stripANSI := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	if stripANSI.ReplaceAllString(got, "") != body {
		t.Fatal("match overlay changed plain text")
	}
}
