package output

import (
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/tigreau/catclip/internal/platform"
)

type planTokenLimit struct {
	cutBytes int64
	tokens   int64
	path     string
	partial  bool
	metadata bool
}

// WithTokenLimit projects the final merged source selection. It uses retained
// body sizes only: no source reads, stats, line counts or wrapper accounting.
// Unlimited output returns immediately. A fitting budget retains the original
// file storage and records the completed check for preview/final-output reuse.
func (p Plan) WithTokenLimit(tokens int64) Plan {
	if tokens <= 0 || tokens > math.MaxInt64/4 || p.tokenBudget == tokens || p.tokenLimit != nil {
		return p
	}
	p.tokenBudget = tokens
	remaining := tokens * 4
	offset := 0
	for si, s := range p.sections {
		if s.kind == SectionKindMetadata {
			if s.metadataBytes <= remaining {
				remaining -= s.metadataBytes
				offset += len(s.items)
				continue
			}
			q := p
			q.sections = append([]PlanSection(nil), p.sections[:si+1]...)
			q.sections[si].tokenCutBytes = remaining
			q.sections[si].metadataBytes = remaining
			q.items = p.items[:offset+len(s.items)]
			q.tokenLimit = &planTokenLimit{tokens: tokens, metadata: true, partial: remaining > 0}
			return q
		}
		for ii := range s.items {
			if s.items[ii].bodyBytes <= remaining {
				remaining -= s.items[ii].bodyBytes
				offset++
				continue
			}
			item := s.items[ii]
			q := p
			q.sections = append([]PlanSection(nil), p.sections[:si+1]...)
			q.tokenLimit = &planTokenLimit{tokens: tokens, path: item.relPath, partial: remaining > 0, cutBytes: remaining}
			keep := ii
			if remaining > 0 {
				keep++
			}
			q.sections[si].items = append([]PlanItem(nil), s.items[:keep]...)
			q.items = append([]PlanItem(nil), p.items[:offset]...)
			if remaining > 0 {
				item.bodyBytes = remaining
				item.unit.BodyBytes = remaining
				q.sections[si].items[ii] = item
				q.items = append(q.items, item)
			}
			if keep == 0 {
				q.sections = q.sections[:si]
			}
			return q
		}
	}
	return p
}

func (p Plan) TokenLimitNotice() string {
	if p.tokenLimit == nil {
		return ""
	}
	return fmt.Sprintf("Output limited to ~%d tokens; additional selected content will be omitted.", p.tokenLimit.tokens)
}

// limitPosition belongs to one emission, never the retained plan: preview
// workers and final output may render the same plan concurrently.
type limitPosition struct {
	line        int
	partial     bool
	outputLine  bool
	unavailable bool
}

func (p Plan) tokenLimitFooter(pos limitPosition) string {
	l := p.tokenLimit
	if l == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[catclip: output truncated — estimated token limit: %d]\n", l.tokens)
	if l.metadata {
		b.WriteString("The metadata report is incomplete.\n")
	} else if pos.unavailable {
		fmt.Fprintf(&b, "Cutoff file unavailable: %s\n", l.path)
	} else if !l.partial {
		fmt.Fprintf(&b, "Stopped before: %s\n", l.path)
	} else if pos.line > 0 {
		label := "line"
		if pos.outputLine {
			label = "output line"
		}
		fmt.Fprintf(&b, "Stopped in %s at %s %d", l.path, label, pos.line)
		if pos.partial {
			b.WriteString(" (partial line)")
		}
		b.WriteString(".\n")
	} else {
		fmt.Fprintf(&b, "Cutoff path: %s (partial output).\n", l.path)
	}
	b.WriteString("Additional selected content was omitted.\n")
	return b.String()
}

