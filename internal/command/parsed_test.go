package command

import (
	"reflect"
	"testing"
)

// TestParsedIsInternalKindCoversEveryInternalField pins both the predicate and
// the command-owned Parsed -> Invocation mapper for every internal command
// field. A missing field could otherwise re-enable prompts in an fzf child.
func TestParsedIsInternalKindCoversEveryInternalField(t *testing.T) {
	cases := []struct {
		name string
		cfg  Parsed
	}{
		{name: "TreePreview", cfg: Parsed{TreePreview: true}},
		{name: "FilePreview", cfg: Parsed{FilePreview: true}},
		{name: "FileSearchingPreview", cfg: Parsed{FileSearchingPreview: true}},
		{name: "ContentMatchList", cfg: Parsed{ContentMatchList: true}},
		{name: "SnippetBoundaryPreview", cfg: Parsed{SnippetBoundaryPreview: true}},
		{name: "RecentPreview", cfg: Parsed{RecentPreview: true}},
		{name: "LinesPreview", cfg: Parsed{LinesPreview: true}},
		{name: "PrediscoveredPath", cfg: Parsed{PrediscoveredPath: "/tmp/ck.json"}},
		{name: "TargetPreviewInventory", cfg: Parsed{TargetPreviewInventory: "/tmp/targets.bin"}},
		{name: "TreeInputDir", cfg: Parsed{TreeInputDir: "/tmp"}},
		{name: "FileSetSelectionPath", cfg: Parsed{FileSetSelectionPath: "/tmp/selected"}},
		{name: "FileSetSelectionStage", cfg: Parsed{FileSetSelectionStage: "exclude"}},
		{name: "SinkTogglePath", cfg: Parsed{SinkTogglePath: "/tmp/toggle"}},
		{name: "SinkPreviewModePath", cfg: Parsed{SinkPreviewModePath: "/tmp/mode"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.cfg.IsInternalKind() {
				t.Fatalf("IsInternalKind() = false; expected true when %s is set", tc.name)
			}
			if !InvocationFromParsed(tc.cfg).Internal {
				t.Fatalf("InvocationFromParsed().Internal = false; expected true when %s is set", tc.name)
			}
		})
	}
}

func TestParsedIsInternalKindReturnsFalseForUserFacingRun(t *testing.T) {
	cfg := Parsed{
		Action:     ActionRun,
		Version:    "0.6.0",
		Platform:   "macos",
		WorkingDir: "/tmp",
		Verbose:    true,
		Quiet:      false,
		Headless:   false,
	}
	if cfg.IsInternalKind() {
		t.Fatalf("IsInternalKind() = true on a plain user invocation; want false")
	}
}

// TestInvocationFromParsedSetsInternalKind verifies that the mapper
// threads the IsInternalKind result through to Invocation.Internal.
// Catches the original 3905793 bug at the mapper boundary, not just at
// the predicate level.
func TestInvocationFromParsedSetsInternalKind(t *testing.T) {
	internalCfg := Parsed{TreePreview: true, Version: "0.6.0"}
	inv := InvocationFromParsed(internalCfg)
	if !inv.Internal {
		t.Fatalf("InvocationFromParsed: Internal = false for TreePreview Parsed; want true")
	}

	userCfg := Parsed{Action: ActionRun, Version: "0.6.0"}
	inv = InvocationFromParsed(userCfg)
	if inv.Internal {
		t.Fatalf("InvocationFromParsed: Internal = true for plain ActionRun; want false")
	}
}

func TestInvocationFromParsedCopiesRuntimeFields(t *testing.T) {
	cfg := Parsed{
		Version:      "1.2.3",
		Platform:     "windows",
		WorkingDir:   `C:\work\project`,
		Verbose:      true,
		Quiet:        true,
		Headless:     true,
		WithBinaries: true,
		LinesPreview: true,
	}
	want := Invocation{
		Version:      "1.2.3",
		Platform:     "windows",
		WorkingDir:   `C:\work\project`,
		Verbose:      true,
		Quiet:        true,
		Headless:     true,
		WithBinaries: true,
		Internal:     true,
	}
	if got := InvocationFromParsed(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("InvocationFromParsed() = %#v, want %#v", got, want)
	}
}

func TestResolvedFromParsedUsesCanonicalScopes(t *testing.T) {
	scopes := []ExecutionScope{
		{Targets: []string{"src"}, NoIgnore: true, Stages: []Stage{{Kind: StageOnly, Values: []string{"*.tsx"}}}},
		{Targets: []string{"docs"}, Paths: true},
	}
	cfg := Parsed{
		Version: "1.2.3",
		Command: FinalizedSpecFromExecutionScopes(scopes),
	}
	got := ResolvedFromParsed(cfg)
	if got.Config.Version != "1.2.3" {
		t.Fatalf("ResolvedFromParsed().Config.Version = %q, want 1.2.3", got.Config.Version)
	}
	if !reflect.DeepEqual(got.Scopes, scopes) {
		t.Fatalf("ResolvedFromParsed().Scopes = %#v, want %#v", got.Scopes, scopes)
	}
}
