package picker

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCommandArgEncodesForTheShellNotGo(t *testing.T) {
	for _, tt := range []struct {
		value string
		shell commandShell
		want  string
	}{
		{`C:\path with spaces\scope.json`, commandCmd, `^"C:\path with spaces\scope.json^"`},
		{`%NAME% & file`, commandCmd, `^"^%NAME^% ^& file^"`},
		{`C:\path\`, commandCmd, `^"C:\path\\^"`},
		{`a"b`, commandCmd, `^"a\^"b^"`},
		{`C:\it's $HOME\scope.json`, commandPowerShell, `'C:\it''s $HOME\scope.json'`},
		{`plain`, commandPOSIX, `plain`},
	} {
		if got := quoteCommandArg(tt.value, tt.shell); got != tt.want {
			t.Errorf("shell=%d value=%q got=%q want=%q", tt.shell, tt.value, got, tt.want)
		}
	}
}

func TestCommandArgEscapesLiteralFzfPlaceholders(t *testing.T) {
	for _, value := range []string{"/tmp/{q}/scope.json", "/tmp/{2}/{+f}/{}/file", `/tmp/\{q}/file`} {
		got := CommandArg(value)
		for _, placeholder := range commandPlaceholder.FindAllString(got, -1) {
			if !strings.Contains(got, `\`+placeholder) {
				t.Errorf("unescaped placeholder in %q", got)
			}
		}
	}
	if got := SelectionFilePlaceholder(); got != `"{+f}"` {
		t.Fatalf("dynamic file placeholder changed: %q", got)
	}
}

func TestCommandPOSIXLiteralRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows executors are covered by the real-fzf matrix")
	}
	t.Setenv("CATCLIP_QUOTE_TEST", "expanded")
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		bin, err := exec.LookPath(shell)
		if err != nil {
			if shell == "sh" {
				t.Fatal(err)
			}
			continue
		}
		for _, value := range []string{"", "space path", "$CATCLIP_QUOTE_TEST", "$(printf expanded)", "`printf expanded`", "it's a path", `C:\back\slashes\`, "é [] * % & | ; !", "new\nline\ttab"} {
			out, err := exec.Command(bin, "-c", "printf '%s' "+quoteCommandArg(value, commandPOSIX)).CombinedOutput()
			if err != nil || string(out) != value {
				t.Fatalf("%s value=%q got=%q err=%v", shell, value, out, err)
			}
		}
	}
}
