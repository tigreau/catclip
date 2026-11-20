package catclip

import "regexp"

// rootAnsiEscape matches SGR sequences chroma's TTY formatter emits so root
// tests can inspect preview output without depending on a UI test-only helper.
var rootAnsiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)
