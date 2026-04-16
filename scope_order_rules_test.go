package catclip

import (
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

	got := make([]scopeStageKind, 0, len(stages))
	for _, stage := range stages {
		got = append(got, stage.Kind)
	}
	want := []scopeStageKind{scopeStageChangedDiff}
	if !slices.Equal(got, want) {
		t.Fatalf("expected current-scope stages %v, got %v", want, got)
	}
}

func TestCurrentScopeStagesFromArgsTreatsModifierLikeRegexAsValue(t *testing.T) {
	stages := currentScopeStagesFromArgs([]string{"src", "--contains", "--snippet"})

	got := make([]scopeStageKind, 0, len(stages))
	for _, stage := range stages {
		got = append(got, stage.Kind)
	}
	want := []scopeStageKind{scopeStageContains}
	if !slices.Equal(got, want) {
		t.Fatalf("expected current-scope stages %v, got %v", want, got)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsSnippetAfterModifierLikeContainsValue(t *testing.T) {
	err := validateCurrentScopeFlagSequence([]string{"src", "--contains", "--snippet"}, []string{"--snippet"})
	if err != nil {
		t.Fatalf("expected snippet after modifier-like contains value to remain valid, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsContainsAfterDiff(t *testing.T) {
	err := validateCurrentScopeFlagSequence([]string{"src", "--changed-diff"}, []string{"--contains"})
	if err == nil {
		t.Fatal("expected contains after diff to fail")
	}
	if !strings.Contains(err.Error(), "--contains must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsContainsAfterSnippet(t *testing.T) {
	err := validateCurrentScopeFlagSequence([]string{"src", "--snippet", "TODO"}, []string{"--contains"})
	if err == nil {
		t.Fatal("expected contains after snippet to fail")
	}
	if !strings.Contains(err.Error(), "--contains must come before --snippet in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceRejectsDiffAfterSnippet(t *testing.T) {
	err := validateCurrentScopeFlagSequence([]string{"src", "--snippet", "TODO"}, []string{"--changed-diff"})
	if err == nil {
		t.Fatal("expected diff after snippet to fail")
	}
	if !strings.Contains(err.Error(), "--snippet and --diff cannot be combined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsSnippetAfterContains(t *testing.T) {
	err := validateCurrentScopeFlagSequence([]string{"src", "--contains", "TODO", "--only", "src/main.ts"}, []string{"--snippet"})
	if err != nil {
		t.Fatalf("expected snippet after earlier filters to remain valid, got %v", err)
	}
}

func TestValidateCurrentScopeFlagSequenceAllowsRecentAfterDiff(t *testing.T) {
	err := validateCurrentScopeFlagSequence([]string{"src", "--changed-diff"}, []string{"--recent"})
	if err != nil {
		t.Fatalf("expected recent after diff to remain valid, got %v", err)
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
}

func declaredScopeStageKinds(t *testing.T) []scopeStageKind {
	t.Helper()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	mainPath := filepath.Join(filepath.Dir(testFile), "main.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(%q) returned error: %v", mainPath, err)
	}

	var kinds []scopeStageKind
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}

		inScopeStageKindBlock := false
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := valueSpec.Type.(*ast.Ident); ok && ident.Name == "scopeStageKind" {
				inScopeStageKindBlock = true
			} else if valueSpec.Type != nil {
				inScopeStageKindBlock = false
			}
			if !inScopeStageKindBlock {
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
				kinds = append(kinds, scopeStageKind(unquoted))
			}
		}
	}

	if len(kinds) == 0 {
		t.Fatal("expected to discover scopeStageKind constants in main.go")
	}
	return kinds
}
