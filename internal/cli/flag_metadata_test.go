package cli

import (
	"strings"
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

	size, ok := scopeModifierFlagSpecForFlag("--size")
	if !ok {
		t.Fatal("missing --size spec")
	}
	if size.Arity != flagArityOptionalTwo {
		t.Fatalf("expected --size to take zero, one, or two values, got %q", size.Arity)
	}
	if size.Family != flagFamilyFileSetRefinement {
		t.Fatalf("expected --size to be a file-set refinement, got %q", size.Family)
	}
	if size.BoundaryPolicy != scopeStageBoundaryNone {
		t.Fatalf("expected --size to have no boundary policy, got %q", size.BoundaryPolicy)
	}
}

func TestFixedValueCount(t *testing.T) {
	for _, test := range []struct {
		flag string
		want int
	}{
		{flag: "--contains", want: 1},
		{flag: "--input-dir", want: 1},
		{flag: "--internal-prediscovered", want: 1},
		{flag: "--internal-target-inventory", want: 1},
		{flag: "--internal-sink-preview", want: 3},
		{flag: "--only", want: 0},
		{flag: "--recent", want: 0},
		{flag: "--changed", want: 0},
	} {
		if got := FixedValueCount(test.flag); got != test.want {
			t.Errorf("FixedValueCount(%q) = %d, want %d", test.flag, got, test.want)
		}
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
	if !IsModifierBoundaryToken("--size") {
		t.Fatal("expected --size to be a modifier boundary")
	}
	if IsModifierBoundaryToken("Button.tsx") {
		t.Fatal("did not expect plain target to be a modifier boundary")
	}
}

// Phase 1 pins (modifier_wiring_consolidation_v2): optional-value
// consumers must treat rejected-standalone and unknown flags as
// boundaries, not values. These lock current behavior BEFORE the token
// helpers derive from specs — a derivation that silently drops --diff
// from the known-boundary set turns the first case into a recent-limit
// parse error, which these catch.
func TestOptionalValueConsumersStopAtBoundaryFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"recent then rejected-standalone diff", []string{"src", "--recent", "--diff"}, "--diff"},
		{"size then rejected-standalone diff", []string{"src", "--size", "--diff"}, "--diff"},
		{"recent then unknown flag", []string{"src", "--recent", "--foo"}, "Unknown option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseArgs(tc.args)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error mentioning %q, got: %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "--recent takes") || strings.Contains(err.Error(), "--size expects") {
				t.Fatalf("boundary flag was consumed as a value: %v", err)
			}
		})
	}
}

// The derived boundary set must carry --diff and --paths as KNOWN
// boundaries (today they ride the strings.HasPrefix("--") fallback);
// the fallback remains responsible only for tokens absent from every
// metadata table.
func TestModifierBoundaryTokensDerivedFromSpecs(t *testing.T) {
	for _, spec := range ScopeModifierFlagSpecs {
		if _, ok := modifierBoundaryTokens[spec.Flag]; !ok {
			t.Fatalf("spec flag %s missing from derived boundary set", spec.Flag)
		}
	}
	for _, known := range []string{"--diff", "--paths", "--then", "--", "-v", "--hiss"} {
		if _, ok := modifierBoundaryTokens[known]; !ok {
			t.Fatalf("expected %s in the derived known-boundary set", known)
		}
	}
	if _, ok := modifierBoundaryTokens["--foo"]; ok {
		t.Fatal("unknown flags must stay on the fallback path, not the known set")
	}
}

// Bidirectional totality with command's stageFlags table (phase 3):
// the forward direction (every declared kind has a spec with a flag) is
// TestScopeModifierFlagSpecsClassifyEveryDeclaredStageKind — since Flag
// now joins from command.StageFlag, that test failing-or-panicking
// covers "spec kind missing from stageFlags". This covers the reverse:
// a stageFlags entry with no spec is an orphan spelling nothing parses.
func TestCommandStageFlagsHaveMatchingSpecs(t *testing.T) {
	for kind, flag := range command.StageFlags() {
		spec, ok := scopeModifierFlagSpecForStageKind(kind)
		if !ok {
			t.Fatalf("command.StageFlags has %q (%s) but cli has no flag spec for it", kind, flag)
		}
		if spec.Flag != flag {
			t.Fatalf("spec flag %q != command.StageFlags spelling %q for kind %q", spec.Flag, flag, kind)
		}
	}
}
