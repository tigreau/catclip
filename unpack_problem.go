package catclip

import (
	"fmt"
	"io"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
)

// unpackProblem is a reason --unpack cannot go on, with the fix worded for
// where the user is: the command line ("run catclip --unpack FILE") or the
// interactive unpack menu ("use [file]"), where command-line advice is wrong
// (user, 2026-11-19).
type unpackProblem struct {
	title   string   // what is wrong, one line
	detail  []string // more lines, shown in both places
	cliFix  []string // what to do on the command line
	menuFix []string // what to do in the unpack menu
	// menuTitle replaces title in the menu when the command-line wording is
	// too technical there ("This doesn't look like a catclip reply.").
	menuTitle string
	noPrefix  bool // the command-line message does not start with "Error: "
}

// Error is the command-line message, exactly as --unpack printed it before.
func (p unpackProblem) Error() string {
	var b strings.Builder
	if !p.noPrefix {
		b.WriteString("Error: ")
	}
	b.WriteString(p.title)
	for _, line := range append(append([]string{}, p.detail...), p.cliFix...) {
		b.WriteString("\n  " + line)
	}
	return b.String()
}

func (unpackProblem) CatclipExitCode() int { return 1 }

// writeMenu writes the problem for the unpack menu preview: the title in the
// warning color, the details, then each fix on a → line.
func (p unpackProblem) writeMenu(w io.Writer, c platform.Palette) {
	title := p.title
	if p.menuTitle != "" {
		title = p.menuTitle
	}
	fmt.Fprintf(w, "%s%s%s\n", c.Bold+c.Warn, title, c.Reset)
	for _, line := range p.detail {
		fmt.Fprintf(w, "  %s\n", line)
	}
	// Each fix gets its own →; a fix may hold \n to wrap by hand in the
	// half-width preview pane.
	for i, fix := range p.menuFix {
		if i == 0 {
			fmt.Fprint(w, "\n")
		}
		for j, line := range strings.Split(fix, "\n") {
			mark := "  "
			if j == 0 {
				mark = "→ "
			}
			fmt.Fprintf(w, "  %s%s%s%s\n", c.OK, mark, line, c.Reset)
		}
	}
}
