package cli

import "slices"

// NormalizeHiddenAliases accepts shorthand while retaining the long spelling
// in errors and canonical commands. Fixed-arity operands stay literal.
// The common path returns the caller's original slice without allocating.
func NormalizeHiddenAliases(args []string) []string {
	if !slices.Contains(args, "-ni") && !slices.Contains(args, "-ot") {
		return args
	}
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-ni" || args[i] == "-ot" {
			if out == nil {
				out = append([]string(nil), args...)
			}
			if args[i] == "-ot" {
				out[i] = "--only-tree"
			} else {
				out[i] = "--no-ignore"
			}
		}
		i += FixedValueCount(args[i])
	}
	if out == nil {
		return args
	}
	return out
}
