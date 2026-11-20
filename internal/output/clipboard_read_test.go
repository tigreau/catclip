package output

import (
	"reflect"
	"strings"
	"testing"
)

func TestClipboardTextLF(t *testing.T) {
	for in, want := range map[string]string{
		"a\r\nb\r\n": "a\nb\n",
		"a\rb":       "a\nb",
		"a\nb":       "a\nb",
		"a—b":        "a—b",
	} {
		if got := clipboardTextLF(in); got != want {
			t.Errorf("clipboardTextLF(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseWSLClipboardOutput(t *testing.T) {
	toLinux := func(p string) string { return "/mnt/c/" + strings.ReplaceAll(strings.TrimPrefix(p, `C:\`), `\`, "/") }
	got := parseWSLClipboardOutput("FILES\n"+`C:\Users\me\reply.txt`+"\r\n"+`C:\b.txt`+"\n", toLinux)
	if want := (ClipboardContent{Files: []string{"/mnt/c/Users/me/reply.txt", "/mnt/c/b.txt"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("files: got %+v, want %+v", got, want)
	}
	got = parseWSLClipboardOutput("TEXT\n<file path=\"a\">\n— x\n</file>\n", toLinux)
	if got.Text != "<file path=\"a\">\n— x\n</file>\n" || got.Files != nil {
		t.Fatalf("text: got %+v", got)
	}
	if got := parseWSLClipboardOutput("TEXT\n", toLinux); got.Text != "" {
		t.Fatalf("empty: got %+v", got)
	}
	if got := parseWSLClipboardOutput("", toLinux); got.Text != "" || got.Files != nil {
		t.Fatalf("no output: got %+v", got)
	}
}
