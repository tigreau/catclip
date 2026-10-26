package output

import (
	"fmt"
	"io"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

const lineOmissionDetailLimit = 10

type lineRange struct{ start, end int }
type lineSelection struct {
	path string
	lineRange
}
type lineOmission struct {
	selection lineSelection
	lines     int
}
type lineOmissionGroup struct {
	rangeRequested lineRange
	count, longest int
	totalLines     int64
	samples        []lineOmission
}

// Broad selections retain at most ten paths in total, regardless of file count.
// Explicit targets are recorded before deduplication so an equivalent directory
// selection cannot erase the user's request for an individual explanation.
type lineOmissions struct {
	explicit   map[lineSelection]struct{}
	details    []lineOmission
	groups     []lineOmissionGroup
	groupIndex map[lineRange]int
	broadCount int
}

func (o *lineOmissions) noteExplicit(entries []discovery.Entry) {
	for i := range entries {
		e := &entries[i]
		if e.Mode != command.EntryModeLines || e.LinesStart <= 0 || e.TargetRoot != e.RelPath || e.RelPath == "" {
			continue
		}
		if o.explicit == nil {
			o.explicit = make(map[lineSelection]struct{})
		}
		o.explicit[lineSelection{e.RelPath, lineRange{e.LinesStart, e.LinesEnd}}] = struct{}{}
	}
}

func (o *lineOmissions) noteScopeExplicit(scope EvaluatedScope) {
	// Picker file rows identify themselves through TargetRoot. Direct CLI file
	// targets use their parent directory there (for depth), and carry distinct
	// resolved-target evidence. Neither path needs a new filesystem query.
	o.noteExplicit(scope.Entries)
	var files map[string]struct{}
	for _, target := range scope.ResolvedTargets {
		if target.Kind != discovery.ResolvedTargetFile {
			continue
		}
		if files == nil {
			files = make(map[string]struct{})
		}
		files[target.Path] = struct{}{}
	}
	if len(files) == 0 {
		return
	}
	for i := range scope.Entries {
		e := &scope.Entries[i]
		if e.Mode != command.EntryModeLines || e.LinesStart <= 0 {
			continue
		}
		if _, ok := files[e.RelPath]; !ok {
			continue
		}
		if o.explicit == nil {
			o.explicit = make(map[lineSelection]struct{})
		}
		o.explicit[lineSelection{e.RelPath, lineRange{e.LinesStart, e.LinesEnd}}] = struct{}{}
	}
}

func (o *lineOmissions) add(e discovery.Entry, lines int) {
	if o == nil {
		return
	}
	detail := lineOmission{lineSelection{e.RelPath, lineRange{e.LinesStart, e.LinesEnd}}, lines}
	if _, ok := o.explicit[detail.selection]; ok {
		o.details = append(o.details, detail)
		return
	}
	if o.groupIndex == nil {
		o.groupIndex = make(map[lineRange]int)
	}
	r := detail.selection.lineRange
	i, ok := o.groupIndex[r]
	if !ok {
		i = len(o.groups)
		o.groupIndex[r] = i
		o.groups = append(o.groups, lineOmissionGroup{rangeRequested: r})
	}
	g := &o.groups[i]
	g.count++
	g.totalLines += int64(lines)
	if lines > g.longest {
		g.longest = lines
	}
	o.broadCount++
	if o.broadCount <= lineOmissionDetailLimit {
		g.samples = append(g.samples, detail)
	} else if o.broadCount == lineOmissionDetailLimit+1 {
		for i := range o.groups {
			o.groups[i].samples = nil
		}
	}
}

func (p Plan) HasLineOmissions() bool {
	return p.lineOmissions.broadCount > 0 || len(p.lineOmissions.details) > 0
}

// WriteLineOmissions writes diagnostics only. It is deliberately separate from
// all payload writers so content previews, clipboard and bundles stay clean.
func (p Plan) WriteLineOmissions(w io.Writer, colors platform.Palette) error {
	writeDetail := func(d lineOmission) error {
		_, err := fmt.Fprintf(w, "%sOmitted %s: requested %s; file has %s.%s\n", colors.Warn, d.selection.path,
			formatLinesRange(d.selection.start, d.selection.end), lineCountDescription(d.lines), colors.Reset)
		return err
	}
	for _, d := range p.lineOmissions.details {
		if err := writeDetail(d); err != nil {
			return err
		}
	}
	for _, g := range p.lineOmissions.groups {
		if p.lineOmissions.broadCount <= lineOmissionDetailLimit {
			for _, d := range g.samples {
				if err := writeDetail(d); err != nil {
					return err
				}
			}
			continue
		}
		word := "files"
		if g.count == 1 {
			word = "file"
		}
		average := (g.totalLines + int64(g.count)/2) / int64(g.count)
		if _, err := fmt.Fprintf(w, "%sOmitted %d %s: requested %s were unavailable.%s\n%sOmitted files average %d lines; longest is %d lines.%s\n",
			colors.Warn, g.count, word, formatLinesRange(g.rangeRequested.start, g.rangeRequested.end), colors.Reset,
			colors.Warn, average, g.longest, colors.Reset); err != nil {
			return err
		}
	}
	if p.HasLineOmissions() {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}

func lineCountDescription(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}
