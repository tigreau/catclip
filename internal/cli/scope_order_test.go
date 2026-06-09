package cli

import (
	"github.com/tigreau/catclip/internal/command"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestScopeStageSemanticsClassifiesEveryDeclaredStageKind(t *testing.T) {
	for _, kind := range declaredScopeStageKinds(t) {
		semantics, ok := scopeStageSemanticsForKind(kind)
		if !ok {
			t.Fatalf("missing semantics for %q", kind)
		}
		if semantics.Category == "" {
			t.Fatalf("missing category for %q", kind)
		}
		if semantics.Flag == "" {
			t.Fatalf("missing flag label for %q", kind)
		}
	}
}

func TestCurrentScopeStagesFromArgsUsesCurrentScopeOnly(t *testing.T) {
	stages := currentScopeStagesFromArgs([]string{
		"src",
		"--contains", "TODO",
		"--recent", "5",
		"--then",
		"docs",
		"--changed-diff",
	})

	got := make([]command.StageKind, 0, len(stages))
	for _, stage := range stages {
		got = append(got, stage.Kind)
	}
	want := []command.StageKind{command.StageChangedDiff}
	if !slices.Equal(got, want) {
		t.Fatalf("expected current-scope stages %v, got %v", want, got)
	}
}

func TestCurrentScopeStagesFromArgsTreatsModifierLikeRegexAsValue(t *testing.T) {
	stages := currentScopeStagesFromArgs([]string{"src", "--contains", "--snippet"})

	got := make([]command.StageKind, 0, len(stages))
	for _, stage := range stages {
		got = append(got, stage.Kind)
	}
	want := []command.StageKind{command.StageContains}
	if !slices.Equal(got, want) {
		t.Fatalf("expected current-scope stages %v, got %v", want, got)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsSnippetAfterModifierLikeContainsValue(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--contains", "--snippet"}, []string{"--snippet"})
	if err != nil {
		t.Fatalf("expected snippet after modifier-like contains value to remain valid, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsContainsAfterDiff(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--changed-diff"}, []string{"--contains"})
	if err == nil {
		t.Fatal("expected contains after diff to fail")
	}
	if !strings.Contains(err.Error(), "--contains must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsContainsAfterSnippet(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--snippet", "TODO"}, []string{"--contains"})
	if err == nil {
		t.Fatal("expected contains after snippet to fail")
	}
	if !strings.Contains(err.Error(), "--contains must come before --snippet in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsDiffAfterSnippet(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--snippet", "TODO"}, []string{"--changed-diff"})
	if err == nil {
		t.Fatal("expected diff after snippet to fail")
	}
	if !strings.Contains(err.Error(), "--snippet and --diff cannot be combined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsSnippetAfterContains(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--contains", "TODO", "--only", "src/main.ts"}, []string{"--snippet"})
	if err != nil {
		t.Fatalf("expected snippet after earlier filters to remain valid, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsRecentAfterDiff(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--changed-diff"}, []string{"--recent"})
	if err != nil {
		t.Fatalf("expected recent after diff to remain valid, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsDepthAfterDiff(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--changed-diff"}, []string{"--depth"})
	if err != nil {
		t.Fatalf("expected depth after diff to remain valid, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsContainsAfterPaths(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--paths"}, []string{"--contains"})
	if err == nil {
		t.Fatal("expected contains after --paths to fail")
	}
	if !strings.Contains(err.Error(), "--paths finalizes the current scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScopeStageBoundaryPolicies(t *testing.T) {
	if !scopeStageBoundaryAllowsCategory(scopeStageBoundaryDiff, scopeStageCategorySetRefinement) {
		t.Fatal("diff boundary should allow later set refinement")
	}
	if scopeStageBoundaryAllowsCategory(scopeStageBoundaryDiff, scopeStageCategoryContentFilter) {
		t.Fatal("diff boundary should reject later content filters")
	}
	if scopeStageBoundaryAllowsCategory(scopeStageBoundaryDiff, scopeStageCategoryGitChangeFilter) {
		t.Fatal("diff boundary should reject later git change filters")
	}
	if !scopeStageBoundaryAllowsCategory(scopeStageBoundaryDiff, scopeStageCategoryOutputMode) {
		t.Fatal("diff boundary should defer output-mode rejection to conflict validation")
	}
	if scopeStageBoundaryAllowsCategory(scopeStageBoundarySnippet, scopeStageCategoryContentFilter) {
		t.Fatal("snippet boundary should reject later content filters")
	}
	if !scopeStageBoundaryAllowsCategory(scopeStageBoundarySnippet, scopeStageCategoryGitChangeFilter) {
		t.Fatal("snippet boundary should still allow later git change filters")
	}
	if scopeStageBoundaryAllowsCategory(scopeStageBoundaryTerminal, scopeStageCategorySetRefinement) {
		t.Fatal("terminal boundary should reject later same-scope stages")
	}
	if scopeStageBoundaryAllowsCategory(scopeStageBoundaryTerminal, scopeStageCategoryOutputMode) {
		t.Fatal("terminal boundary should reject later same-scope output modes")
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsIncludeAfterModifier(t *testing.T) {
	for _, prior := range []string{"--only", "--exclude", "--changed", "--contains", "--recent"} {
		args := []string{"src", prior}
		if prior == "--only" || prior == "--exclude" || prior == "--contains" {
			args = append(args, "val")
		}
		err := ValidateCurrentScopeFlagSequence(args, []string{"--include"})
		if err == nil {
			t.Fatalf("expected --include after %s to fail", prior)
		}
		if !strings.Contains(err.Error(), "--include must come before other modifiers") {
			t.Fatalf("unexpected error after %s: %v", prior, err)
		}
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsRepeatedInclude(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--include", "vendor"}, []string{"--include"})
	if err == nil {
		t.Fatal("expected repeated --include to fail")
	}
	if !strings.Contains(err.Error(), "--include can only appear once per scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsIncludeFirst(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src"}, []string{"--include"})
	if err != nil {
		t.Fatalf("expected --include as first modifier to succeed, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsModifierAfterInclude(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--include", "vendor"}, []string{"--only"})
	if err != nil {
		t.Fatalf("expected --only after --include to succeed, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceIncludeAfterThenResetsScope(t *testing.T) {
	err := ValidateCurrentScopeFlagSequence([]string{"src", "--only", "*.ts", "--then", "docs"}, []string{"--include"})
	if err != nil {
		t.Fatalf("expected --include after --then to succeed, got %v", err)
	}
}

func declaredScopeStageKinds(t *testing.T) []command.StageKind {
	t.Helper()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// Constants moved out of main.go and into internal/command/stage.go in
	// the v0.6.0 command extraction. The scan keeps the same shape; just
	// the path changed.
	stagePath := filepath.Join(filepath.Dir(testFile), "..", "command", "stage.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, stagePath, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(%q) returned error: %v", stagePath, err)
	}

	var kinds []command.StageKind
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}

		inStageKindBlock := false
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := valueSpec.Type.(*ast.Ident); ok && ident.Name == "StageKind" {
				inStageKindBlock = true
			} else if valueSpec.Type != nil {
				inStageKindBlock = false
			}
			if !inStageKindBlock {
				continue
			}
			for _, value := range valueSpec.Values {
				lit, ok := value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("Unquote(%q) returned error: %v", lit.Value, err)
				}
				kinds = append(kinds, command.StageKind(unquoted))
			}
		}
	}

	if len(kinds) == 0 {
		t.Fatalf("expected to discover StageKind constants in %s", stagePath)
	}
	return kinds
}
