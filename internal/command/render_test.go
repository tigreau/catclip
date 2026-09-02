package command

import (
	"bytes"
	"encoding/base64"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalResolvedInvocationCommandRoundTripsPOSIXShellArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell replay test")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	target := "src/$draft's file.ts"
	pattern := `can't match $HOME or *`
	invocation := Resolved{
		Config: Invocation{Platform: "linux"},
		Scopes: []ExecutionScope{{
			Targets: []string{target},
			Stages: []Stage{{
				Kind:   StageContains,
				Values: []string{pattern},
			}},
		}},
	}

	rendered := CanonicalResolvedInvocationCommand(invocation, RenderFlags{})
	script := "set -- " + strings.TrimPrefix(rendered, "catclip ") + `; printf '%s\0' "$@"`
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("replaying canonical arguments through sh: %v\ncommand: %s", err, rendered)
	}
	got := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	want := [][]byte{[]byte(target), []byte("--contains"), []byte(pattern)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shell replay changed argv\nrendered: %s\ngot:  %q\nwant: %q", rendered, got, want)
	}
}

func TestCanonicalGlobalArgsRendersNeverEmitPolicy(t *testing.T) {
	got := CanonicalGlobalArgs(Invocation{}, RenderFlags{EmissionPolicy: EmissionNever})
	if !reflect.DeepEqual(got, []string{"--no"}) {
		t.Fatalf("CanonicalGlobalArgs() = %#v, want [--no]", got)
	}
}

func TestCanonicalResolvedInvocationCommandUsesPowerShellLiteralQuoting(t *testing.T) {
	target := `src\Chris's $draft file.ts`
	pattern := `can't match $HOME or *`
	invocation := Resolved{
		Config: Invocation{Platform: "windows"},
		Scopes: []ExecutionScope{{
			Targets: []string{target},
			Stages: []Stage{{
				Kind:   StageSnippet,
				Values: []string{pattern},
			}},
		}},
	}

	got := CanonicalResolvedInvocationCommand(invocation, RenderFlags{})
	want := `catclip 'src\Chris''s $draft file.ts' --snippet 'can''t match $HOME or *'`
	if got != want {
		t.Fatalf("PowerShell command\n got: %s\nwant: %s", got, want)
	}

	powershell, err := exec.LookPath("pwsh")
	if err != nil {
		powershell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell not available for executable replay check")
	}
	script := `[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); ` +
		`function catclip { foreach ($value in $args) { ` +
		`[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$value)) } }; ` + got
	out, err := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		t.Fatalf("replaying canonical arguments through PowerShell: %v\ncommand: %s", err, got)
	}
	lines := strings.Fields(string(out))
	decoded := make([]string, 0, len(lines))
	for _, line := range lines {
		value, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			t.Fatalf("decoding PowerShell argv %q: %v", line, err)
		}
		decoded = append(decoded, string(value))
	}
	if wantArgs := []string{target, "--snippet", pattern}; !reflect.DeepEqual(decoded, wantArgs) {
		t.Fatalf("PowerShell replay changed argv\nrendered: %s\ngot:  %q\nwant: %q", got, decoded, wantArgs)
	}
}
