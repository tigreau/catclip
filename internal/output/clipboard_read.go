package output

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/fileclip"
	"github.com/tigreau/catclip/internal/platform"
)

// ClipboardContent is what the clipboard holds for a reader such as
// `catclip --unpack`: copied files (a bundle file copied in Explorer or
// Finder, or catclip's own bundle copy) or text. Text uses LF line breaks on
// every platform.
type ClipboardContent struct {
	Files []string
	Text  string
}

// nativeClipboardRead reads the clipboard without a child process. Set by the
// Windows build (text_clipboard_windows.go); nil elsewhere.
var nativeClipboardRead func() (ClipboardContent, error)

// ReadClipboard returns the clipboard's copied files or text. It mirrors how
// catclip writes the clipboard: the Windows API on Windows, one PowerShell call
// under WSL, and the platform tools (fileclip, pbpaste, wl-paste) elsewhere.
func ReadClipboard(plat string, colors platform.Palette) (ClipboardContent, error) {
	var content ClipboardContent
	var err error
	switch {
	case plat == "windows" && nativeClipboardRead != nil:
		content, err = nativeClipboardRead()
	case plat == "wsl":
		content, err = readWSLClipboard(colors)
	case plat == "macos":
		content, err = readToolClipboard("pbpaste", nil, plat, colors)
	default:
		if platform.DetectLinuxSession() != platform.LinuxSessionWayland {
			return ClipboardContent{}, errors.New(
				"Error: Reading the clipboard requires Wayland.\n\n" +
					"No Wayland session was detected. Pass the bundle file instead:\n" +
					"  catclip --unpack FILE")
		}
		content, err = readToolClipboard("wl-paste", []string{"--no-newline"}, plat, colors)
	}
	if err != nil {
		return ClipboardContent{}, err
	}
	content.Text = clipboardTextLF(content.Text)
	return content, nil
}

// clipboardTextLF turns the CRLF (Windows) or CR line breaks some clipboards
// carry into LF, so readers parse one form everywhere.
func clipboardTextLF(text string) string {
	if !strings.Contains(text, "\r") {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// readToolClipboard asks fileclip for copied files first, then reads text
// with the platform's paste tool.
func readToolClipboard(tool string, args []string, plat string, colors platform.Palette) (ClipboardContent, error) {
	if has, _ := fileclip.Has(); has {
		if files, err := fileclip.Paste(); err == nil && len(files) > 0 {
			return ClipboardContent{Files: files}, nil
		}
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return ClipboardContent{}, fmt.Errorf("Error: No clipboard tool found to read the clipboard.\n%s", ClipboardInstallHint(plat, colors))
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// wl-paste exits non-zero on an empty clipboard ("Nothing is
		// copied"); that is an empty clipboard, not a failure.
		if tool == "wl-paste" && stdout.Len() == 0 {
			return ClipboardContent{}, nil
		}
		return ClipboardContent{}, fmt.Errorf("Error: Could not read the clipboard with %s: %s", tool, firstNonEmpty(strings.TrimSpace(stderr.String()), err.Error()))
	}
	return ClipboardContent{Text: stdout.String()}, nil
}

// wslClipboardScript answers in one PowerShell start (~2 s from WSL, measured
// 2026-11-18): "FILES" and one Windows path per line, or "TEXT" and the text.
// The output encoding is set explicitly so non-ASCII text arrives intact.
const wslClipboardScript = `[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$files = Get-Clipboard -Format FileDropList
if ($files) {
  [Console]::Out.Write("FILES` + "`" + `n")
  foreach ($f in $files) { [Console]::Out.Write($f.FullName + "` + "`" + `n") }
} else {
  [Console]::Out.Write("TEXT` + "`" + `n")
  $text = Get-Clipboard -Raw
  if ($text) { [Console]::Out.Write($text) }
}`

func readWSLClipboard(colors platform.Palette) (ClipboardContent, error) {
	path, err := exec.LookPath("powershell.exe")
	if err != nil {
		return ClipboardContent{}, fmt.Errorf("Error: No clipboard tool found to read the clipboard.\n%s", ClipboardInstallHint("wsl", colors))
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(path, "-NoProfile", "-NonInteractive", "-Command", wslClipboardScript)
	// Started from a Linux folder, Windows programs warn about UNC paths.
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ClipboardContent{}, fmt.Errorf("Error: Could not read the clipboard through PowerShell: %s", firstNonEmpty(strings.TrimSpace(stderr.String()), err.Error()))
	}
	return parseWSLClipboardOutput(stdout.String(), wslToLinuxPath), nil
}

func parseWSLClipboardOutput(out string, toLinux func(string) string) ClipboardContent {
	kind, rest, _ := strings.Cut(out, "\n")
	switch strings.TrimSpace(kind) {
	case "FILES":
		var files []string
		for _, line := range strings.Split(rest, "\n") {
			if p := strings.TrimSpace(strings.TrimSuffix(line, "\r")); p != "" {
				files = append(files, toLinux(p))
			}
		}
		return ClipboardContent{Files: files}
	case "TEXT":
		return ClipboardContent{Text: rest}
	}
	return ClipboardContent{}
}

func wslToLinuxPath(windowsPath string) string {
	out, err := exec.Command("wslpath", "-u", windowsPath).Output()
	if err != nil {
		return windowsPath
	}
	return strings.TrimSpace(string(out))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
