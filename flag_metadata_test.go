package catclip

import "testing"

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
	seenFlags := make(map[string]struct{}, len(scopeModifierFlagSpecs))
	seenKinds := make(map[scopeStageKind]struct{}, len(scopeModifierFlagSpecs))
	for _, spec := range scopeModifierFlagSpecs {
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
}

func TestIsValueTakingFlag(t *testing.T) {
	cases := []string{
		"--include",
		"--only",
		"--exclude",
		"--contains",
		"--snippet",
		"--internal-tree-target",
		"--internal-tree-kind",
		"--internal-tree-state",
		"--internal-file-path",
	}
	for _, arg := range cases {
		if !isValueTakingFlag(arg) {
			t.Fatalf("expected %s to be value-taking", arg)
		}
	}
	if isValueTakingFlag("--changed") {
		t.Fatal("did not expect --changed to be value-taking")
	}
}

func TestIsModifierBoundaryTokenUsesSharedValueTakingFlags(t *testing.T) {
	if !isModifierBoundaryToken("--contains") {
		t.Fatal("expected --contains to be a modifier boundary")
	}
	if !isModifierBoundaryToken("--internal-file-path") {
		t.Fatal("expected --internal-file-path to be a modifier boundary")
	}
	if !isModifierBoundaryToken("--recent") {
		t.Fatal("expected --recent to be a modifier boundary")
	}
	if isModifierBoundaryToken("Button.tsx") {
		t.Fatal("did not expect plain target to be a modifier boundary")
	}
}
