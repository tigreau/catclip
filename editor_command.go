package catclip

import (
	"errors"
	"fmt"
	"strings"
)

func resolveEditorCommandForGOOS(goos, visual, editor string, lookPath func(string) (string, error)) (editorCommand, error) {
	configured, source := configuredEditorValue(visual, editor)
	defaultEditor := defaultEditorNameForGOOS(goos)

	if configured == "" {
		return resolveEditorPartsForGOOS(goos, []string{defaultEditor}, false, lookPath)
	}

	parts, err := splitEditorCommandForGOOS(goos, configured)
	if err != nil {
		return editorCommand{}, fmt.Errorf("Error: could not parse %s as an editor command.", source)
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

func resolveEditorPartsForGOOS(goos string, parts []string, allowDefaultFallback bool, lookPath func(string) (string, error)) (editorCommand, error) {
	path, err := lookPath(parts[0])
	if err == nil {
		return editorCommand{
			Path: path,
			Args: parts[1:],
		}, nil
	}

	if allowDefaultFallback {
		defaultEditor := defaultEditorNameForGOOS(goos)
		if !sameEditorNameForGOOS(goos, parts[0], defaultEditor) {
			if fallbackPath, fallbackErr := lookPath(defaultEditor); fallbackErr == nil {
				return editorCommand{Path: fallbackPath}, nil
			}
		}
	}

	return editorCommand{}, errors.New(editorNotFoundMessageForGOOS(goos))
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