func writeTokenLimitedPlan(w io.Writer, cfg EmitConfig, p Plan, print *printPresentation, prefetchEnabled bool, present func(io.Writer, PreparedFileUnit) (bool, error)) (string, error) {
	observed, ok := w.(*payloadWriter)
	if !ok {
		observed = &payloadWriter{w: w}
	}
	var pos limitPosition
	for si, s := range p.sections {
		if si > 0 && !cfg.Raw {
			if _, err := io.WriteString(observed, outputSectionSeparator(p.sections[si-1].kind, s.kind)); err != nil {
				return "", err
			}
		}
		if s.kind == SectionKindMetadata {
			if si == len(p.sections)-1 && p.tokenLimit.metadata {
				cut := &textCutWriter{w: observed, remaining: s.tokenCutBytes}
				_, err := s.writeMetadata(cut, platform.Palette{})
				if err != nil && err != errTokenCut {
					return "", err
				}
			} else {
				colors := platform.Palette{}
				if print != nil {
					colors = print.colors
				}
				added, err := s.writeMetadata(observed, colors)
				if print != nil {
					print.addedBytes += added
				}
				if err != nil {
					return "", err
				}
			}
			continue
		}
		// Retain the normal prefetch pool for complete files; never prefetch the
		// partial file, whose bounded reader must not fetch the rest of its body.
		var prefetch *emitPrefetcher
		if prefetchEnabled && present == nil && !cfg.Raw && s.kind == SectionKindFiles {
			n := len(s.items)
			if n > 0 && si == len(p.sections)-1 && p.tokenLimit.partial && !p.tokenLimit.metadata {
				n--
			}
			if n > 0 {
				prefetch = startEmitPrefetch(Plan{items: s.items[:n]})
			}
		}
		err := func() error {
			if prefetch != nil {
				defer prefetch.Close()
			}
			for i, item := range s.items {
				cut := si == len(p.sections)-1 && i == len(s.items)-1 && p.tokenLimit.partial && !p.tokenLimit.metadata
				if item.kind == SectionKindPaths {
					data := []byte(item.relPath + "\n")
					if cut {
						data = data[:utf8CutLength(data, int(p.tokenLimit.cutBytes))]
					}
					if _, err := observed.Write(data); err != nil {
						return err
					}
					continue
				}
				before := observed.n
				var err error
				switch {
				case cut:
					pos, err = emitTokenCutFile(observed, item.unit, cfg.Raw, p.tokenLimit.cutBytes)
				case present != nil:
					err = writePreviewFileUnit(observed, cfg, item, present)
				case cfg.Raw:
					err = writePreviewFileUnit(observed, cfg, item, nil)
				case print != nil && print.highlight != nil:
					err = print.emitEntry(observed, item.unit, i, prefetch)
				default:
					err = emitEntry(observed, item.unit, i, prefetch)
				}
				if err != nil {
					if !observed.skip(err, item.unit.Entry.AbsPath, item.relPath, before) {
						return err
					}
					if cut {
						pos.unavailable = true
					}
				}
			}
			return nil
		}()
		if err != nil {
			return "", err
		}
	}
	if observed.n == 0 && len(observed.skipped) > 0 {
		return "", ErrNoAccessibleOutput
	}
	footer := p.tokenLimitFooter(pos)
	if !cfg.Raw {
		_, err := io.WriteString(observed, footer)
		return "", err
	}
	return footer, nil
}

var errTokenCut = fmt.Errorf("token budget exhausted")

// Metadata/path text has no enclosing file wrapper. This writer only appears
// on the cutoff report, never on complete files or the unlimited output path.
type textCutWriter struct {
	w         io.Writer
	remaining int64
}

func (w *textCutWriter) Write(p []byte) (int, error) {
	if int64(len(p)) <= w.remaining {
		n, e := w.w.Write(p)
		w.remaining -= int64(n)
		return n, e
	}
	n := utf8CutLength(p, int(w.remaining))
	if _, err := w.w.Write(p[:n]); err != nil {
		return 0, err
	}
	w.remaining = 0
	return n, errTokenCut
}

// The original payload may contain invalid UTF-8; preserve it. Back up only
// when an otherwise valid character actually straddles the requested cut.
func utf8CutLength(p []byte, n int) int {
	if n >= len(p) {
		return len(p)
	}
	if n <= 0 {
		return 0
	}
	start := n
	for start > 0 && n-start < 3 && p[start]&0xc0 == 0x80 {
		start--
	}
	if start == n {
		return n
	}
	size := 0
	c := p[start]
	switch {
	case c >= 0xc2 && c <= 0xdf:
		size = 2
	case c >= 0xe0 && c <= 0xef:
		size = 3
	case c >= 0xf0 && c <= 0xf4:
		size = 4
	}
	if size > n-start && start+size <= len(p) && utf8.Valid(p[start:start+size]) {
		return start
	}
	return n
}
