package unpack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BlockedError lists files that cannot be written. Nothing was changed.
type BlockedError struct {
	Problems []string
}

func (e BlockedError) Error() string {
	return "cannot write " + fmt.Sprint(len(e.Problems)) + " file(s); nothing was changed:\n  " + strings.Join(e.Problems, "\n  ")
}

// PartialWriteError reports a failure after some files were already written.
type PartialWriteError struct {
	Written    []string
	NotWritten []string
	Failed     string
	Err        error
}

func (e PartialWriteError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "writing %s failed: %v\n", e.Failed, e.Err)
	fmt.Fprintf(&b, "Written before the failure (%d):", len(e.Written))
	for _, p := range e.Written {
		fmt.Fprintf(&b, "\n  %s", p)
	}
	fmt.Fprintf(&b, "\nNot written (%d):", len(e.NotWritten))
	for _, p := range e.NotWritten {
		fmt.Fprintf(&b, "\n  %s", p)
	}
	return b.String()
}

// Check confirms every chosen change can be written before any is: existing
// files open for writing (catching read-only and locked files) and new files'
// nearest existing folder accepts a new file. It leaves no files behind.
func Check(changes []Change) error {
	var problems []string
	probed := map[string]error{}
	for _, c := range changes {
		if c.Kind == KindChanged {
			f, err := os.OpenFile(c.Abs, os.O_WRONLY, 0)
			if err != nil {
				problems = append(problems, c.Path+": "+describe(err))
				continue
			}
			f.Close()
			continue
		}
		dir := filepath.Dir(c.Abs)
		for {
			info, err := os.Stat(dir)
			if err == nil {
				if !info.IsDir() {
					err = fmt.Errorf("%s is not a folder", dir)
				}
				if err == nil {
					if cached, seen := probed[dir]; seen {
						err = cached
					} else {
						err = probeDir(dir)
						probed[dir] = err
					}
				}
				if err != nil {
					problems = append(problems, c.Path+": "+describe(err))
				}
				break
			}
			if !errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, c.Path+": "+describe(err))
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if len(problems) > 0 {
		return BlockedError{Problems: problems}
	}
	return nil
}

func probeDir(dir string) error {
	f, err := os.CreateTemp(dir, ".catclip-unpack-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func describe(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied (read-only, locked by another program, or no access)"
	}
	return err.Error()
}

// Write writes each change through a temporary file in the same folder and
// a rename, so an interrupted write never leaves a half-written file. Bytes
// are written exactly as they appear in the bundle.
func Write(changes []Change) ([]string, error) {
	written := make([]string, 0, len(changes))
	for i, c := range changes {
		if err := writeOne(c); err != nil {
			var rest []string
			for _, r := range changes[i:] {
				rest = append(rest, r.Path)
			}
			return written, PartialWriteError{Written: written, NotWritten: rest, Failed: c.Path, Err: err}
		}
		written = append(written, c.Path)
	}
	return written, nil
}

func writeOne(c Change) error {
	dir := filepath.Dir(c.Abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".catclip-unpack-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(c.Body); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, c.Mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, c.Abs); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
