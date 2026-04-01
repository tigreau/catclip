package catclip

import "testing"

func TestIsValueTakingFlag(t *testing.T) {
	cases := []string{
		"--include",
		"--only",
		"--exclude",
		"--contains",
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
	if isModifierBoundaryToken("Button.tsx") {
		t.Fatal("did not expect plain target to be a modifier boundary")
	}
}
