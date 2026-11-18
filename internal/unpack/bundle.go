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
	"strings"
)

// Entry is one complete file from the bundle.
type Entry struct {
	Path string
	Body []byte
}

// Skipped is a bundle entry that is not a complete file and is never written.
type Skipped struct {
	Path   string
	Reason string
}

// Bundle is the parsed, validated content of a catclip bundle.
type Bundle struct {
	Entries []Entry
	Skipped []Skipped
}

// ErrNotBundle reports input without any catclip file wrapper.
var ErrNotBundle = errors.New("not a catclip bundle")

var (
	openTagPattern = regexp.MustCompile(`^<file path="([^"]*)"((?: [a-z]+="[^"]*")*)>$`)
	attrPattern    = regexp.MustCompile(` ([a-z]+)="([^"]*)"`)
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
			pos, line = next, line+1
			continue
		}
		m := openTagPattern.FindSubmatch(trimmed)
		if m == nil {
			return Bundle{}, fmt.Errorf("line %d: unreadable file tag: %s", tagLine, clip(string(trimmed)))
		}
		found = true
		path := string(m[1])
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
				} else if i, dup := full[path]; dup {
					if !bytes.Equal(b.Entries[i].Body, body) {
						return Bundle{}, fmt.Errorf("%s appears twice with different contents", path)
					}
				} else {
					full[path] = len(b.Entries)
					b.Entries = append(b.Entries, Entry{Path: path, Body: body})
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
		return Bundle{}, ErrNotBundle
	}
	return b, nil
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
	if attrs["numbered"] == "true" {
		return "numbered lines (copy without --lines to unpack)"
	}
	if lines, ok := attrs["lines"]; ok {
		return "excerpt (lines " + lines + ")"
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
		if name != "type" {
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
