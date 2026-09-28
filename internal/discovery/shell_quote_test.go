package discovery

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestShellQuoteFixedOperandsStayLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX/fish quoting contract; native Windows launcher is a separate workstream")
	}
	t.Setenv("CATCLIP_QUOTE_LITERAL", "must-not-expand")
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		bin, err := exec.LookPath(shell)
		if err != nil {
			if shell == "sh" {
				t.Fatal(err)
			}
			continue
		}
		t.Run(shell, func(t *testing.T) {
			for _, value := range []string{"", "plain", "space path", "~", "#comment", "=sh", "^prefix", "/tmp/$CATCLIP_QUOTE_LITERAL/state.json", "`printf expanded`", "$(printf expanded)", "quote'and\"double", `C:\path\back\\slashes\`, "newline\nand\ttab", "雪 é ! & | ; < > ( ) { } [ ] * ?"} {
				out, err := exec.Command(bin, "-c", "printf '%s' "+ShellQuoteArg(value)).CombinedOutput()
				if err != nil || string(out) != value {
					t.Fatalf("value %q: got %q err=%v", value, out, err)
				}
			}
		})
	}
	if got := ShellQuoteArg("{+f}"); got != `"{+f}"` {
		t.Fatalf("raw fzf file placeholder exception lost: %s", got)
	}
}
