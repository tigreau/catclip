package output

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

type refinementNotice struct {
	scope int
	count int
	paths []string
}

// Refinement bookkeeping is invocation-local and only allocated for --then
// with snippet/diff scopes. No file reads or query replay is permitted here.
type refinementMerge struct {
	scopes   []EvaluatedScope
	active   []bool
	notices  []refinementNotice
	filtered bool
}

func isWholeBody(e discovery.Entry) bool {
	return e.Mode == "" || e.Mode == command.EntryModeFull ||
		(e.Mode == command.EntryModeLines && e.LinesStart == 0)
}

func beginRefinementMerge(commands []command.ExecutionScope, scopes []EvaluatedScope) ([]EvaluatedScope, *refinementMerge) {
	if len(scopes) < 2 || len(commands) != len(scopes) {
		return scopes, nil
	}
	any := false
	for i, s := range scopes {
		mode := commands[i].OutputMode()
		if !s.Paths && (mode == command.EntryModeSnippet || mode == command.EntryModeDiff) &&
			(s.EmptyRefinement.Evaluated || len(s.Entries) > 0) {
			any = true
			break
		}
	}
	if !any {
		return scopes, nil
	}
	m := &refinementMerge{scopes: scopes, active: make([]bool, len(scopes)), notices: make([]refinementNotice, len(scopes))}
	// Index only focused paths, not a potentially huge broad selection.
	focusedCount := 0
	for i, s := range scopes {
		mode := commands[i].OutputMode()
		if !s.Paths && (mode == command.EntryModeSnippet || mode == command.EntryModeDiff) {
			focusedCount += len(s.EmptyRefinement.Paths) + len(s.Entries)
		}
	}
	full := make(map[string]int, focusedCount)
	for i, s := range scopes {
		mode := commands[i].OutputMode()
		if s.Paths || (mode != command.EntryModeSnippet && mode != command.EntryModeDiff) {
			continue
		}
		for _, path := range s.EmptyRefinement.Paths {
			full[path] = -1
		}
		for _, e := range s.Entries {
			full[e.RelPath] = -1
		}
	}
	for _, s := range scopes {
		if !s.Paths {
			for _, e := range s.Entries {
				if isWholeBody(e) {
					if _, relevant := full[e.RelPath]; relevant {
						full[e.RelPath] = 0
					}
				}
			}
		}
	}
	var suppressed map[string]struct{}
	for i, s := range scopes {
		mode := commands[i].OutputMode()
		if s.Paths || (mode != command.EntryModeSnippet && mode != command.EntryModeDiff) {
			continue
		}
		m.active[i] = s.EmptyRefinement.Evaluated || len(s.Entries) > 0
		n := &m.notices[i]
		n.scope = i
		note := func(path string) {
			if last, ok := full[path]; ok && last >= 0 && last != i+1 {
				full[path] = i + 1
				n.count++
				if n.count <= 5 {
					n.paths = append(n.paths, path)
				} else {
					n.paths = nil
				}
			}
		}
		for _, path := range s.EmptyRefinement.Paths {
			if suppressed == nil {
				suppressed = make(map[string]struct{}, focusedCount)
			}
			suppressed[path] = struct{}{}
			note(path)
		}
		for _, e := range s.Entries {
			note(e.RelPath)
		}
	}
	if len(suppressed) == 0 {
		return scopes, m
	}
	out := append([]EvaluatedScope(nil), scopes...)
	for i, s := range scopes {
		if s.Paths {
			continue
		}
		// Allocate only when a whole-file contribution really needs removing.
		first, removed := -1, 0
		for j, e := range s.Entries {
			if _, ok := suppressed[e.RelPath]; ok && isWholeBody(e) {
				if first < 0 {
					first = j
				}
				removed++
			}
		}
		if first < 0 {
			continue
		}
		entries := make([]discovery.Entry, 0, len(s.Entries)-removed)
		entries = append(entries, s.Entries[:first]...)
		for _, e := range s.Entries[first+1:] {
			if _, ok := suppressed[e.RelPath]; !ok || !isWholeBody(e) {
				entries = append(entries, e)
			}
		}
		out[i].Entries = entries
		m.filtered = true
	}
	return out, m
}

func (m *refinementMerge) finish(plan *Plan) {
	if m == nil {
		return
	}
	var survived map[string]bool
	for i, s := range m.scopes {
		if m.active[i] && !s.EmptyExplained && len(s.Entries) > 0 {
			if survived == nil {
				survived = make(map[string]bool, len(s.Entries))
			}
			for _, e := range s.Entries {
				survived[e.RelPath] = false
			}
		}
	}
	if len(survived) > 0 {
		for _, item := range plan.items {
			if item.kind == SectionKindFiles {
				if _, relevant := survived[item.relPath]; relevant {
					survived[item.relPath] = true
				}
			}
		}
	}
	for i, s := range m.scopes {
		if !m.active[i] || s.EmptyExplained {
			continue
		}
		found := false
		for _, e := range s.Entries {
			if survived[e.RelPath] {
				found = true
				break
			}
		}
		if found {
			continue
		}
		n := m.notices[i]
		sort.Strings(n.paths)
		plan.refinementNotices = append(plan.refinementNotices, n)
	}
}

// RefinementNoticeText is bounded by five paths per empty scope. Callers can
// place it below a combined tree, or later below individual scope trees.
func (p Plan) RefinementNoticeText() []string {
	if len(p.refinementNotices) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.refinementNotices))
	for _, n := range p.refinementNotices {
		var b strings.Builder
		fmt.Fprintf(&b, "Scope %d produced no output.", n.scope+1)
		if n.count > 5 {
			fmt.Fprintf(&b, "\n%d full files omitted.", n.count)
		} else if n.count > 0 {
			b.WriteString("\nFull files omitted:")
			for _, path := range n.paths {
				fmt.Fprintf(&b, "\n  %s", refinementPathEscaper.Replace(path))
			}
		}
		out = append(out, b.String())
	}
	return out
}

var refinementPathEscaper = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`, "\x1b", `\x1b`)

func (p Plan) WriteRefinementNotices(w io.Writer, colors platform.Palette) error {
	for _, text := range p.RefinementNoticeText() {
		if _, err := fmt.Fprintf(w, "\n%s%s%s\n", colors.Warn, text, colors.Reset); err != nil {
			return err
		}
	}
	return nil
}
