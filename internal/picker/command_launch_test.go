package picker

import (
	"reflect"
	"testing"
)

func TestNormalizeCommandArgsRequiresExplicitMarker(t *testing.T) {
	t.Setenv("FZF_QUERY", "replacement")
	args := []string{"--print", QuerySourceMarker, "--contains", "literal"}
	got, err := NormalizeCommandArgs(args)
	if err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("public command was changed: %q %v", got, err)
	}
}

func TestNormalizeCommandArgsRecoversExactQuery(t *testing.T) {
	for _, query := range []string{"", `--changed | "double" 'single' $dollar %percent% ` + "`tick`", QuerySourceMarker} {
		t.Run(query, func(t *testing.T) {
			t.Setenv("FZF_QUERY", query)
			for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
				for _, tail := range [][]string{{}, {"shell-altered query"}} {
					args := append([]string{commandContextFlag, "--quiet", QuerySourceMarker, flag}, tail...)
					got, err := NormalizeCommandArgs(args)
					want := []string{"--quiet", flag, query}
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("query recovery: got %q, %v; want %q", got, err, want)
					}
				}
			}
		})
	}
}
