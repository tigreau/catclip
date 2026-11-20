package unpack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// UnsafePathError refuses the whole bundle: one bad path means it was edited
// by hand, corrupted, or made for somewhere else.
type UnsafePathError struct {
	Path   string
	Reason string
}

func (e UnsafePathError) Error() string {
	return fmt.Sprintf("unsafe path %q in bundle: %s", e.Path, e.Reason)
}

// ValidatePaths checks every path the bundle could write, including skipped
// entries, before any prompt or project read. It reads only the existing
// ancestors of each path under root, to refuse symlinks and junctions.
func ValidatePaths(root string, b Bundle) error {
	// Each existing folder is checked once, however many files share it.
	checked := map[string]bool{}
	for _, e := range b.Entries {
		if err := validatePath(root, e.Path, checked); err != nil {
			return err
		}
		// A move's old path is removed, so it gets the same checks.
		if e.From != "" {
			if err := validatePath(root, e.From, checked); err != nil {
				return err
			}
		}
	}
	for _, s := range b.Skipped {
		if err := validateName(s.Path, runtime.GOOS == "windows"); err != nil {
			return err
		}
	}
	return nil
}

// validatePath walks the path below root with Lstat. A relative path with no
// '..' whose every existing component is a plain folder (and whose target is
// not a link) cannot leave root, so no per-file symlink resolution is needed.
func validatePath(root, rel string, checked map[string]bool) error {
	if err := validateName(rel, runtime.GOOS == "windows"); err != nil {
		return err
	}
	parts := strings.Split(rel, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		last := i == len(parts)-1
		if !last && checked[current] {
			continue
		}
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular != 0 {
			return UnsafePathError{Path: rel, Reason: "it goes through a symbolic link or junction"}
		}
		if !last {
			if !info.IsDir() {
				return UnsafePathError{Path: rel, Reason: strings.Join(parts[:i+1], "/") + " is a file here, not a folder"}
			}
			checked[current] = true
		}
	}
	return nil
}

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validateName checks the relative path text alone.
func validateName(rel string, windows bool) error {
	bad := func(reason string) error { return UnsafePathError{Path: rel, Reason: reason} }
	switch {
	case rel == "":
		return bad("it is empty")
	case strings.HasPrefix(rel, "/"), strings.HasPrefix(rel, `\`), len(rel) >= 2 && rel[1] == ':':
		return bad("it is an absolute path")
	case strings.Contains(rel, `\`):
		return bad("it contains a backslash; bundle paths use forward slashes")
	}
	for _, part := range strings.Split(rel, "/") {
		switch {
		case part == "", part == ".":
			return bad("it has an empty or '.' path segment")
		case part == "..":
			return bad("it points outside the project")
		case strings.EqualFold(part, ".git"):
			return bad("it writes inside .git")
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return bad("it contains a control character")
			}
		}
		if windows {
			if strings.ContainsAny(part, `<>:"|?*`) {
				return bad(`it contains a character Windows does not allow in names (<>:"|?*)`)
			}
			if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return bad("a name ends with a dot or space, which Windows does not allow")
			}
			stem := strings.ToUpper(part)
			if i := strings.IndexByte(stem, '.'); i >= 0 {
				stem = stem[:i]
			}
			if windowsReserved[strings.TrimRight(stem, " ")] {
				return bad("it uses a name Windows reserves (" + part + ")")
			}
		}
	}
	return nil
}
