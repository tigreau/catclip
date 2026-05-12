package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tigreau/catclip/fileclip"
)

const usage = `fileclip — copy file references to the clipboard

Usage:
  fileclip <path> [path...]         Copy file ref(s) to clipboard
  command | fileclip                Read paths from stdin, copy as file refs
  fileclip --paste                  Read file refs from clipboard
  fileclip --has                    Check if clipboard has file refs
  fileclip --help                   Show this message

Examples:
  fileclip report.txt                        Copy one file
  fileclip a.txt b.txt c.txt                 Copy multiple files
  find . -name "*.go" | fzf | fileclip      Pick with fzf, copy ref
  rg --files -g "*.go" | fileclip           Copy all Go file refs
  fileclip --has && fileclip --paste         Read back if present`

func main() {
	// No args and stdin is a pipe → read paths from stdin.
	if len(os.Args) < 2 {
		if stdinIsPipe() {
			copyFromStdin()
			return
		}
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "--help", "-h":
		fmt.Println(usage)

	case "--paste":
		paths, err := fileclip.Paste()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, p := range paths {
			fmt.Println(p)
		}

	case "--has":
		has, err := fileclip.Has()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if has {
			fmt.Println("true")
		} else {
			fmt.Println("false")
			os.Exit(1) // exit 1 = no file refs, for shell scripting
		}

	default:
		// Any non-flag argument is a path → default to copy.
		var paths []string
		for _, arg := range os.Args[1:] {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n\n%s\n", arg, usage)
				os.Exit(1)
			}
			paths = append(paths, arg)
		}
		if err := fileclip.Copy(paths...); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("✓ copied %d file ref(s) to clipboard\n", len(paths))
	}
}

// stdinIsPipe returns true if stdin is a pipe (not a terminal).
func stdinIsPipe() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// copyFromStdin reads newline-separated paths from stdin, trims
// whitespace, skips blank lines, and copies them as file references.
func copyFromStdin() {
	var paths []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			paths = append(paths, line)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "No paths received from stdin")
		os.Exit(1)
	}
	if err := fileclip.Copy(paths...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("✓ copied %d file ref(s) to clipboard\n", len(paths))
}
