package command

import "testing"

// TestParsedIsInternalKindCoversEveryInternalField pins the contract
// that Parsed.IsInternalKind returns true for every field the root
// internalCommandConfig.isInternalKind predicate watches. Caught in
// review of 3905793: command.InvocationFromParsed initially dropped
// Internal, so internal preview/reload commands would have lost prompt
// suppression once the parser extraction switched callers to the
// command-side mapper. The fix added IsInternalKind here; this test
// prevents the field set from drifting out of sync with the root
// predicate.
//
// Update both predicates together when a new internal-kind field is
// added to Parsed.
func TestParsedIsInternalKindCoversEveryInternalField(t *testing.T) {
	cases := []struct {
		name string
		cfg  Parsed
	}{
		{name: "TreePreview", cfg: Parsed{TreePreview: true}},
		{name: "FilePreview", cfg: Parsed{FilePreview: true}},
		{name: "ContentMatchList", cfg: Parsed{ContentMatchList: true}},
		{name: "SnippetBoundaryPreview", cfg: Parsed{SnippetBoundaryPreview: true}},
		{name: "RecentPreview", cfg: Parsed{RecentPreview: true}},
		{name: "LinesPreview", cfg: Parsed{LinesPreview: true}},
		{name: "PrediscoveredPath", cfg: Parsed{PrediscoveredPath: "/tmp/ck.json"}},
		{name: "TreeInputDir", cfg: Parsed{TreeInputDir: "/tmp"}},
		{name: "SinkTogglePath", cfg: Parsed{SinkTogglePath: "/tmp/toggle"}},
		{name: "SinkPreviewModePath", cfg: Parsed{SinkPreviewModePath: "/tmp/mode"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.cfg.IsInternalKind() {
				t.Fatalf("IsInternalKind() = false; expected true when %s is set", tc.name)
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
