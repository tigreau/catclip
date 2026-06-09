package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// EditorCommand describes how to invoke the user's editor: a resolved
// absolute path plus any leading arguments (for editors expressed as
// $EDITOR='code --wait' and similar).
type EditorCommand struct {
	Path string
	Args []string
}

// ResolveEditorCommand resolves the user's editor from $VISUAL / $EDITOR
// with the OS-appropriate default fallback.
func ResolveEditorCommand() (EditorCommand, error) {
	return resolveEditorCommandForGOOS(runtime.GOOS, os.Getenv("VISUAL"), os.Getenv("EDITOR"), exec.LookPath)
}

func resolveEditorCommandForGOOS(goos, visual, editor string, lookPath func(string) (string, error)) (EditorCommand, error) {
	configured, source := configuredEditorValue(visual, editor)
	defaultEditor := defaultEditorNameForGOOS(goos)

	if configured == "" {
		return resolveEditorPartsForGOOS(goos, []string{defaultEditor}, false, lookPath)
	}

	parts, err := splitEditorCommandForGOOS(goos, configured)
	if err != nil {
		return EditorCommand{}, fmt.Errorf("Error: could not parse %s as an editor command.", source)
	}
	if len(parts) == 0 {
		return resolveEditorPartsForGOOS(goos, []string{defaultEditor}, false, lookPath)
	}
	return resolveEditorPartsForGOOS(goos, parts, true, lookPath)
}

func configuredEditorValue(visual, editor string) (string, string) {
	if visual != "" {
		return visual, "$VISUAL"
	}
	if editor != "" {
		return editor, "$EDITOR"
	}
	return "", ""
}

func resolveEditorPartsForGOOS(goos string, parts []string, allowDefaultFallback bool, lookPath func(string) (string, error)) (EditorCommand, error) {
	path, err := lookPath(parts[0])
	if err == nil {
		return EditorCommand{
			Path: path,
			Args: parts[1:],
		}, nil
	}

	if allowDefaultFallback {
		defaultEditor := defaultEditorNameForGOOS(goos)
		if !sameEditorNameForGOOS(goos, parts[0], defaultEditor) {
			if fallbackPath, fallbackErr := lookPath(defaultEditor); fallbackErr == nil {
				return EditorCommand{Path: fallbackPath}, nil
			}
		}
	}

	return EditorCommand{}, errors.New(editorNotFoundMessageForGOOS(goos))
}

func defaultEditorNameForGOOS(goos string) string {
	if goos == "windows" {
		return "notepad.exe"
	}
	return "nano"
}

func editorNotFoundMessageForGOOS(goos string) string {
	if goos == "windows" {
		return "Error: no editor found. Set $EDITOR or ensure notepad.exe is available."
	}
	return "Error: no editor found. Set $EDITOR or install nano."
}

func sameEditorNameForGOOS(goos, left, right string) bool {
	if goos == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func splitEditorCommandForGOOS(goos, command string) ([]string, error) {
	if strings.TrimSpace(command) == "" {
		return nil, nil
	}
	if goos == "windows" {
		return splitWindowsEditorCommand(command)
	}
	return strings.Fields(command), nil
}
