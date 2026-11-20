package catclip

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

func writeAccessSummary(ctx outputExecutionContext, state outputExecutionState) error {
	paths := discovery.AccessFailuresForScopes(ctx.Invocation.WorkingDir, state.DiscoveredScopes, ctx.Invocation.PayloadKind == command.PayloadMetadata)
	if len(paths) == 0 {
		return nil
	}
	description := "inaccessible"
	for _, issue := range paths {
		if issue.Vanished {
			description = "unavailable"
			break
		}
	}
	if _, err := fmt.Fprintf(ctx.Stderr, "\nSkipped %d %s path(s):\n", len(paths), description); err != nil {
		return err
	}
	for _, issue := range paths[:min(len(paths), 10)] {
		path := issue.Path
		tag := "permission error"
		if issue.Vanished {
			tag = "no longer exists"
		}
		// Keep filenames containing control characters from injecting terminal
		// commands or extra diagnostic lines; ordinary Unicode/spaces stay readable.
		if strings.ContainsFunc(path, unicode.IsControl) {
			path = fmt.Sprintf("%q", path)
		}
		if _, err := fmt.Fprintf(ctx.Stderr, "  %s  [%s]\n", path, tag); err != nil {
			return err
		}
	}
	if len(paths) > 10 {
		if _, err := fmt.Fprintf(ctx.Stderr, "  …and %d more %s paths\n", len(paths)-10, description); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(ctx.Stderr)
	return err
}
