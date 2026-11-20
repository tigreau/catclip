// Package catclip only embeds the repository VERSION file. VERSION must stay
// at the repository root because the install scripts and release tooling read
// it there, and go:embed cannot reach a parent directory. The application
// itself lives in internal/catclip.
package catclip

import _ "embed"

// SourceVersion is the content of the repository VERSION file.
//
//go:embed VERSION
var SourceVersion string
