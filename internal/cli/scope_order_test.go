package cli

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/tigreau/catclip/internal/command"
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

func TestCurrentScopeStagesFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []command.StageKind
	}{
		{
			name: "uses only the scope after then",
			args: []string{"src", "--contains", "TODO", "--recent", "5", "--then", "docs", "--changed-diff"},
			want: []command.StageKind{command.StageChangedDiff},
		},
		{
			name: "treats modifier-like regex as a value",
			args: []string{"src", "--contains", "--snippet"},
			want: []command.StageKind{command.StageContains},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stages := currentScopeStagesFromArgs(tt.args)
			got := make([]command.StageKind, 0, len(stages))
			for _, stage := range stages {
				got = append(got, stage.Kind)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("stage kinds = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateCurrentScopeFlagSequence(t *testing.T) {
	tests := []struct {
		name         string
		currentArgs  []string
		flags        []string
		wantReason   Reason
		wantFlag     string
		wantBoundary string
		wantNext     string
	}{
		{name: "modifier-like contains value then snippet", currentArgs: []string{"src", "--contains", "--snippet"}, flags: []string{"--snippet"}},
		{name: "contains after diff", currentArgs: []string{"src", "--changed-diff"}, flags: []string{"--contains"}, wantReason: ReasonDiffContentFilterOrder, wantFlag: "--contains", wantBoundary: "--changed-diff"},
		{name: "contains after snippet", currentArgs: []string{"src", "--snippet", "TODO"}, flags: []string{"--contains"}, wantReason: ReasonSnippetContentFilterOrder, wantFlag: "--contains"},
		{name: "diff after snippet", currentArgs: []string{"src", "--snippet", "TODO"}, flags: []string{"--changed-diff"}, wantReason: ReasonDiffSnippetConflict},
		{name: "snippet after contains", currentArgs: []string{"src", "--contains", "TODO", "--only", "src/main.ts"}, flags: []string{"--snippet"}},
		{name: "recent after diff", currentArgs: []string{"src", "--changed-diff"}, flags: []string{"--recent"}},
		{name: "depth after diff", currentArgs: []string{"src", "--changed-diff"}, flags: []string{"--depth"}},
		{name: "contains after paths", currentArgs: []string{"src", "--paths"}, flags: []string{"--contains"}, wantReason: ReasonTerminalBoundaryOrder, wantBoundary: "--paths", wantNext: "--contains"},
		{name: "include after only", currentArgs: []string{"src", "--only", "val"}, flags: []string{"--include"}, wantReason: ReasonIncludeAfterModifier},
		{name: "include after exclude", currentArgs: []string{"src", "--exclude", "val"}, flags: []string{"--include"}, wantReason: ReasonIncludeAfterModifier},
		{name: "include after changed", currentArgs: []string{"src", "--changed"}, flags: []string{"--include"}, wantReason: ReasonIncludeAfterModifier},
		{name: "include after contains", currentArgs: []string{"src", "--contains", "val"}, flags: []string{"--include"}, wantReason: ReasonIncludeAfterModifier},
		{name: "include after recent", currentArgs: []string{"src", "--recent"}, flags: []string{"--include"}, wantReason: ReasonIncludeAfterModifier},
		{name: "repeated include", currentArgs: []string{"src", "--include", "vendor"}, flags: []string{"--include"}, wantReason: ReasonRepeatedInclude},
		{name: "no ignore after only", currentArgs: []string{"src", "--only", "val"}, flags: []string{"--no-ignore"}, wantReason: ReasonNoIgnoreAfterModifier},
		{name: "repeated no ignore", currentArgs: []string{"src", "--no-ignore"}, flags: []string{"--no-ignore"}, wantReason: ReasonRepeatedNoIgnore},
		{name: "include after no ignore", currentArgs: []string{"src", "--no-ignore"}, flags: []string{"--include"}, wantReason: ReasonIncludeNoIgnoreConflict},
		{name: "no ignore after include", currentArgs: []string{"src", "--include", "vendor"}, flags: []string{"--no-ignore"}, wantReason: ReasonIncludeNoIgnoreConflict},
		{name: "include first", currentArgs: []string{"src"}, flags: []string{"--include"}},
		{name: "no ignore first", currentArgs: []string{"src"}, flags: []string{"--no-ignore"}},
		{name: "modifier after include", currentArgs: []string{"src", "--include", "vendor"}, flags: []string{"--only"}},
		{name: "then resets include ordering", currentArgs: []string{"src", "--only", "*.ts", "--then", "docs"}, flags: []string{"--include"}},
		{name: "then resets no ignore ordering", currentArgs: []string{"src", "--no-ignore", "--only", "*.ts", "--then", "docs"}, flags: []string{"--no-ignore"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCurrentScopeFlagSequence(tt.currentArgs, tt.flags)
			if tt.wantReason == "" {
				if err != nil {
					t.Fatalf("expected sequence to be valid, got %v", err)
				}
				return
			}
			var failure ValidationFailure
			if !errors.As(err, &failure) {
				t.Fatalf("expected ValidationFailure reason %q, got %T: %v", tt.wantReason, err, err)
			}
			if failure.Reason != tt.wantReason || failure.Flag != tt.wantFlag || failure.BoundaryFlag != tt.wantBoundary || failure.NextFlag != tt.wantNext {
				t.Fatalf("validation failure = %#v, want reason=%q flag=%q boundary=%q next=%q", failure, tt.wantReason, tt.wantFlag, tt.wantBoundary, tt.wantNext)
			}
		})
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
