package catclip

import (
	"fmt"
	"os"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
)

func lookupIgnoredDotfiles(name string) []string {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil
	}
	return discovery.FindIgnoredDotfiles(workingDir, name)
}

func positionalDotfileHint(name string, matches []string, scope positionalGlobScope, prefix, suffix [][]string) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	if len(matches) == 1 {
		fmt.Fprintf(&b, "\n\n  Found an ignored file named %s. To select it directly:", discovery.SingleQuoted(name))
	} else {
		fmt.Fprintf(&b, "\n\n  Found ignored files named %s. Select one by its complete relative path:", discovery.SingleQuoted(name))
	}
	const maxShown = 5
	for _, match := range matches[:min(len(matches), maxShown)] {
		replacement := append([]string(nil), scope.raw...)
		// Only replace the offending positional token, never a modifier value
		// that happens to contain the same text. Preserve surrounding scopes.
		for i, token := range scope.positional {
			if token.raw == name {
				replacement[i] = match
				break
			}
		}
		args := joinPositionalGlobCommand(prefix, [][]string{replacement}, suffix)
		fmt.Fprintf(&b, "\n    %s", cli.FormatResolvedStartupCommand(args))
	}
	if len(matches) > maxShown {
		fmt.Fprintf(&b, "\n  ... and %d more matching ignored files.", len(matches)-maxShown)
	}
	return b.String()
}
