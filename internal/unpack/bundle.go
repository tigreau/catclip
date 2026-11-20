// Package unpack writes a project's files back from a catclip bundle.
//
// It reads only the bundle and the exact local paths the bundle names: no
// discovery, ignore walk, classification or Git. Nothing is written until the
// caller has collected every decision and called Write.
package unpack

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Entry is one file from the bundle: either a complete file (Body) or line
// edits to it (Edits, from excerpt tags with lines="A-B").
type Entry struct {
	Path  string
	Body  []byte
	Edits []LineEdit
}

// Skipped is a bundle entry that is never written. Entries skipped by design
// (Git patches, binary descriptions, partial="true") are listed and ignored.
// Blocking entries were meant to be written but cannot be (the file changed
// since the excerpt was copied, is missing here, or is a folder or binary):
// a reply is one change, so any of them stops the whole unpack.
type Skipped struct {
	Path string
	// Reason describes an entry that is listed and ignored ("Git patch",
	// "binary description"). Blocking entries use Problem and Detail instead.
	Reason string
	// Problem is the kind of problem of a blocking entry. Messages explain
	// each kind once and list its files under it, so 100 stale excerpts do
	// not repeat the same explanation 100 times (user, 2026-11-20).
	Problem Problem
	// Detail is what is specific to this file, e.g. `lines="1-1" out-of="9",
	// but the file has 251 lines now`, or the placeholder line found.
	Detail   string
	Blocking bool
}

// Problem is why a blocking entry cannot be applied.
type Problem string

const (
	ProblemChanged   Problem = "changed"   // out-of no longer matches the file
	ProblemMissing   Problem = "missing"   // an excerpt for a file that is not here
	ProblemShortened Problem = "shortened" // code replaced by a placeholder
	ProblemFolder    Problem = "folder"    // a folder where a file should go
	ProblemBinary    Problem = "binary"    // the local copy is binary
)

// Bundle is the parsed, validated content of a catclip bundle.
type Bundle struct {
	Entries []Entry
	Skipped []Skipped
	// Notes is the text outside the entries: in an agent's reply, its
	// explanations and caveats ("I assumed User has an Email field"), shown
	// before anything is written. Code fences and outer blank lines are
	// dropped.
	Notes []string
}

// ErrNotBundle reports input without any catclip file wrapper.
var ErrNotBundle = errors.New("not a catclip bundle")

var (
	openTagPattern = regexp.MustCompile(`^<file path="([^"]*)"((?: [a-z][a-z-]*="[^"]*")*)>$`)
	attrPattern    = regexp.MustCompile(` ([a-z][a-z-]*)="([^"]*)"`)
)

const closeTag = "</file>"

