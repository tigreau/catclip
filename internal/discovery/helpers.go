package discovery

import (
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

// ScopeView is the minimal view of a parsed, resolved, and discovered
// current scope that discovery's resolver needs to produce the fzf
// content-match preview. Used as the POD returned by the callback that
// root (resolved_scope_view.go) registers via SetScopeViewResolver.
//
// Carries just what the picker preview needs: WorkingDir for path
// resolution, GitContext for status, and Entries for the checkpoint.
type ScopeView struct {
	WorkingDir string
	GitContext git.Context
	Entries    []Entry
}

// scopeViewResolverFn is the root-registered callback that derives a
// ScopeView from CLI args. Stays nil if root never calls
// SetScopeViewResolver — the resolver falls back to a non-checkpoint
// content-match command in that case.
var scopeViewResolverFn func(args []string) (ScopeView, bool)

// SetScopeViewResolver wires the args->view derivation that discovery
// can't perform itself (CLI parsing lives in internal/cli; the
// resolvedScopeView wrapper carries render config that's app-side).
// Root's resolved_scope_view.go calls this from Main().
func SetScopeViewResolver(fn func(args []string) (ScopeView, bool)) {
	scopeViewResolverFn = fn
}

// canPromptForChoice duplicates the root main.go free function so the
// resolver can stay independent of root.
func canPromptForChoice(cfg command.Invocation) bool {
	return !cfg.Headless && !cfg.Internal && platform.CanPromptInteractively()
}

// treeTarget* constants are duplicated from internal/render/model.go
// rather than aliased. internal/render is a runtime-removable boundary;
// importing it from discovery would invert the DAG. The values are
// stable string keys used by TargetMatch.State so render-side preview
// UIs find the right rows — sync the values with render/model.go if
// either side changes (no churn expected).
const (
	treeTargetKindDir  = "dir"
	treeTargetKindFile = "file"

	treeTargetStateOK             = "ok"
	treeTargetStateText           = "text"
	treeTargetStateNoTextChildren = "no_text_children"
	treeTargetStateNonText        = "non_text"
)

// startupEscLabel duplicates root startup_undo.go's helper so picker
// headers built by the resolver match the labeling used at root.
func startupEscLabel(hint string) string {
	if hint == "undo" {
		return "[Esc] undo"
	}
	return "[Esc] exit"
}

// normalizeTreeTargetKind / normalizeTreeTargetState mirror
// internal/render/model.go's NormalizeTargetKind / NormalizeTargetState.
// Duplicated rather than imported for the same reason as the constants
// above (no inversion of the discovery -> render direction).
func normalizeTreeTargetKind(kind string) string {
	switch normalizeRelPath(kind) {
	case "all", treeTargetKindDir:
		return treeTargetKindDir
	case treeTargetKindFile:
		return treeTargetKindFile
	default:
		return ""
	}
}

func normalizeTreeTargetState(state string) string {
	switch strings.TrimSpace(state) {
	case treeTargetStateOK, treeTargetStateText, treeTargetStateNoTextChildren, treeTargetStateNonText:
		return state
	default:
		return ""
	}
}

// themedFzfRequest is the discovery-side duplicate of root's
// fzf_theme.go helper. Kept private here because the resolver's fzf
// invocations need themed requests, but discovery shouldn't depend on
// the root catclip package. Sourced palette via platform.ActivePalette.
func themedFzfRequest(req picker.Request) picker.Request {
	if platform.ActivePalette() == (platform.Palette{}) {
		return req
	}
	hasPreview := req.PreviewCommand != "" || req.PreviewWindow != ""
	req.ColorSpecs = fzfColorSpecs(hasPreview)
	return req
}

func fzfColorSpecs(hasPreview bool) []string {
	specs := []string{
		"prompt:6",
		"pointer:6",
		"marker:5",
		"spinner:6",
		"hl:4",
		"hl+:4",
		"info:3",
		"header:8",
		"border:8",
		"label:8",
		"header-border:8",
		"header-label:8",
		"fg+:7",
		"bg+:8",
	}
	if hasPreview {
		specs = append(specs,
			"preview-bg:0",
			"preview-fg:7",
			"preview-border:8",
			"preview-label:6",
		)
	}
	return specs
}
