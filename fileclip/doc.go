// Package fileclip copies file references to the system clipboard.
//
// Unlike text clipboard packages, fileclip places native OS file references
// on the clipboard — the same format used when you copy a file in Finder.
// This allows pasting files as:
//
//   - File copies in Finder
//   - File attachments in web UIs (Claude, ChatGPT, Gemini)
//   - File drops in applications that accept drag-and-drop
//
// Basic usage:
//
//	fileclip.Copy("/path/to/file.txt")
//
// Multiple files:
//
//	fileclip.Copy("/path/to/a.txt", "/path/to/b.png")
//
// Platform support: macOS (v0.1), Linux (planned), Windows (planned).
package fileclip
