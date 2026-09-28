package picker

import (
	"fmt"
	"os"
)

const commandContextFlag = "--internal-picker-command"

// NormalizeCommandArgs recovers exact live queries for marked internal helpers.
// It never changes process cwd or environment.
func NormalizeCommandArgs(args []string) ([]string, error) {
	if len(args) == 0 || args[0] != commandContextFlag {
		return args, nil
	}
	args = args[1:]
	for _, i := range []int{len(args) - 3, len(args) - 2} {
		if i < 0 || args[i] != QuerySourceMarker {
			continue
		}
		flag := args[i+1]
		if flag != "--contains" && flag != "--not-contains" && flag != "--snippet" {
			continue
		}
		query, ok := os.LookupEnv("FZF_QUERY")
		if !ok {
			return nil, fmt.Errorf("picker query environment is missing")
		}
		return append(append([]string(nil), args[:i]...), flag, query), nil
	}
	return args, nil
}
