package cli

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Private duplicates of root-package helpers used by parser / validation /
// canonical render machinery. Per reviewer guidance on the v0.6.0 cli/
// extraction: duplicate, do not export, do not move — the root copies
// stay where they are because of heavy non-cli use (resolver fzf
// commands, stage runtime, picker runtime). Mirrors the dup pattern in
// internal/search/helpers.go, internal/git/helpers.go, and
// internal/command/helpers.go.
//
// Drift risk: each helper is small (5-20 lines) with no domain logic and
// is unlikely to change. If any grows substantially, promote to a
// shared package instead of letting the implementations diverge.

const SnippetContextMax = 200

// UsageError is the CLI parsing failure shape. CatclipExitCode lets the root
// process boundary classify it structurally without importing this type.
type UsageError struct {
	Message string
}

func (e UsageError) Error() string {
	return e.Message
}

func (e UsageError) CatclipExitCode() int { return 2 }

func newUsageError(format string, args ...any) error {
	return UsageError{Message: fmt.Sprintf(format, args...)}
}

// normalizeRelPath is the path-string utility used across the parser
// for canonicalizing positional targets and modifier values. Identical
// to root discovery.go's normalizeRelPath.
func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

// hasGlobChars reports whether pattern contains any glob metacharacter.
// Identical to root cli.go's hasGlobChars (kept at root because
// resolver, stage runtime, and picker all use it).
func hasGlobChars(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// ParseRecentLimitToken parses --recent's integer argument. The single
// home for this validator (modifier_wiring_consolidation_v2 phase 2):
// UI pickers import it from here; the former internal/discovery copy is
// deleted — stage appliers take already-parsed ints.
func ParseRecentLimitToken(token string) (int, error) {
	limit, err := strconv.Atoi(token)
	if err != nil || limit <= 0 {
		return 0, newUsageError("Error: --recent takes an optional positive integer.\n  Example: catclip src --recent\n  Example: catclip src --recent 5")
	}
	return limit, nil
}

// ParseDepthToken parses --depth's integer argument. Single home; see
// ParseRecentLimitToken.
func ParseDepthToken(token string) (int, error) {
	depth, err := strconv.Atoi(token)
	if err != nil || depth <= 0 {
		return 0, newUsageError("Error: --depth takes a positive integer.\n  Example: catclip src --depth 2")
	}
	return depth, nil
}

// ParseSizeBoundToken parses one --size bound in KiB. MIN may be zero;
// MAX validation happens after the parser knows whether this token is
// positional bound 0 or 1.
func ParseSizeBoundToken(token string) (int, error) {
	size, err := strconv.Atoi(token)
	if err != nil || size < 0 {
		return 0, SizeInvalidValueError(token)
	}
	return size, nil
}

func SizeInvalidValueError(token string) error {
	return newUsageError("Error: --size expects integer KiB values: --size [MIN [MAX]]\n  MIN may be 0; MAX must be >= 1 (got %q).", token)
}

func SizeTooManyValuesError(extra string) error {
	return newUsageError("Error: --size takes at most two values.\n  Use: catclip src --size\n  Or:  catclip src --size MIN\n  Or:  catclip src --size MIN MAX\n  Extra value: %q", extra)
}

func SizeMaxZeroError() error {
	return newUsageError("Error: --size max must be >= 1 KiB (got 0).\n  Use bare --size or --size 0 for size sorting without an upper bound.")
}

func SizeMaxBeforeMinError(min, max int) error {
	return newUsageError("Error: --size max (%d) must be >= min (%d).\n  Use: --size MIN MAX where MAX >= MIN.", max, min)
}

func SizeEqualsFormError() error {
	return newUsageError("Error: --size requires a space before the value.\n  Use: catclip src --size 100\n  Or:  catclip src --size 0 100")
}

func ValidateSizeBounds(nums []int) error {
	if len(nums) > 2 {
		return SizeTooManyValuesError(strconv.Itoa(nums[2]))
	}
	if len(nums) == 2 {
		min, max := nums[0], nums[1]
		if max == 0 {
			return SizeMaxZeroError()
		}
		if max < min {
			return SizeMaxBeforeMinError(min, max)
		}
	}
	return nil
}

// shellQuoteArg / shellEnforceSingleQuote mirror the POSIX user-facing
// canonical render helpers. The parse-failure fallback switches to the
// PowerShell variants on Windows.

func shellQuoteArg(arg string) string {
	if isPlainShellArg(arg) {
		return arg
	}
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, "$`\\\"!\n\r") {
		return `"` + arg + `"`
	}
	return shellEnforceSingleQuote(arg)
}

func shellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func powershellQuoteArg(arg string) string {
	if isPlainShellArg(arg) {
		return arg
	}
	return powershellEnforceSingleQuote(arg)
}

func powershellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
}

func isPlainShellArg(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("_./:-", r):
		default:
			return false
		}
	}
	return true
}

// singleQuoted unconditionally wraps arg in single quotes (no
// escaping). Used by some validation error message templates.
func singleQuoted(value string) string {
	return "'" + value + "'"
}

// intPtr is a tiny dup of the root recent_stage.go helper. Parser uses
// it for --depth's Stage.Limit.
func intPtr(v int) *int {
	return &v
}

// consumeDepthLimit consumes --depth's required integer arg. Shared by
// the strict and preflight parsers so their value-consumption semantics
// cannot drift (modifier_wiring_consolidation_v2 phase 4, following the
// consumeOptionalSizeBounds precedent).
func consumeDepthLimit(args []string, start int) (*int, int, error) {
	if start >= len(args) {
		return nil, start, RequiredStageValueError("--depth")
	}
	depth, err := ParseDepthToken(args[start])
	if err != nil {
		return nil, start, err
	}
	return intPtr(depth), start + 1, nil
}

// EqualsFormRejectionError rejects "--flag=value" spellings for
// value-taking scope modifiers with a flag-appropriate hint. Returns
// nil when arg isn't an equals form of a spec flag with scalar arity —
// many-valued flags (--only=x, --exclude=x) deliberately
// keep falling through to the unknown-option error, matching the
// hand-written ladders this replaces. Shared verbatim by both parsers;
// a new scalar-arity flag gets equals rejection with the generic
// pattern hint automatically.
// Exported: the startup picker/undo frame parsers pre-empt strict parse
// and must reject with the same text (they had drifted hand ladders that
// missed --not-contains/--snippet before sharing this).
func EqualsFormRejectionError(arg string) error {
	eq := strings.IndexByte(arg, '=')
	if eq < 0 {
		return nil
	}
	flag := arg[:eq]
	spec, ok := scopeModifierFlagSpecForFlag(flag)
	if !ok {
		return nil
	}
	switch spec.Arity {
	case flagArityOne, flagArityOptionalOne, flagArityOptionalTwo:
	default:
		return nil
	}
	switch flag {
	case "--size":
		return SizeEqualsFormError()
	case "--recent":
		return newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
	case "--depth":
		return newUsageError("Error: --depth requires a space before the value.\n  Use: catclip src --depth 2")
	default:
		return newUsageError("Error: %s requires a space before the pattern.\n  Use: catclip src %s 'pattern'\n  Not: catclip src %s='pattern'", flag, flag, flag)
	}
}

// consumeOptionalRecentLimit consumes an optional integer arg for
// --recent. Shared by the strict and preflight parsers.
func consumeOptionalRecentLimit(args []string, start int) (*int, int, error) {
	if start >= len(args) {
		return nil, start, nil
	}
	next := args[start]
	if IsModifierBoundaryToken(next) {
		return nil, start, nil
	}
	limit, err := ParseRecentLimitToken(next)
	if err != nil {
		return nil, start, err
	}
	return intPtr(limit), start + 1, nil
}

func consumeOptionalSizeBounds(args []string, start int) ([]int, int, error) {
	nums := make([]int, 0, 2)
	i := start
	for i < len(args) {
		next := args[i]
		if IsModifierBoundaryToken(next) {
			break
		}
		if len(nums) == 2 {
			return nil, i, SizeTooManyValuesError(next)
		}
		n, err := ParseSizeBoundToken(next)
		if err != nil {
			return nil, i, err
		}
		nums = append(nums, n)
		i++
	}
	if err := ValidateSizeBounds(nums); err != nil {
		return nil, i, err
	}
	return nums, i, nil
}

func cloneSliceStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
