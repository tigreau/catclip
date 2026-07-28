package cli

import (
	"fmt"
	"path"
	"strings"
)

func validateDirectPathPatterns(surface string, values []string, exactValues bool) error {
	if exactValues {
		return nil
	}
	for _, value := range values {
		if value != "-" && strings.Contains(value, "**") {
			return UnsupportedDoubleStarError(surface, value)
		}
	}
	return nil
}

func renderUnsupportedDoubleStarValidationFailure(surface, value string) string {
	if surface == "target" {
		return renderUnsupportedTargetDoubleStar(value)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Error: %s patterns do not support '**': %s.\n\n", surface, singleQuoted(value))
	b.WriteString("  In Catclip filters, '*' already matches across folders.\n")

	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.Trim(normalized, "*") == "" {
		if surface == "--only" {
			b.WriteString("  This pattern would keep every file, so remove the --only stage.")
		} else {
			fmt.Fprintf(&b, "  This pattern would exclude every file. If that is intentional, use:\n    %s %s",
				surface, shellQuoteHint("*"))
		}
		return b.String()
	}

	prefix := literalPrefixBeforeDoubleStar(value)
	rawBase := path.Base(normalized)
	if strings.Trim(rawBase, "*") == "" {
		if prefix == "" {
			prefix = "."
		}
		fmt.Fprintf(&b, "  Use the directory target directly:\n    catclip %s", shellQuoteHint(prefix))
		return b.String()
	}
	base := collapseAdjacentStars(rawBase)
	if base != "" && base != "." && base != "/" && !strings.Contains(base, "**") {
		if prefix == "" {
			prefix = "."
		}
		fmt.Fprintf(&b, "  Use a directory target plus a single-star filter:\n    catclip %s %s %s",
			shellQuoteHint(prefix), surface, shellQuoteHint(base))
		return b.String()
	}
	fmt.Fprintf(&b, "  Use a single '*' instead, for example:\n    catclip src %s %s", surface, shellQuoteHint("*.tsx"))
	return b.String()
}

func renderUnsupportedTargetDoubleStar(value string) string {
	prefix := literalPrefixBeforeDoubleStar(value)
	if prefix == "" {
		prefix = "."
	}
	rawBase := path.Base(strings.ReplaceAll(value, "\\", "/"))
	if strings.Trim(rawBase, "*") == "" || rawBase == "" || rawBase == "." || rawBase == "/" {
		return fmt.Sprintf("Error: Positional target patterns do not support '**': %s.\n\n  Use a directory target for recursive traversal:\n    catclip %s",
			singleQuoted(value), shellQuoteHint(prefix))
	}
	base := collapseAdjacentStars(rawBase)
	return fmt.Sprintf("Error: Positional target patterns do not support '**': %s.\n\n  Use a directory target plus --only for recursive file matching:\n    catclip %s --only %s",
		singleQuoted(value), shellQuoteHint(prefix), shellQuoteHint(base))
}

func literalPrefixBeforeDoubleStar(value string) string {
	normalized := strings.ReplaceAll(value, "\\", "/")
	before, _, _ := strings.Cut(normalized, "**")
	before = strings.TrimSuffix(before, "/")
	if before == "" {
		return ""
	}
	for strings.ContainsAny(path.Base(before), "*?[") {
		next := path.Dir(before)
		if next == before || next == "." {
			return ""
		}
		before = next
	}
	return before
}

func collapseAdjacentStars(value string) string {
	for strings.Contains(value, "**") {
		value = strings.ReplaceAll(value, "**", "*")
	}
	return value
}

func shellQuoteHint(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$&;|<>*?[](){}!") {
		return value
	}
	return singleQuoted(strings.ReplaceAll(value, "'", `'"'"'`))
}
