//go:build darwin

package fileclip

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func copyPlatform(paths []string) error {
	var scriptBuilder strings.Builder
	scriptBuilder.WriteString(`use framework "AppKit"
use framework "Foundation"
set pb to current application's NSPasteboard's generalPasteboard()
pb's clearContents()
set urlList to current application's NSMutableArray's array()
`)

	for _, p := range paths {
		escaped := strings.ReplaceAll(p, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		scriptBuilder.WriteString(fmt.Sprintf(`urlList's addObject:(current application's NSURL's fileURLWithPath:"%s")`+"\n", escaped))
	}

	scriptBuilder.WriteString(`pb's writeObjects:urlList`)

	cmd := exec.Command("osascript")
	cmd.Stdin = strings.NewReader(scriptBuilder.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: osascript not found", ErrToolNotFound)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: osascript: %s", ErrToolFailed, msg)
		}
		return fmt.Errorf("%w: osascript: %v", ErrToolFailed, err)
	}
	return nil
}

func pastePlatform() ([]string, error) {
	has, err := hasPlatform()
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrNoFileRefs
	}

	// Try the NSPasteboard ObjC bridge first (handles multiple files).
	script := `
use framework "AppKit"
set pb to current application's NSPasteboard's generalPasteboard()
set urls to pb's readObjectsForClasses:{current application's NSURL} options:(missing value)
if urls is missing value or (count of urls) is 0 then
	return ""
end if
set output to ""
repeat with u in urls
	set output to output & (u's |path|() as text) & linefeed
end repeat
return output
`
	cmd := exec.Command("osascript")
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		paths, err := parsePathList(stdout.String())
		if err == nil {
			return paths, nil
		}
	}

	// Fall back: try reading as «class furl» (single file only).
	return pastePlatformSimple()
}

// pastePlatformSimple is a fallback that reads a single file reference
// using plain AppleScript (no Objective-C bridge).
func pastePlatformSimple() ([]string, error) {
	cmd := exec.Command("osascript")
	cmd.Stdin = strings.NewReader(`POSIX path of (the clipboard as «class furl»)`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, ErrNoFileRefs
	}

	return parsePathList(stdout.String())
}

func parsePathList(raw string) ([]string, error) {
	var paths []string
	for _, line := range strings.Split(raw, "\n") {
		p := strings.TrimSpace(line)
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, ErrNoFileRefs
	}
	return paths, nil
}

func hasPlatform() (bool, error) {
	script := `
use framework "AppKit"
set pb to current application's NSPasteboard's generalPasteboard()
if pb's types() is missing value then return false
set theTypes to pb's types() as list
return (theTypes contains "public.file-url") or (theTypes contains "NSFilenamesPboardType") or (theTypes contains "CorePasteboardFlavorType 0x6675726C")
`
	cmd := exec.Command("osascript")
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		out := strings.TrimSpace(stdout.String())
		if out == "true" {
			return true, nil
		} else if out == "false" {
			return false, nil
		}
	}

	// Fallback to simple clipboard info for single files
	cmd = exec.Command("osascript")
	cmd.Stdin = strings.NewReader("clipboard info")
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return false, fmt.Errorf("%w: osascript: %s", ErrToolFailed, msg)
		}
		return false, fmt.Errorf("%w: osascript: %v", ErrToolFailed, err)
	}

	// «class furl» indicates file URL references on the clipboard.
	return strings.Contains(stdout.String(), "«class furl»"), nil
}
