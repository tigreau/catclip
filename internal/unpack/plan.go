package unpack

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// ChangeKind classifies one bundle file against the local project.
type ChangeKind string

const (
	KindNew       ChangeKind = "new"
	KindChanged   ChangeKind = "changed"
	KindIdentical ChangeKind = "identical"
)

// Change is a bundle file that differs from (or is missing in) the project.
type Change struct {
	Kind  ChangeKind
	Path  string
	Abs   string
	Body  []byte
	Local []byte
	Mode  fs.FileMode
}

// Plan is what unpacking would do, computed before anything is written.
type Plan struct {
	Root      string
	Changes   []Change
	Identical int
	Skipped   []Skipped
}

// compareReaders bounds concurrent local reads; results keep bundle order.
const compareReaders = 8

type comparison struct {
	change  Change
	kind    ChangeKind
	skipped string
	err     error
}

// BuildPlan compares each complete bundle file with the same relative path
// under root. It reads only those exact paths, a few at a time.
func BuildPlan(root string, b Bundle) (Plan, error) {
	p := Plan{Root: root, Skipped: append([]Skipped(nil), b.Skipped...)}
	results := make([]comparison, len(b.Entries))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(compareReaders, len(b.Entries)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = compare(root, b.Entries[i])
			}
		}()
	}
	for i := range b.Entries {
		next <- i
	}
	close(next)
	wg.Wait()
	for i, r := range results {
		switch {
		case r.err != nil:
			return Plan{}, r.err
		case r.skipped != "":
			p.Skipped = append(p.Skipped, Skipped{Path: b.Entries[i].Path, Reason: r.skipped})
		case r.kind == KindIdentical:
			p.Identical++
		default:
			p.Changes = append(p.Changes, r.change)
		}
	}
	return p, nil
}

func compare(root string, e Entry) comparison {
	abs := filepath.Join(root, filepath.FromSlash(e.Path))
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return comparison{kind: KindNew, change: Change{Kind: KindNew, Path: e.Path, Abs: abs, Body: e.Body, Mode: 0o644}}
	case err != nil:
		return comparison{err: err}
	case info.IsDir():
		return comparison{skipped: "a folder with this name exists here"}
	}
	local, err := os.ReadFile(abs)
	if err != nil {
		return comparison{err: err}
	}
	if bytes.Equal(local, e.Body) {
		return comparison{kind: KindIdentical}
	}
	if bytes.IndexByte(local, 0) >= 0 {
		return comparison{skipped: "your copy is a binary file"}
	}
	return comparison{kind: KindChanged, change: Change{Kind: KindChanged, Path: e.Path, Abs: abs, Body: e.Body, Local: local, Mode: info.Mode().Perm()}}
}

// Counts returns how many changes are new and how many replace a file.
func (p Plan) Counts() (changed, added int) {
	for _, c := range p.Changes {
		if c.Kind == KindNew {
			added++
		} else {
			changed++
		}
	}
	return changed, added
}
