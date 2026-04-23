package catclip

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizePositionalGlobArgsWrapperStar(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*file1*"}, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}

	if !reflect.DeepEqual(result.Args, []string{"file1"}) {
		t.Fatalf("expected normalized args, got %#v", result.Args)
	}
	if got, want := len(result.Hints), 1; got != want {
		t.Fatalf("expected %d hint, got %d", want, got)
	}
	if !strings.Contains(result.Hints[0], "target 'file1' is fuzzy by default") {
		t.Fatalf("expected wrapper-star hint, got %q", result.Hints[0])
	}
}

func TestNormalizePositionalGlobArgsWrapperStarQuietSuppressesHint(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*file1*", "--quiet"}, true)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}

	if !reflect.DeepEqual(result.Args, []string{"file1", "--quiet"}) {
		t.Fatalf("expected normalized args, got %#v", result.Args)
	}
	if len(result.Hints) != 0 {
		t.Fatalf("expected no hints under quiet, got %#v", result.Hints)
	}
}

func TestNormalizePositionalGlobArgsGlobPatternPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*.js"}, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*.js"}) {
		t.Fatalf("expected glob pattern to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsBareExtensionFixIt(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"src", ".tsx", "--changed"}, false)
	if err == nil {
		t.Fatal("expected bare-extension fix-it error")
	}
	if !strings.Contains(err.Error(), "Error: '.tsx' is a bare extension, not a target.") {
		t.Fatalf("expected bare-extension fix-it, got:\n%s", err)
	}
	if !strings.Contains(err.Error(), `catclip src --only "*.tsx" --changed`) {
		t.Fatalf("expected canonical bare-extension fix-it, got:\n%s", err)
	}
}

func TestNormalizePositionalGlobArgsCrossScopeGlobPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*.js", "--then", "*.ts"}, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*.js", "--then", "*.ts"}) {
		t.Fatalf("expected cross-scope globs to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsWrapperStarAndPatternPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*Button*", "*.js"}, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*Button*", "*.js"}) {
		t.Fatalf("expected mixed wrapper-star/pattern to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsDegenerateStarPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*"}, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*"}) {
		t.Fatalf("expected degenerate star to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsInterleavedGlobsPassThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"src", "*.js", "components", "*.tsx"}, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"src", "*.js", "components", "*.tsx"}) {
		t.Fatalf("expected interleaved globs to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsLeavesModifierValuesUntouched(t *testing.T) {
	args := []string{"src", "--only", "*.js"}
	result, err := normalizePositionalGlobArgs(args, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, args) {
		t.Fatalf("expected modifier values untouched, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsLeavesDotfileTargetsUntouched(t *testing.T) {
	args := []string{".gitignore"}
	result, err := normalizePositionalGlobArgs(args, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, args) {
		t.Fatalf("expected dotfile target untouched, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsSkipsConsumedThenLikeModifierValue(t *testing.T) {
	args := []string{"src", "--contains", "--then"}
	result, err := normalizePositionalGlobArgs(args, false)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, args) {
		t.Fatalf("expected consumed modifier value untouched, got %#v", result.Args)
	}
}
