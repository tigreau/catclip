package unpack

import (
	"fmt"
	"strings"
)

const (
	// maxDiffLines and maxDiffEdits bound preview work and memory (about
	// maxDiffEdits² ints). Beyond them the preview shows a summary instead.
	maxDiffLines = 100000
	maxDiffEdits = 2000
)

// UnifiedDiff renders a git-style unified diff (3 lines of context) from the
// local copy to the bundle copy. A new file diffs against /dev/null.
func UnifiedDiff(c Change) string {
	from, to := "a/"+c.Path, "b/"+c.Path
	if c.Kind == KindNew {
		from = "/dev/null"
	}
	old, new := splitLines(string(c.Local)), splitLines(string(c.Body))
	var b strings.Builder
	// Git-style header: viewers and `git apply` recognise each file's section,
	// `patch -p1` ignores it, and neither side needs a Git repository. No
	// index hashes: they only help `git apply --3way` inside a repository.
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", c.Path, c.Path)
	if c.Kind == KindNew {
		b.WriteString("new file mode 100644\n")
	}
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", from, to)
	if len(old)+len(new) > maxDiffLines {
		fmt.Fprintf(&b, "@@ too large to preview: %d lines → %d lines @@\n", len(old), len(new))
		return b.String()
	}
	ops, ok := diffLines(old, new)
	if !ok {
		fmt.Fprintf(&b, "@@ too many differences to preview: %d lines → %d lines @@\n", len(old), len(new))
		return b.String()
	}
	writeHunks(&b, ops, old, new, 3)
	return b.String()
}

// splitLines keeps each line's terminator, so a missing final newline shows.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type opKind byte

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	a, b int // positions in old and new
}

// diffLines is Myers' shortest edit script over whole lines. Step d keeps only
// diagonals -d..d. It gives up (ok=false) after maxDiffEdits edits.
func diffLines(a, b []string) ([]op, bool) {
	n, m := len(a), len(b)
	if n+m == 0 {
		return nil, true
	}
	limit := min(n+m, maxDiffEdits)
	// v[k+off] is the furthest x on diagonal k; off leaves room for k±1.
	off := limit + 1
	v := make([]int, 2*off+1)
	// trace[d][k+d] keeps the state before step d, for diagonals -d..d.
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m, d), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, n, m, d int) []op {
	x, y := n, m
	var ops []op
	for ; d > 0; d-- {
		v := trace[d]
		at := func(k int) int { return v[k+d] }
		k := x - y
		prevK := k - 1
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			ops = append(ops, op{opEqual, x, y})
		}
		if x == prevX {
			y--
			ops = append(ops, op{opInsert, x, y})
		} else {
			x--
			ops = append(ops, op{opDelete, x, y})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		ops = append(ops, op{opEqual, x, y})
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

func writeHunks(b *strings.Builder, ops []op, old, new []string, context int) {
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			i++
			continue
		}
		start := i
		for start > 0 && i-start < context && ops[start-1].kind == opEqual {
			start--
		}
		end := i
		for end < len(ops) {
			if ops[end].kind != opEqual {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == opEqual {
				run++
			}
			if run == len(ops) || run-end > 2*context {
				end += min(context, run-end)
				break
			}
			end = run
		}
		oldStart, newStart, oldCount, newCount := -1, -1, 0, 0
		for _, o := range ops[start:end] {
			if o.kind != opInsert {
				if oldStart < 0 {
					oldStart = o.a
				}
				oldCount++
			}
			if o.kind != opDelete {
				if newStart < 0 {
					newStart = o.b
				}
				newCount++
			}
		}
		fmt.Fprintf(b, "@@ -%s +%s @@\n", hunkRange(oldStart, oldCount, ops[start].a), hunkRange(newStart, newCount, ops[start].b))
		for _, o := range ops[start:end] {
			switch o.kind {
			case opEqual:
				writeDiffLine(b, ' ', old[o.a])
			case opDelete:
				writeDiffLine(b, '-', old[o.a])
			case opInsert:
				writeDiffLine(b, '+', new[o.b])
			}
		}
		i = end
	}
}

// hunkRange follows git: one-based start, and the line before for count 0.
func hunkRange(start, count, position int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", position)
	case 1:
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

func writeDiffLine(b *strings.Builder, prefix byte, line string) {
	b.WriteByte(prefix)
	b.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		b.WriteString("\n\\ No newline at end of file\n")
	}
}
