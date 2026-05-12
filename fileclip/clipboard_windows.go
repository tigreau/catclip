//go:build windows

package fileclip

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ---------------------------------------------------------------------------
// Copy
// ---------------------------------------------------------------------------

// copyPlatform uses PowerShell's Set-Clipboard -Path to place file references
// on the Windows clipboard as CF_HDROP format — the same format Windows
// Explorer uses for Ctrl+C on files.
//
// Note: clip.exe is text-only and cannot set file references.
// Set-Clipboard -Path requires PowerShell 5.1+ (built into Windows 10/11).
func copyPlatform(paths []string) error {
	var psPaths []string
	for _, p := range paths {
		// PowerShell literal strings use single quotes.
		// A single quote inside is escaped by doubling it.
		escaped := strings.ReplaceAll(p, "'", "''")
		psPaths = append(psPaths, fmt.Sprintf("'%s'", escaped))
	}

	script := fmt.Sprintf("Set-Clipboard -Path %s", strings.Join(psPaths, ","))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: powershell not found", ErrToolNotFound)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: powershell: %s", ErrToolFailed, msg)
		}
		return fmt.Errorf("%w: powershell: %v", ErrToolFailed, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Paste
// ---------------------------------------------------------------------------

func pastePlatform() ([]string, error) {
	has, err := hasPlatform()
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrNoFileRefs
	}

	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-Clipboard -Format FileDropList | ForEach-Object { $_.FullName }")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, ErrNoFileRefs
	}

	var paths []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		p := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, ErrNoFileRefs
	}
	return paths, nil
}

// ---------------------------------------------------------------------------
// Has
// ---------------------------------------------------------------------------

func hasPlatform() (bool, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"if (Get-Clipboard -Format FileDropList) { 'true' } else { 'false' }")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout.String()) == "true", nil
}
