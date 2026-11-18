package cli

import "strings"

// UnpackOptions is the complete --unpack invocation. It is an action, like
// --hiss: it takes no targets or filters.
type UnpackOptions struct {
	Path     string
	Yes      bool
	Headless bool
	Quiet    bool
	Verbose  bool
	// Print shows the diffs on stdout and writes nothing.
	Print bool
}

// IsUnpackRequest reports whether args ask for --unpack as a flag, not as the
// value of another option (for example a --contains regex).
func IsUnpackRequest(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--unpack" {
			return true
		}
		i += FixedValueCount(args[i])
	}
	return false
}

const unpackUsageHint = "\n  Run it from your project root: catclip --unpack bundle.txt" +
	"\n  It combines only with --print, --yes, --headless, --quiet and --verbose."

// ParseUnpackArgs parses an --unpack invocation.
func ParseUnpackArgs(args []string) (UnpackOptions, error) {
	var opts UnpackOptions
	seen := false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--unpack":
			if seen {
				return UnpackOptions{}, newUsageError("Error: --unpack takes one bundle file.%s", unpackUsageHint)
			}
			seen = true
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") || args[i+1] == "" {
				return UnpackOptions{}, newUsageError("Error: --unpack needs the bundle file to read.%s", unpackUsageHint)
			}
			i++
			opts.Path = args[i]
		case "-y", "--yes":
			opts.Yes = true
		case "-p", "--print":
			opts.Print = true
		case "--headless":
			opts.Headless = true
		case "-q", "--quiet":
			opts.Quiet = true
		case "-v", "--verbose":
			opts.Verbose = true
		default:
			return UnpackOptions{}, newUsageError("Error: --unpack cannot be combined with %s.%s", arg, unpackUsageHint)
		}
	}
	if !seen {
		return UnpackOptions{}, newUsageError("Error: --unpack needs the bundle file to read.%s", unpackUsageHint)
	}
	return opts, nil
}
