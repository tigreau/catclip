package cli

import (
	"strings"
	"testing"
)

// TestStrictAndPreflightAgreeOnValueConsumption is the phase-4
// differential pin: the strict parser (ParseArgs) and the preflight
// parser (StartupPreflightCommandSpec) now share consumption helpers
// for --recent/--size/--depth and one equals-form rejector, so for
// these vectors they must make the same accept/reject decision with
// the same error text. A divergence means someone edited one parser's
// path without the shared helper.
func TestStrictAndPreflightAgreeOnValueConsumption(t *testing.T) {
	vectors := [][]string{
		{"src", "--recent"},
		{"src", "--recent", "5"},
		{"src", "--recent", "0"},
		{"src", "--recent", "x"},
		{"src", "--recent", "--paths"},
		{"src", "--recent", "--diff"},
		{"src", "--depth", "2"},
		{"src", "--depth", "0"},
		{"src", "--depth"},
		{"src", "--depth", "--paths"},
		{"src", "--size"},
		{"src", "--size", "10"},
		{"src", "--size", "10", "100"},
		{"src", "--size", "x"},
		{"src", "--size", "--diff"},
		{"src", "--recent=5"},
		{"src", "--size=10"},
		{"src", "--depth=2"},
		{"src", "--contains=TODO"},
		{"src", "--not-contains=TODO"},
		{"src", "--snippet=TODO"},
		{"src", "--include=x"},
	}
	for _, args := range vectors {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, strictErr := ParseArgs(args)
			_, preflightErr := StartupPreflightCommandSpec(args)
			if (strictErr == nil) != (preflightErr == nil) {
				t.Fatalf("accept/reject diverges:\n  strict:    %v\n  preflight: %v", strictErr, preflightErr)
			}
			if strictErr != nil && strictErr.Error() != preflightErr.Error() {
				t.Fatalf("error text diverges:\n  strict:    %v\n  preflight: %v", strictErr, preflightErr)
			}
		})
	}
}
