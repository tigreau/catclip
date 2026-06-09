package catclip

import (
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

// TestInternalKindPredicateParity holds the two parallel internal-kind
// predicates together: root internalCommandConfig.isInternalKind (used
// by run() dispatch) and command.Parsed.IsInternalKind (used by
// command.InvocationFromParsed). They must agree for every Parsed,
// because runtime prompt suppression flows through both paths today
// and the cli/ parser extraction (future commit) will retire the root
// version in favor of the command-side one.
//
// If a new internal-kind field is added to command.Parsed, update both
// predicates and add a case to the table below.
func TestInternalKindPredicateParity(t *testing.T) {
	cases := []struct {
		name string
		cfg  command.Parsed
	}{
		{name: "plain ActionRun", cfg: command.Parsed{Action: command.ActionRun}},
		{name: "TreePreview", cfg: command.Parsed{TreePreview: true}},
		{name: "FilePreview", cfg: command.Parsed{FilePreview: true}},
		{name: "ContentMatchList", cfg: command.Parsed{ContentMatchList: true}},
		{name: "SnippetBoundaryPreview", cfg: command.Parsed{SnippetBoundaryPreview: true}},
		{name: "RecentPreview", cfg: command.Parsed{RecentPreview: true}},
		{name: "LinesPreview", cfg: command.Parsed{LinesPreview: true}},
		{name: "PrediscoveredPath", cfg: command.Parsed{PrediscoveredPath: "/tmp/ck.json"}},
		{name: "TreeInputDir", cfg: command.Parsed{TreeInputDir: "/tmp"}},
		{name: "SinkTogglePath", cfg: command.Parsed{SinkTogglePath: "/tmp/toggle"}},
		{name: "SinkPreviewModePath", cfg: command.Parsed{SinkPreviewModePath: "/tmp/mode"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fromCommand := tc.cfg.IsInternalKind()
			fromRoot := internalCommandConfigFromParsedCommand(tc.cfg).isInternalKind()
			if fromCommand != fromRoot {
				t.Fatalf("predicate disagreement: command.Parsed.IsInternalKind=%v, internalCommandConfig.isInternalKind=%v\n  Update both predicates in lockstep when adding a new internal-kind field.", fromCommand, fromRoot)
			}
		})
	}
}
