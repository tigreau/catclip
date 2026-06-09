package cli

import (
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestScopeModifierFlagSpecsClassifyEveryDeclaredStageKind(t *testing.T) {
	for _, kind := range declaredScopeStageKinds(t) {
		spec, ok := scopeModifierFlagSpecForStageKind(kind)
		if !ok {
			t.Fatalf("missing flag spec for stage kind %q", kind)
		}
		if spec.Flag == "" {
			t.Fatalf("missing flag label for %q", kind)
		}
		if spec.Arity == "" {
			t.Fatalf("missing arity for %q", kind)
		}
		if spec.Family == "" {
			t.Fatalf("missing semantic family for %q", kind)
		}
		if spec.Recoverability == "" {
			t.Fatalf("missing recoverability for %q", kind)
		}
		if spec.StageKind != kind {
			t.Fatalf("expected spec for %q to point at same stage kind, got %q", kind, spec.StageKind)
		}
	}
}

func TestScopeModifierFlagSpecsAreUnique(t *testing.T) {
	seenFlags := make(map[string]struct{}, len(ScopeModifierFlagSpecs))
	seenKinds := make(map[command.StageKind]struct{}, len(ScopeModifierFlagSpecs))
	for _, spec := range ScopeModifierFlagSpecs {
		if _, ok := seenFlags[spec.Flag]; ok {
			t.Fatalf("duplicate flag spec for %q", spec.Flag)
		}
		seenFlags[spec.Flag] = struct{}{}
		if _, ok := seenKinds[spec.StageKind]; ok {
			t.Fatalf("duplicate stage kind spec for %q", spec.StageKind)
		}
		seenKinds[spec.StageKind] = struct{}{}
	}
}

func TestScopeModifierFlagSpecsClassifyContentFamily(t *testing.T) {
	contains, ok := scopeModifierFlagSpecForFlag("--contains")
	if !ok {
		t.Fatal("missing --contains spec")
	}
	if contains.Arity != flagArityOne {
		t.Fatalf("expected --contains to take one value, got %q", contains.Arity)
	}
	if contains.Family != flagFamilyContentFilter {
		t.Fatalf("expected --contains to be a content filter, got %q", contains.Family)
	}

	snippet, ok := scopeModifierFlagSpecForFlag("--snippet")
	if !ok {
		t.Fatal("missing --snippet spec")
	}
	if snippet.Arity != flagArityOne {
		t.Fatalf("expected --snippet to take one value, got %q", snippet.Arity)
	}
	if snippet.Family != flagFamilyOutputMode {
		t.Fatalf("expected --snippet to be an output mode, got %q", snippet.Family)
	}
	if snippet.BoundaryPolicy != scopeStageBoundarySnippet {
		t.Fatalf("expected --snippet boundary policy, got %q", snippet.BoundaryPolicy)
	}

	paths, ok := scopeModifierFlagSpecForFlag("--paths")
	if !ok {
		t.Fatal("missing --paths spec")
	}
	if paths.Arity != flagArityNone {
		t.Fatalf("expected --paths to take no value, got %q", paths.Arity)
	}
	if paths.Family != flagFamilyOutputMode {
		t.Fatalf("expected --paths to be an output mode, got %q", paths.Family)
	}
	if paths.BoundaryPolicy != scopeStageBoundaryTerminal {
		t.Fatalf("expected --paths terminal boundary policy, got %q", paths.BoundaryPolicy)
	}
}

func TestIsValueTakingFlag(t *testing.T) {
	cases := []string{
		"--include",
		"--only",
		"--exclude",
		"--depth",
		"--contains",
		"--snippet",
		"--internal-tree-target",
		"--internal-tree-kind",
		"--internal-tree-state",
		"--internal-file-path",
		"--input-dir",
		"--input-stem",
	}
	for _, arg := range cases {
		if !IsValueTakingFlag(arg) {
			t.Fatalf("expected %s to be value-taking", arg)
		}
	}
	if IsValueTakingFlag("--changed") {
		t.Fatal("did not expect --changed to be value-taking")
	}
}

func TestIsModifierBoundaryTokenUsesSharedValueTakingFlags(t *testing.T) {
	if !IsModifierBoundaryToken("--contains") {
		t.Fatal("expected --contains to be a modifier boundary")
	}
	if !IsModifierBoundaryToken("--internal-file-path") {
		t.Fatal("expected --internal-file-path to be a modifier boundary")
	}
	if !IsModifierBoundaryToken("--recent") {
		t.Fatal("expected --recent to be a modifier boundary")
	}
	if IsModifierBoundaryToken("Button.tsx") {
		t.Fatal("did not expect plain target to be a modifier boundary")
	}
}
