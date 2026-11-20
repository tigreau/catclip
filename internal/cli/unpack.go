package cli

import "strings"

// UnpackOptions is the complete --unpack invocation. It is an action, like
// --hiss: it takes no targets or filters.
type UnpackOptions struct {
	// Path is the bundle file; empty means the clipboard.
	Path     string
	Yes      bool
	Headless bool
	Quiet    bool
	Verbose  bool
	// Print shows the diffs on stdout and writes nothing.
	Print bool
	// CanGoBack is set only by the interactive unpack menu, never parsed from
	// arguments: "n" at the root question and Esc in the picker then return to
	// the menu instead of ending the run.
	CanGoBack bool
}

// FromClipboard reports whether the bundle is read from the clipboard.
func (o UnpackOptions) FromClipboard() bool { return o.Path == "" }

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

const unpackUsageHint = "\n  catclip --unpack           apply the bundle or chat reply you copied to this project" +
	"\n  catclip --unpack FILE      apply a bundle file to this project" +
	"\n  Run it from your project root. It combines only with --print, --yes," +
	"\n  --headless, --quiet and --verbose."

// ParseUnpackArgs parses an --unpack invocation. Without a FILE the bundle is
// read from the clipboard; a next argument starting with "-" is a flag.
func ParseUnpackArgs(args []string) (UnpackOptions, error) {
	var opts UnpackOptions
	seen := false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--unpack":
			if seen {
				return UnpackOptions{}, newUsageError("Error: --unpack is given twice.%s", unpackUsageHint)
			}
			seen = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				if args[i] == "" {
					return UnpackOptions{}, newUsageError("Error: --unpack got an empty file name.%s", unpackUsageHint)
				}
				opts.Path = args[i]
			}
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
			if seen && opts.Path != "" && !strings.HasPrefix(arg, "-") {
				return UnpackOptions{}, newUsageError("Error: --unpack takes one bundle file, not %s too.%s", arg, unpackUsageHint)
			}
			return UnpackOptions{}, newUsageError("Error: --unpack cannot be combined with %s.%s", arg, unpackUsageHint)
		}
	}
	if !seen {
		return UnpackOptions{}, newUsageError("Error: --unpack is missing.%s", unpackUsageHint)
	}
	// --headless never touches the clipboard: an agent or script would
	// otherwise write whatever the user last copied, with no question and no
	// diff.
	if opts.Headless && opts.FromClipboard() {
		return UnpackOptions{}, newUsageError("Error: --unpack --headless needs a bundle file: catclip --unpack FILE --headless\n  --headless never reads the clipboard.")
	}
	return opts, nil
}
