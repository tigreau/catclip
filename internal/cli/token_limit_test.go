package cli

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestTokenLimitGlobalParsing(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		limit int64
	}{
		{[]string{"src", "--token-limit"}, command.DefaultTokenLimit},
		{[]string{"-tl", "src"}, command.DefaultTokenLimit},
		{[]string{"--token-limit", "12", "src"}, 12},
		{[]string{"src", "--only", "*.go", "-tl", "12", "--then", "docs", "--paths"}, 12},
		{[]string{"src", "--contains", "-tl"}, 0},
		{[]string{"src", "--token-limit", "--headless"}, command.DefaultTokenLimit},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cfg, err := ParseArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TokenLimit != tc.limit {
				t.Fatalf("limit %d, want %d", cfg.TokenLimit, tc.limit)
			}
			if err := ValidateStartupPreflightArgs(tc.args); err != nil {
				t.Fatal(err)
			}
			args := command.CanonicalGlobalArgs(command.InvocationFromParsed(cfg), command.RenderFlagsFromParsed(cfg))
			if tc.limit > 0 && !reflect.DeepEqual(args[:2], []string{"--token-limit", strconv.FormatInt(tc.limit, 10)}) {
				t.Fatal(args)
			}
		})
	}
}

func TestTokenLimitRejectsInvalidNumericOperand(t *testing.T) {
	for _, value := range []string{"0", "-1", "1.5", "1k", "+bad", "2305843009213693952", "9999999999999999999999999"} {
		args := []string{"src", "--token-limit", value}
		if _, err := ParseArgs(args); err == nil {
			t.Errorf("accepted %q", value)
		}
		if err := ValidateStartupPreflightArgs(args); err == nil {
			t.Errorf("preflight accepted %q", value)
		}
	}
}
