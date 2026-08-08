package cli

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestEveryRenderedHelpExampleIsRegistered(t *testing.T) {
	examples, err := RegisteredHelpExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("expected registered help examples")
	}
}

func TestPOSIXHelpPipelinesHaveValidShellSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX help pipeline syntax is covered on Linux and macOS")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	examples, err := RegisteredHelpExamples()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]HelpExample, len(examples))
	for _, example := range examples {
		byID[example.ID] = example
	}
	for _, example := range examples {
		if example.Shell != HelpExampleShellPOSIX && example.Shell != HelpExampleShellMacOS {
			continue
		}
		if example.ID == "shell.replace-fragment" {
			continue
		}
		command := example.Command
		if example.ID == "shell.replace-continuation" {
			command += "\n  " + byID["shell.replace-fragment"].Command
		}
		t.Run(example.ID, func(t *testing.T) {
			if out, err := exec.Command(bash, "-n", "-c", command).CombinedOutput(); err != nil {
				t.Fatalf("invalid shell example: %v\ncommand:\n%s\n%s", err, command, out)
			}
		})
	}
}

func TestEveryHelpExampleHasValidCatclipSyntax(t *testing.T) {
	examples, err := RegisteredHelpExamples()
	if err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		example := example
		t.Run(example.ID, func(t *testing.T) {
			args, ok, err := example.CatclipArgs()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				if example.Kind != HelpExamplePipeline {
					t.Fatalf("example has no catclip command and is not a shell fragment: %q", example.Command)
				}
				return
			}
			if example.Kind == HelpExampleInteractive || example.Kind == HelpExampleStdin {
				return
			}
			if _, err := ParseArgs(args); err != nil {
				t.Fatalf("displayed command does not parse: %v\ncommand: %s\nargv: %#v", err, example.Command, args)
			}
		})
	}
}
