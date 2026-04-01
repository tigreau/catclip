package catclip

import (
	"errors"
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

func TestNormalizePositionalGlobArgsPatternFixIt(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"*.js"}, false)
	if err == nil {
		t.Fatal("expected pattern fix-it error")
	}

	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("expected usageError, got %T", err)
	}
	if !strings.Contains(err.Error(), "Error: '*.js' is a pattern, not a target.") {
		t.Fatalf("expected pattern fix-it, got:\n%s", err)
	}
	if !strings.Contains(err.Error(), `catclip . --only "*.js"`) {
		t.Fatalf("expected canonical pattern fix-it, got:\n%s", err)
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

func TestNormalizePositionalGlobArgsCrossScopeFixIt(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"*.js", "--then", "*.ts"}, false)
	if err == nil {
		t.Fatal("expected cross-scope fix-it error")
	}
	if !strings.Contains(err.Error(), `catclip . --only "*.js" --then . --only "*.ts"`) {
		t.Fatalf("expected full cross-scope canonical fix-it, got:\n%s", err)
	}
}

func TestNormalizePositionalGlobArgsWrapperStarAndPatternFixIt(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"*Button*", "*.js"}, false)
	if err == nil {
		t.Fatal("expected mixed wrapper-star/pattern fix-it error")
	}
	if !strings.Contains(err.Error(), `catclip Button --only "*.js"`) {
		t.Fatalf("expected wrapper-star target normalization inside fix-it, got:\n%s", err)
	}
}

func TestNormalizePositionalGlobArgsDegenerateStarFixIt(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"*"}, false)
	if err == nil {
		t.Fatal("expected degenerate-star fix-it error")
	}
	if !strings.Contains(err.Error(), "Error: '*' is a pattern, not a target.") {
		t.Fatalf("expected pattern error, got:\n%s", err)
	}
	if !strings.Contains(err.Error(), "catclip .") {
		t.Fatalf("expected collapsed dot fix-it, got:\n%s", err)
	}
	if strings.Contains(err.Error(), `--only "*"`) {
		t.Fatalf("expected degenerate star not to suggest --only *, got:\n%s", err)
	}
}

func TestNormalizePositionalGlobArgsAmbiguousInterleaving(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"src", "*.js", "components", "*.tsx"}, false)
	if err == nil {
		t.Fatal("expected ambiguous interleaving error")
	}
	if !strings.Contains(err.Error(), "Patterns and targets can't be interleaved in one scope.") {
		t.Fatalf("expected ambiguity diagnostic, got:\n%s", err)
	}
	if !strings.Contains(err.Error(), `catclip src --only "*.js" --then components --only "*.tsx"`) {
		t.Fatalf("expected --then alternative, got:\n%s", err)
	}
	if !strings.Contains(err.Error(), `catclip src components --only "*.js" "*.tsx"`) {
		t.Fatalf("expected one-scope alternative, got:\n%s", err)
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
