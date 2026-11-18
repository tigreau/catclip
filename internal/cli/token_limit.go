package cli

import (
	"github.com/tigreau/catclip/internal/command"
	"math"
	"strconv"
)

// ConsumeTokenLimit consumes a numeric operand only. This lets the global flag
// precede a target without confusing a path with its optional budget. Numeric
// target names can be disambiguated with ./ (as with other numeric options).
func ConsumeTokenLimit(args []string, start int) (int64, int, error) {
	if start == len(args) || args[start] == "" {
		return command.DefaultTokenLimit, start, nil
	}
	s := args[start]
	c := s[0]
	if (c < '0' || c > '9') && c != '+' && c != '-' {
		return command.DefaultTokenLimit, start, nil
	}
	if IsModifierBoundaryToken(s) || (c == '-' && (len(s) < 2 || s[1] < '0' || s[1] > '9')) {
		return command.DefaultTokenLimit, start, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/4 {
		return 0, start, newUsageError("Error: --token-limit expects a positive whole number no larger than %d.", int64(math.MaxInt64/4))
	}
	return n, start + 1, nil
}