// Parse reads catclip's wrapper format:
//
//	<file path="REL/PATH"[ attr="..."]...>
//	BODY
//	</file>
//
// The body is exactly the bytes between the open-tag line and the "</file>"
// line. Text outside wrappers (path lists, metadata reports, token-limit
// notices) is ignored. A body line that is exactly "</file>" ends the entry:
// a known limit of the current format.
func Parse(data []byte) (Bundle, error) {
	var b Bundle
	// index of each path's entry; a path is either a complete file or line
	// edits, never both.
	full := map[string]int{}
	pos := 0
	line := 1
	found := false
	for pos < len(data) {
		end := bytes.IndexByte(data[pos:], '\n')
		var text []byte
		next := len(data)
		if end >= 0 {
			text = data[pos : pos+end]
			next = pos + end + 1
		} else {
			text = data[pos:]
		}
		tagLine := line
		trimmed := bytes.TrimSuffix(text, []byte{'\r'})
		// Chat apps sometimes indent a whole code block (inside a list).
		// The opening tag's indentation is then the block's margin: it is
		// removed from the tag, every body line and the closing tag.
		// catclip's own bundles are never indented.
		tagStart := bytes.TrimLeft(trimmed, " \t")
		margin := string(trimmed[:len(trimmed)-len(tagStart)])
		trimmed = tagStart
		if !bytes.HasPrefix(trimmed, []byte("<file ")) {
			b.Notes = appendNote(b.Notes, string(trimmed))
			pos, line = next, line+1
			continue
		}
		m := openTagPattern.FindSubmatch(trimmed)
		if m == nil {
			return Bundle{}, fmt.Errorf("line %d: unreadable file tag: %s", tagLine, clip(string(trimmed)))
		}
		found = true
		// Bundles always use forward slashes; an agent that assumes Windows
		// may write src\main.go, which on macOS/Linux would otherwise become
		// one file with a backslash in its name.
		path := strings.ReplaceAll(string(m[1]), `\`, "/")
		attrs := map[string]string{}
		for _, a := range attrPattern.FindAllSubmatch(m[2], -1) {
			attrs[string(a[1])] = string(a[2])
		}
		bodyStart := next
		pos, line = next, line+1
		closed := false
		for pos < len(data) {
			end := bytes.IndexByte(data[pos:], '\n')
			lineEnd := len(data)
			after := len(data)
			if end >= 0 {
				lineEnd = pos + end
				after = lineEnd + 1
			}
			if string(bytes.TrimSuffix(data[pos:lineEnd], []byte{'\r'})) == margin+closeTag {
				body := stripMargin(data[bodyStart:pos], margin)
				if reason := partialReason(attrs); reason != "" {
					b.Skipped = append(b.Skipped, Skipped{Path: path, Reason: reason})
				} else if rangeValue, isEdit := attrs["lines"]; isEdit {
					edit, err := parseEdit(path, rangeValue, attrs["out-of"], body)
					if err != nil {
						return Bundle{}, fmt.Errorf("line %d: %w", tagLine, err)
					}
					i, seen := full[path]
					switch {
					case !seen:
						full[path] = len(b.Entries)
						b.Entries = append(b.Entries, Entry{Path: path, Edits: []LineEdit{edit}})
					case b.Entries[i].Edits == nil:
						return Bundle{}, fmt.Errorf("%s appears both as a whole file and as line edits", path)
					case !containsEdit(b.Entries[i].Edits, edit):
						b.Entries[i].Edits = append(b.Entries[i].Edits, edit)
					}
				} else {
					if attrs["numbered"] == "true" {
						body = stripLineNumbers(body, 1)
					}
					if i, dup := full[path]; dup {
						if b.Entries[i].Edits != nil {
							return Bundle{}, fmt.Errorf("%s appears both as a whole file and as line edits", path)
						}
						if !bytes.Equal(b.Entries[i].Body, body) {
							return Bundle{}, fmt.Errorf("%s appears twice with different contents", path)
						}
					} else {
						full[path] = len(b.Entries)
						b.Entries = append(b.Entries, Entry{Path: path, Body: body})
					}
				}
				pos, line = after, line+1
				closed = true
				break
			}
			pos, line = after, line+1
		}
		if !closed {
			return Bundle{}, fmt.Errorf("line %d: %s has no closing </file>", tagLine, path)
		}
	}
	if !found {
		return Bundle{Notes: b.Notes}, ErrNotBundle
	}
	for _, e := range b.Entries {
		if err := checkEditsConsistent(e.Path, e.Edits); err != nil {
			return Bundle{}, err
		}
	}
	return b, nil
}

// parseEdit reads an excerpt entry: lines="A-B", an optional out-of="N" (the
// file's line count when the excerpt was copied), and
// the body with any --lines number column removed.
func parseEdit(path, rangeValue, totalValue string, body []byte) (LineEdit, error) {
	start, end, ok := parseLineRange(rangeValue)
	if !ok {
		return LineEdit{}, fmt.Errorf("%s has an unreadable line range lines=%q", path, rangeValue)
	}
	total := 0
	if totalValue != "" {
		n, err := strconv.Atoi(totalValue)
		if err != nil || n < 0 {
			return LineEdit{}, fmt.Errorf("%s has an unreadable out-of=%q", path, totalValue)
		}
		total = n
	}
	return LineEdit{Start: start, End: end, Total: total, HasTotal: totalValue != "", Body: stripLineNumbers(body, start)}, nil
}

// containsEdit reports an identical repeat (same range and body), which is
// harmless and applied once.
func containsEdit(edits []LineEdit, e LineEdit) bool {
	for _, x := range edits {
		if x.Start == e.Start && x.End == e.End && bytes.Equal(x.Body, e.Body) {
			return true
		}
	}
	return false
}

// stripMargin returns a copy of body with margin removed from the start of
// each line that has it. Lines without it (such as blank lines a chat app
// left unindented) are kept as they are.
func stripMargin(body []byte, margin string) []byte {
	if margin == "" {
		return append([]byte(nil), body...)
	}
	var out bytes.Buffer
	for _, line := range bytes.SplitAfter(body, []byte{'\n'}) {
		out.Write(bytes.TrimPrefix(line, []byte(margin)))
	}
	return out.Bytes()
}

// partialReason explains why a wrapper is not a complete file, or returns "".
func partialReason(attrs map[string]string) string {
	if attrs["partial"] == "true" {
		return "cut short by --token-limit"
	}
	switch kind, ok := attrs["type"]; {
	case !ok, kind == "untracked":
	case kind == "binary":
		return "binary description (send the file separately)"
	case kind == "diff", kind == "staged-diff", kind == "unstaged-diff":
		return "Git patch"
	default:
		return "unsupported entry (type=" + kind + ")"
	}
	var unknown []string
	for name := range attrs {
		switch name {
		case "type", "lines", "out-of", "numbered":
		default:
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return "unsupported entry (" + strings.Join(unknown, ", ") + ")"
	}
	return ""
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

// appendNote keeps a line of text from outside the entries, without code
// fences and without leading blank lines.
func appendNote(notes []string, line string) []string {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
		return notes
	}
	if t == "" && (len(notes) == 0 || notes[len(notes)-1] == "") {
		return notes
	}
	return append(notes, strings.TrimRight(line, " \t"))
}

// NoteText returns the notes as one block, without trailing blank lines.
func (b Bundle) NoteText() string {
	notes := b.Notes
	for len(notes) > 0 && notes[len(notes)-1] == "" {
		notes = notes[:len(notes)-1]
	}
	return strings.Join(notes, "\n")
}
