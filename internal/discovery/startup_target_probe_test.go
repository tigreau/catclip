package discovery

import (
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestAdoptVisibleTargetInventoryFromCompatibleResolver(t *testing.T) {
	source := Resolver{
		Cfg:                  command.Invocation{WorkingDir: "/project"},
		ScopeTargets:         []string{"src"},
		interactiveTargets:   []TargetMatch{{Path: "src/App.tsx", Kind: "file"}},
		interactiveTargetsOk: true,
		IncludedTargets: includedTargetSet{
			exact: map[string]struct{}{"blocked": {}},
		},
	}
	target := Resolver{
		Cfg:          command.Invocation{WorkingDir: "/project"},
		ScopeTargets: []string{"src"},
	}

	if !target.AdoptVisibleTargetInventoryFrom(&source) {
		t.Fatal("compatible resolver did not adopt the visible target inventory")
	}
	if !target.interactiveTargetsOk || len(target.interactiveTargets) != 1 {
		t.Fatalf("adopted inventory = %#v, ready=%v", target.interactiveTargets, target.interactiveTargetsOk)
	}
	if target.hasAnyIncludeActive() {
		t.Fatal("inventory adoption must not copy include authorization")
	}

	source.interactiveTargets[0].Path = "changed.ts"
	if got := target.interactiveTargets[0].Path; got != "src/App.tsx" {
		t.Fatalf("adopted inventory shares mutable backing storage: %q", got)
	}
}

func TestAdoptVisibleTargetInventoryRejectsIncompatibleResolver(t *testing.T) {
	base := Resolver{
		Cfg:                  command.Invocation{WorkingDir: "/project"},
		ScopeTargets:         []string{"src"},
		interactiveTargets:   []TargetMatch{{Path: "src/App.tsx", Kind: "file"}},
		interactiveTargetsOk: true,
	}

	tests := []struct {
		name   string
		target Resolver
	}{
		{name: "working directory", target: Resolver{Cfg: command.Invocation{WorkingDir: "/other"}, ScopeTargets: []string{"src"}}},
		{name: "binary policy", target: Resolver{Cfg: command.Invocation{WorkingDir: "/project"}, WithBinaries: true, ScopeTargets: []string{"src"}}},
		{name: "symlink policy", target: Resolver{Cfg: command.Invocation{WorkingDir: "/project"}, AllowFileSymlinks: true, ScopeTargets: []string{"src"}}},
		{name: "scope targets", target: Resolver{Cfg: command.Invocation{WorkingDir: "/project"}, ScopeTargets: []string{"lib"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.target.AdoptVisibleTargetInventoryFrom(&base) {
				t.Fatal("incompatible resolver adopted the visible target inventory")
			}
		})
	}
}
