package unpack

import (
	"bytes"
	"errors"
	"fmt"
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
// Body is the complete new content, after line edits and line endings.
type Change struct {
	Kind  ChangeKind
	Path  string
	Abs   string
	Body  []byte
	Local []byte
	Mode  fs.FileMode
	// LineEdits marks a file changed by excerpt edits, not replaced whole.
	LineEdits bool
}

// Plan is what unpacking would do, computed before anything is written.
type Plan struct {
	Root      string
	Changes   []Change
	Identical int
	Skipped   []Skipped
	// Notes is the reply's text outside the entries (Bundle.NoteText).
	Notes string
}

// compareReaders bounds concurrent local reads; results keep bundle order.
const compareReaders = 8

type comparison struct {
	change  Change
	kind    ChangeKind
	problem Problem // set when the entry cannot be applied
	detail  string  // what is specific to this file
	err     error
}

// BuildPlan compares each complete bundle file with the same relative path
// under root. It reads only those exact paths, a few at a time.
func BuildPlan(root string, b Bundle) (Plan, error) {
	p := Plan{Root: root, Skipped: append([]Skipped(nil), b.Skipped...), Notes: b.NoteText()}
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
		case r.problem != "":
			p.Skipped = append(p.Skipped, Skipped{Path: b.Entries[i].Path, Problem: r.problem, Detail: r.detail, Blocking: true})
		case r.kind == KindIdentical:
			p.Identical++
		default:
			p.Changes = append(p.Changes, r.change)
		}
	}
	return p, nil
}

// compare works out the new content of one file. Line endings (user,
// 2026-11-18): an existing file keeps its own (CRLF stays CRLF, LF stays LF,
// so unpacking never converts a file or mixes endings in it); a new file is
// written with LF.
func compare(root string, e Entry) comparison {
	abs := filepath.Join(root, filepath.FromSlash(e.Path))
	edits := e.Edits != nil
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if edits && !createsFile(e.Edits) {
			// An excerpt edits an existing file. A missing one almost always
			// means --unpack runs in another folder than the copy was made
			// in (frontend/ vs the repository root); creating a file that
			// holds only the excerpt would be wrong. Agents create new files
			// with whole-file entries, or (seen from Haiku, 2026-11-19) with
			// an excerpt tag declaring out-of="0", which is unambiguous.
			return comparison{problem: ProblemMissing}
		}
		if edits {
			if line := shortenedPlaceholder(e, nil); line != "" {
				return comparison{problem: ProblemShortened, detail: line}
			}
			return comparison{kind: KindNew, change: Change{Kind: KindNew, Path: e.Path, Abs: abs, Body: applyLineEdits(nil, e.Edits), Mode: 0o644, LineEdits: true}}
		}
		if line := shortenedPlaceholder(e, nil); line != "" {
			return comparison{problem: ProblemShortened, detail: line}
		}
		return comparison{kind: KindNew, change: Change{Kind: KindNew, Path: e.Path, Abs: abs, Body: withLineEndings(e.Body, false), Mode: 0o644}}
	case err != nil:
		return comparison{err: err}
	case info.IsDir():
		return comparison{problem: ProblemFolder}
	}
	local, err := os.ReadFile(abs)
	if err != nil {
		return comparison{err: err}
	}
	if bytes.IndexByte(local, 0) >= 0 {
		if bytes.Equal(local, e.Body) {
			return comparison{kind: KindIdentical}
		}
		return comparison{problem: ProblemBinary}
	}
	if line := shortenedPlaceholder(e, local); line != "" {
		return comparison{problem: ProblemShortened, detail: line}
	}
	var body []byte
	if edits {
		// The excerpt was copied from a file with Total lines; if the file
		// has changed size since, its line numbers no longer point at the
		// copied code. Copy the excerpt again (user, 2026-11-18).
		now := LineCount(local)
		for _, ed := range e.Edits {
			if ed.HasTotal && ed.Total != now {
				// The tag itself is shown, so an agent sees its own markup
				// (user, 2026-11-20).
				return comparison{problem: ProblemChanged,
					detail: fmt.Sprintf(`lines="%d-%d" out-of="%d", but the file has %s now`, ed.Start, ed.End, ed.Total, lineCountWord(now))}
			}
		}
		body = applyLineEdits(local, e.Edits)
	} else {
		body = withLineEndings(e.Body, fileUsesCRLF(local))
	}
	if bytes.Equal(local, body) {
		return comparison{kind: KindIdentical}
	}
	return comparison{kind: KindChanged, change: Change{Kind: KindChanged, Path: e.Path, Abs: abs, Body: body, Local: local, Mode: info.Mode().Perm(), LineEdits: edits}}
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

// Blocked lists the entries that were meant to be written but cannot be.
// While any exists, nothing may be written: applying the rest of a reply
// could leave code that no longer builds (one file changed, the file it
// depends on skipped).
func (p Plan) Blocked() []Skipped {
	var blocked []Skipped
	for _, s := range p.Skipped {
		if s.Blocking {
			blocked = append(blocked, s)
		}
	}
	return blocked
}

// lineCountWord is "1 line" or "N lines".
func lineCountWord(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}
