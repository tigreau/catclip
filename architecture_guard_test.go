package catclip

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestRunPipelineArchitectureGuards(t *testing.T) {
	files := parseProductionGoFiles(t)
	renderFiles := parseRenderGoFiles(t)
	platformFiles := parsePlatformGoFiles(t)
	searchFiles := parseSearchGoFiles(t)
	gitFiles := parseGitGoFiles(t)
	commandFiles := parseCommandGoFiles(t)
	cliFiles := parseCLIGoFiles(t)
	discoveryFiles := parseDiscoveryGoFiles(t)

	requireNoTypeDecl(t, files, "runConfig")
	requireNoTypeDecl(t, files, "parsedInvocation")
	requireTypeDecl(t, discoveryFiles, "Result")
	requireTypeDecl(t, files, "DiagnosticSummary")
	requireFuncDecl(t, discoveryFiles, "DiscoverInvocation")

	requireNoDirectCallInFile(t, files, "cli.go", "discovery.EvaluateScope")
	requireParsedCommandOnlyAtRootBoundary(t, files)

	// Bundled-rg lookup is the rg boundary: after the v0.6.0 search
	// extraction, search.RipgrepBinary is the single entry point.
	// required_tools.go is the only root file that calls it directly
	// (for the startup tool-availability check). New root callers are
	// an rg leak — bypass the wrappers, escape the rg cache, and break
	// the eventual search.Index encapsulation. SelectorExpr-aware match
	// catches calls of the form search.RipgrepBinary().
	requireCallOnlyInAllowedFiles(t, files, "search.RipgrepBinary",
		[]string{"required_tools.go"})

	// fzf execution is confined to interactive_choose.go (the v0.6.6
	// discovery file split), so the resolver core and other discovery files
	// never spawn or reconstruct an fzf picker. picker.Run is the boundary;
	// the fzf command-string builders live in picker_commands.go /
	// interactive_choose.go (audited by name in requirePreviewPlaceholders...).
	requireCallOnlyInAllowedFiles(t, discoveryFiles, "picker.Run",
		[]string{filepath.Join("internal", "discovery", "interactive_choose.go")})

	uiFiles := parseUIGoFiles(t)
	requireInteractivePickersAvoidPersistentSideEffects(t, append(files, uiFiles...))
	requireInternalRenderHandlersAvoidDerivation(t, uiFiles)
	requireMultiFilePreviewHandlersWrapInPreviewCap(t, uiFiles)
	requirePreviewCapWriterStaysInPreviewHandlers(t, append(files, uiFiles...))
	requireRenderPackageAvoidDerivationDeps(t, renderFiles)
	requireDiscoveryPackageAllowedImports(t, discoveryFiles)
	requireOutputPackageAllowedImports(t, parseOutputGoFiles(t))
	requireUIPackageAllowedImports(t, uiFiles)
	requirePlatformPackageAvoidDomainDeps(t, platformFiles)
	requireSearchPackageAvoidDomainDeps(t, searchFiles)
	requireGitPackageAvoidDomainDeps(t, gitFiles)
	requireCommandPackageAvoidDomainDeps(t, commandFiles)
	requireCLIPackageAllowedImports(t, cliFiles)
	requirePreviewPlaceholdersStayStandalone(t, append(files, uiFiles...))
}

// previewCommandBuilders are the functions that emit fzf preview/reload command
// strings forwarded to the shell (sh on POSIX, `cmd /s /c` on Windows). Every
// fzf placeholder ({2}, {+2}, {q}, …) in their output must be a standalone,
// whitespace-delimited token — never concatenated into a compound string —
// because cmd.exe and POSIX sh quote embedded substitutions differently.
// Compound paths must be assembled in Go on the receiving side (e.g. root
// catclip's --input-dir/--input-stem). This bug class has regressed and been
// re-fixed four times; see
// docs/versions/v0.5.0/reports/RESOLVED_BUG_windows_preview_posix_shell.md and
// RULES.md Rule 18.
//
// SCOPE LIMIT: this guard enforces only the *standalone-token* half of the
// rule (no concatenation). It does NOT and cannot enforce the *trivial-value*
// half — that the value fzf substitutes is a bare number/key with no spaces or
// shell metacharacters — because that depends on runtime row data. A green
// result means "placeholder not concatenated," not "safe to push any value
// through fzf." Keep substituted values trivial and quote hazardous paths
// (absolute temp paths, etc.) in Go via discovery.ShellQuoteArg as fixed args. This is
// why `--input-file {N}` with a full-path column is unsafe even though it would
// pass this guard.
var previewCommandBuilders = map[string]struct{}{
	"FzfFileSetPreviewCommand":                  {},
	"FzfDiffFilePreviewCommand":                 {},
	"FzfPreviewCommand":                         {},
	"FzfContentPreviewCommand":                  {},
	"FzfContentSearchingPreviewCommand":         {},
	"FzfContentMatchListCommand":                {},
	"fzfCheckpointContentMatchListCommand":      {},
	"startupFileSetPreviewCommand":              {},
	"buildFileSetCheckpointPreview":             {},
	"startupModifierCurrentScopePreviewCommand": {},
	"buildSnippetBoundaryPreviewForScope":       {},
	"buildDepthPickerPreview":                   {},
	"recentPickerPreviewCommand":                {},
	"buildLinesPickerStartPreviewCommand":       {},
	"buildLinesPickerEndPreviewCommand":         {},
}

// fzfPlaceholderExemptFuncs use fzf placeholders in fzf-native contexts that
// fzf evaluates itself rather than forwarding to the shell — e.g. the
// --preview-window scroll offset `+{N}-/2`. Embedding is safe there.
var fzfPlaceholderExemptFuncs = map[string]struct{}{
	"contentMatchPreviewWindow": {},
}

// previewBuilderFiles hold the fzf command builders. They contain no regex
// literals, so the meta-check may scan them for *any* placeholder form
// (including bare {N}) without confusing a regex quantifier for a placeholder.
// Compared against filepath.Base(parsed.name) so the entries are bare
// basenames regardless of which package the file lives in.
var previewBuilderFiles = []string{
	"resolver.go", "startup_picker.go", "depth_picker.go",
	"recent_picker.go", "lines_picker.go", "startup_sink_picker.go",
}

// previewBuilderDiscoveryFunctions are the fzf preview builders that
// live in internal/discovery after the v0.6.0 extraction. They moved
// with the resolver and still need the placeholder-standalone audit.
var previewBuilderDiscoveryFunctions = map[string]struct{}{
	"FzfFileSetPreviewCommand":             {},
	"FzfDiffFilePreviewCommand":            {},
	"FzfPreviewCommand":                    {},
	"FzfContentPreviewCommand":             {},
	"FzfContentSearchingPreviewCommand":    {},
	"FzfContentMatchListCommand":           {},
	"fzfCheckpointContentMatchListCommand": {},
}

func requirePreviewPlaceholdersStayStandalone(t *testing.T, files []parsedGoFile) {
	t.Helper()
	// All fzf placeholder forms used in this codebase.
	anyPlaceholder := regexp.MustCompile(`\{\+?\d+}|\{q}`)
	// Forms that can never be a regex quantifier, so they are safe to flag in
	// any file (used by the cross-file meta-check).
	unambiguous := regexp.MustCompile(`\{q}|\{\+\d+}`)

	seen := make(map[string]bool, len(previewCommandBuilders))
	for _, parsed := range files {
		inBuilderFile := containsString(previewBuilderFiles, filepath.Base(parsed.name))
		for _, decl := range parsed.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			_, guarded := previewCommandBuilders[name]
			_, exempt := fzfPlaceholderExemptFuncs[name]
			if guarded {
				seen[name] = true
			}

			usesAny, usesUnambiguous := false, false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if unambiguous.MatchString(val) {
					usesUnambiguous = true
				}
				if anyPlaceholder.MatchString(val) {
					usesAny = true
				}
				if !guarded {
					return true
				}
				for _, frag := range []string{"catclip-tree", "CATCLIP_TREE", "--internal-tree-payload", "|"} {
					if strings.Contains(val, frag) {
						t.Errorf("%s: %s contains retired renderer subprocess fragment %q; fzf preview builders must call root catclip --internal-* render handlers directly.", parsed.name, name, frag)
					}
				}
				for _, loc := range anyPlaceholder.FindAllStringIndex(val, -1) {
					if !placeholderStandalone(val, loc) {
						t.Errorf("%s: %s embeds fzf placeholder %q in literal %q; fzf placeholders must be standalone whitespace-delimited arguments — assemble compound paths in Go on the receiving side (Rule 18, Windows cmd.exe). See RESOLVED_BUG_windows_preview_posix_shell.md.", parsed.name, name, val[loc[0]:loc[1]], val)
					}
				}
				for _, frag := range []string{"set --", "if [ ", "; then", `"$@"`} {
					if strings.Contains(val, frag) {
						t.Errorf("%s: %s contains POSIX shell fragment %q; preview commands must be straight program invocations with no shell scripting — cmd.exe cannot run them (Rule 18). Push conditionals into Go-side --internal-* subcommands.", parsed.name, name, frag)
					}
				}
				return true
			})

			if guarded || exempt {
				continue
			}
			// An unclassified function that builds an fzf placeholder must be
			// triaged so it cannot quietly escape the standalone guard.
			if usesUnambiguous || (inBuilderFile && usesAny) {
				t.Errorf("%s: %s uses an fzf placeholder but is not classified for the Windows-safety guard. If it builds a shell-forwarded preview/reload command, add it to previewCommandBuilders; if the placeholder is fzf-native (e.g. a --preview-window offset), add it to fzfPlaceholderExemptFuncs.", parsed.name, name)
			}
		}
	}
	for name := range previewCommandBuilders {
		if _, inDiscovery := previewBuilderDiscoveryFunctions[name]; inDiscovery {
			continue
		}
		if !seen[name] {
			t.Errorf("guarded preview builder %s not found; update previewCommandBuilders (renamed or removed?)", name)
		}
	}
}

// placeholderStandalone reports whether the placeholder at loc within s has
// whitespace (or a string boundary) on both sides — i.e. it is its own token.
func placeholderStandalone(s string, loc []int) bool {
	if loc[0] > 0 {
		if c := s[loc[0]-1]; c != ' ' && c != '\t' {
			return false
		}
	}
	if loc[1] < len(s) {
		if c := s[loc[1]]; c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}

// requireInternalRenderHandlersAvoidDerivation enforces the internal
// picker entry-point contract: a per-refresh preview/render handler must
// render an already-built checkpoint payload, never derive one. It may not
// run discovery, scope evaluation, git, or rg, because fzf re-runs preview
// commands on every cursor move (Rule 19: discovery is the parent's job,
// once). See docs/versions/v0.5.5/.../ACTIVE_PLAN_internal_picker_entrypoint_contract.md.
//
// This catches *direct* calls only. The handlers' remaining heavy coupling
// (rg via discovery.ApplyPrediscoveredScopeTail → applyScopeStages; git via
// ui.RenderTreePreviewFromPlan → output.BuildReportForPlan) is *transitive*
// and cannot be guarded here while everything is one package — that
// becomes a link-time guarantee at the v0.6.0 internal/render package
// boundary. Freezing the direct surface now stops a handler from gaining a
// brand-new direct git/rg/discovery call before then.
//
// Picker DRIVERS (which run once at picker open in the parent) and the
// content-match `change:reload:` search path legitimately discover, so they
// are deliberately absent from this list — only the render handlers are
// guarded.
func requireInternalRenderHandlersAvoidDerivation(t *testing.T, files []parsedGoFile) {
	t.Helper()
	requireFuncsAvoidCalls(t, files,
		[]string{
			"RunInternalPrediscoveredTreePreview",
			"RunInternalLinesPreview",
			"RunInternalPrediscoveredContentMatchList",
			"RunInternalFilePreview",
			"RunInternalRecentPreview",
			"RunInternalSinkPreview",
			"RunInternalSinkToggle",
			"RunInternalSnippetBoundaryPreview",
			"RunInternalTreePayloadFilePreview",
		},
		[]string{
			// discovery / scope evaluation (bare — still at root)
			"discovery.EvaluateScope", "discovery.DiscoverInvocation", "discoverFilesUnder",
			// git: pkg-qualified entries are leaf internal/git helpers.
			// Qualified matching keeps generic method names (.Lines /
			// .Capture / .NoOutput on unrelated types) from
			// false-positiving the guard.
			"git.Capture", "git.NoOutput", "git.Lines",
			"git.DiffAgainstHeadOrIndex", "git.StatusMapForPathspecs",
			// ripgrep: same pattern as git — qualified to internal/search so
			// only the leaf entry points trip the guard.
			"search.RipgrepBinary", "search.RunRipgrepFiles",
			"search.RunRipgrepMatchLines", "search.RunRipgrepMatches",
			"search.ResolveTextFileSet", "search.ResolveVisibleFileSet",
			"search.FirstMatchLinePerFile", "search.HasScopedIgnoredTargetsStreaming",
		},
	)
}

// requireMultiFilePreviewHandlersWrapInPreviewCap enforces that every
// preview handler that streams multi-file body bytes wraps its writer
// in output.PreviewCapWriter. Without the cap, fzf preview pipes can
// be fed multi-MiB payloads per focus on large corpora — Defender
// scales linearly with file count on Windows, and the user only sees
// a screenful. With the cap, the per-focus cost is bounded by
// PreviewByteLimit (128 KiB). The plan that introduced this guard is
// docs/versions/v0.6.2/reports/ACTIVE_PLAN_multi_file_preview_cap.md.
//
// The guard scans each named function's body for a direct call to
// output.NewPreviewCapWriter. Each handler must still exist — a
// silent rename cannot retire the rule.
func requireMultiFilePreviewHandlersWrapInPreviewCap(t *testing.T, files []parsedGoFile) {
	t.Helper()
	required := []string{
		"RunInternalLinesPreview",
		"RunInternalSnippetBoundaryPreview",
		"renderSinkOutputTextPreview",
		"renderSinkTreeReportPreview",
	}
	requireFuncsCall(t, files, required, "output.NewPreviewCapWriter")
}

// previewCapAllowedFiles lists the source files that may construct an
// output.PreviewCapWriter. Anything outside this set is a leak — the
// cap MUST NOT be applied to the final emit (clipboard / bundle /
// stdout); doing so would silently truncate the bytes the user
// pastes. Confirmed 2026-06-21 by tracing the call graph: the three
// handler call sites (cli.go:88, :112, :125) are the only paths into
// preview rendering, and they live in these three files. Any future
// refactor that puts a NewPreviewCapWriter call outside this list is
// almost certainly capping a path that must emit the full payload.
var previewCapAllowedFiles = []string{
	"internal_prediscovered.go",
	"snippet_boundary_lazy.go",
	"startup_sink_picker.go",
}

func requirePreviewCapWriterStaysInPreviewHandlers(t *testing.T, files []parsedGoFile) {
	t.Helper()
	allowed := make(map[string]struct{}, len(previewCapAllowedFiles))
	for _, name := range previewCapAllowedFiles {
		allowed[name] = struct{}{}
	}
	for _, parsed := range files {
		if _, ok := allowed[filepath.Base(parsed.name)]; ok {
			continue
		}
		ast.Inspect(parsed.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkgIdent.Name == "output" && sel.Sel.Name == "NewPreviewCapWriter" {
				t.Errorf("%s calls output.NewPreviewCapWriter; the cap belongs only in preview handlers (%v) — capping a final-emit path would silently truncate the bytes the user pastes", parsed.name, previewCapAllowedFiles)
			}
			return true
		})
	}
}

// requireFuncsCall asserts that each named function body contains a
// direct call to the given callee. Mirrors requireFuncsAvoidCalls but
// inverts the polarity: a guard for "this MUST be present" instead of
// "this MUST be absent." Used to lock in cross-cutting requirements
// (e.g. every multi-file preview handler must wrap its writer in
// output.PreviewCapWriter).
func requireFuncsCall(t *testing.T, files []parsedGoFile, funcNames []string, callee string) {
	t.Helper()
	funcSet := make(map[string]struct{}, len(funcNames))
	for _, name := range funcNames {
		funcSet[name] = struct{}{}
	}
	found := make(map[string]bool, len(funcNames))
	hasCall := make(map[string]bool, len(funcNames))
	pkgName, selName := splitCallee(callee)
	for _, parsed := range files {
		for _, decl := range parsed.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, ok := funcSet[fn.Name.Name]; !ok {
				continue
			}
			found[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if pkgName == "" && fun.Name == selName {
						hasCall[fn.Name.Name] = true
					}
				case *ast.SelectorExpr:
					if fun.Sel == nil {
						return true
					}
					if pkgIdent, ok := fun.X.(*ast.Ident); ok {
						if pkgIdent.Name == pkgName && fun.Sel.Name == selName {
							hasCall[fn.Name.Name] = true
						}
					}
				}
				return true
			})
		}
	}
	for _, name := range funcNames {
		if !found[name] {
			t.Errorf("required handler %s not found; update the cap-wrapping guard (renamed or removed?)", name)
			continue
		}
		if !hasCall[name] {
			t.Errorf("%s does not call %s; multi-file body previews must wrap their writer in output.PreviewCapWriter (see docs/versions/v0.6.2/reports/ACTIVE_PLAN_multi_file_preview_cap.md)", name, callee)
		}
	}
}

func splitCallee(callee string) (pkg, sel string) {
	dot := strings.IndexByte(callee, '.')
	if dot < 0 {
		return "", callee
	}
	return callee[:dot], callee[dot+1:]
}

// requireRenderPackageAvoidDerivationDeps enforces the renderer package
// boundary: internal/render renders already-derived documents only. It must not
// import filesystem discovery, git/rg wrappers, or tool-spawning dependencies.
func requireRenderPackageAvoidDerivationDeps(t *testing.T, files []parsedGoFile) {
	t.Helper()
	forbiddenImports := map[string]string{
		"os":            "filesystem access",
		"os/exec":       "tool spawning",
		"path/filepath": "filesystem path derivation",
	}
	forbiddenIdents := map[string]string{
		// Bare-name forbidden idents: derivation helpers that still live at
		// root. After v0.6.0 the rg/git wrappers moved into internal/search
		// and internal/git, so the package-level import guard
		// (requireRenderPackageAvoidDerivationDeps below) is the real
		// enforcement — but the bare names that remain at root still need
		// listing here so a new render-package file can't accidentally call
		// them either.
		"discovery.EvaluateScope":      "scope evaluation",
		"discovery.DiscoverInvocation": "discovery",
		"discoverFilesUnder":           "discovery",
	}

	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if reason, bad := forbiddenImports[importPath]; bad {
				t.Errorf("%s imports %s (%s); internal/render must stay renderer-only", parsed.name, importPath, reason)
			}
			if strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				t.Errorf("%s imports %s; internal/render must not depend on catclip discovery/root packages", parsed.name, importPath)
			}
		}

		ast.Inspect(parsed.file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			if reason, bad := forbiddenIdents[ident.Name]; bad {
				t.Errorf("%s references %s (%s); internal/render must render precomputed documents only", parsed.name, ident.Name, reason)
			}
			return true
		})
	}
}

type parsedGoFile struct {
	name string
	file *ast.File
}

func parseProductionGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := make([]parsedGoFile, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files = append(files, parsedGoFile{name: path, file: file})
	}
	return files
}

// requirePlatformPackageAvoidDomainDeps enforces the platform package
// boundary: internal/platform is a leaf-utility package that owns OS / TTY /
// bundled-tool / editor / palette helpers. It may import only stdlib (and
// platform's own types). It must not import any tigreau/catclip domain
// package — discovery, output, render, picker, search, etc. Domain types
// crossing into platform is the real trip wire for the bottom-of-stack
// extraction; export count is not.
func requirePlatformPackageAvoidDomainDeps(t *testing.T, files []parsedGoFile) {
	t.Helper()
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				t.Errorf("%s imports %s; internal/platform must stay a leaf — no catclip domain dependencies", parsed.name, importPath)
			}
		}
	}
}

// requireCLIPackageAllowedImports enforces the cli package boundary:
// internal/cli is the parser / help / flag / validation layer, sitting
// just above the leaf model packages. Per the v0.6.0 DAG it may import
// stdlib + internal/command + internal/platform only. No git, search,
// discovery, output, preview, render, picker — and obviously no root
// catclip imports.
func requireCLIPackageAllowedImports(t *testing.T, files []parsedGoFile) {
	t.Helper()
	allowed := map[string]struct{}{
		"github.com/tigreau/catclip/internal/command":  {},
		"github.com/tigreau/catclip/internal/platform": {},
	}
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if !strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				continue
			}
			if _, ok := allowed[importPath]; !ok {
				t.Errorf("%s imports %s; internal/cli may import only stdlib + internal/command + internal/platform", parsed.name, importPath)
			}
		}
	}
}

// requireDiscoveryPackageAllowedImports enforces the discovery package
// boundary: internal/discovery owns the resolver, scope stages, ignore
// handling, and the checkpoint format. Per the v0.6.0 DAG it may import
// stdlib + internal/command + internal/git + internal/picker +
// internal/platform + internal/search only. No cli (parser is upstream;
// dup HasGlobChars locally instead — see discovery.go), no render
// (runtime-removable boundary; dup constants instead — see helpers.go),
// no output/preview, no root catclip.
func requireDiscoveryPackageAllowedImports(t *testing.T, files []parsedGoFile) {
	t.Helper()
	allowed := map[string]struct{}{
		"github.com/tigreau/catclip/internal/command":  {},
		"github.com/tigreau/catclip/internal/git":      {},
		"github.com/tigreau/catclip/internal/picker":   {},
		"github.com/tigreau/catclip/internal/platform": {},
		"github.com/tigreau/catclip/internal/search":   {},
	}
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if !strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				continue
			}
			if _, ok := allowed[importPath]; !ok {
				t.Errorf("%s imports %s; internal/discovery may import only stdlib + internal/command + internal/git + internal/picker + internal/platform + internal/search", parsed.name, importPath)
			}
		}
	}
}

// requireOutputPackageAllowedImports enforces the output package
// boundary: internal/output owns plans, prepared units, snippet
// resolution, byte emission, clipboard delivery, and report aggregation.
// Per the v0.6.0 DAG it may import stdlib + internal/command +
// internal/discovery + internal/git + internal/platform +
// internal/search + the sibling fileclip package only. No cli, no
// render, no preview, no root catclip — render in particular is the
// runtime-removable boundary the output extraction is meant to keep
// inverted (render consumes output's Plan/Report, not the other way
// around).
func requireOutputPackageAllowedImports(t *testing.T, files []parsedGoFile) {
	t.Helper()
	allowed := map[string]struct{}{
		"github.com/tigreau/catclip/internal/command":   {},
		"github.com/tigreau/catclip/internal/discovery": {},
		"github.com/tigreau/catclip/internal/git":       {},
		"github.com/tigreau/catclip/internal/platform":  {},
		"github.com/tigreau/catclip/internal/search":    {},
		"github.com/tigreau/catclip/fileclip":           {},
	}
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if !strings.HasPrefix(importPath, "github.com/tigreau/catclip") {
				continue
			}
			if _, ok := allowed[importPath]; !ok {
				t.Errorf("%s imports %s; internal/output may import only stdlib + internal/command + internal/discovery + internal/git + internal/platform + internal/search + sibling fileclip", parsed.name, importPath)
			}
		}
	}
}

// requireUIPackageAllowedImports enforces the UI package boundary:
// internal/ui owns pickers, render-driving bridges, snippet preview
// helpers, and the internal subcommand runners. Per the v0.6.0 DAG it
// may import stdlib + internal/cli + internal/command + internal/discovery +
// internal/git + internal/output + internal/picker + internal/platform +
// internal/render + internal/search. It must NOT import fileclip
// (clipboard writes live in output.EmitOutputPlan / EmitBundle) and
// must NOT import the root catclip package.
func requireUIPackageAllowedImports(t *testing.T, files []parsedGoFile) {
	t.Helper()
	allowed := map[string]struct{}{
		"github.com/tigreau/catclip/internal/cli":       {},
		"github.com/tigreau/catclip/internal/command":   {},
		"github.com/tigreau/catclip/internal/discovery": {},
		"github.com/tigreau/catclip/internal/git":       {},
		"github.com/tigreau/catclip/internal/output":    {},
		"github.com/tigreau/catclip/internal/picker":    {},
		"github.com/tigreau/catclip/internal/platform":  {},
		"github.com/tigreau/catclip/internal/render":    {},
		"github.com/tigreau/catclip/internal/search":    {},
	}
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if !strings.HasPrefix(importPath, "github.com/tigreau/catclip") {
				continue
			}
			if _, ok := allowed[importPath]; !ok {
				t.Errorf("%s imports %s; internal/ui may import only stdlib + internal/{cli,command,discovery,git,output,picker,platform,render,search} (no fileclip, no root catclip)", parsed.name, importPath)
			}
		}
	}
}

// requireCommandPackageAvoidDomainDeps enforces the strictest leaf
// boundary in the codebase: internal/command holds the typed command
// model and must import only stdlib. No git / platform / search / picker /
// render — let alone any catclip domain package. Sharing a POD model
// across the rest of the pipeline depends on this boundary staying clean,
// or the model becomes coupled to runtime concerns it has no business
// knowing about.
func requireCommandPackageAvoidDomainDeps(t *testing.T, files []parsedGoFile) {
	t.Helper()
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				t.Errorf("%s imports %s; internal/command must stay a leaf — stdlib only, no catclip imports", parsed.name, importPath)
			}
		}
	}
}

// requireGitPackageAvoidDomainDeps enforces the git package boundary:
// internal/git is a leaf wrapper around the git subprocess and must not
// import any github.com/tigreau/catclip/* package. The Context POD is its
// own boundary type — domain types (command.ExecutionScope / discovery.Entry / output.Plan)
// stay outside git in discovery.FilterChangedEntries / discovery.CollectChangedRepoPaths
// and in output.BuildReportForPlan.
func requireGitPackageAvoidDomainDeps(t *testing.T, files []parsedGoFile) {
	t.Helper()
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				t.Errorf("%s imports %s; internal/git must stay a leaf — no catclip domain dependencies", parsed.name, importPath)
			}
		}
	}
}

// requireSearchPackageAvoidDomainDeps enforces the search package boundary:
// internal/search wraps rg / fzf-process plumbing and may import only stdlib
// plus internal/platform. Any github.com/tigreau/catclip/* import other than
// internal/platform (root, internal/render, internal/picker, future
// command/discovery/output/preview/git/app) is a leak — pull derived data
// across the boundary and the cache state becomes coupled to those packages,
// breaking the leaf-utility role and the eventual search.Index wrap.
func requireSearchPackageAvoidDomainDeps(t *testing.T, files []parsedGoFile) {
	t.Helper()
	allowed := map[string]struct{}{
		"github.com/tigreau/catclip/internal/platform": {},
	}
	for _, parsed := range files {
		for _, spec := range parsed.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has unparsable import %s: %v", parsed.name, spec.Path.Value, err)
			}
			if !strings.HasPrefix(importPath, "github.com/tigreau/catclip/") {
				continue
			}
			if _, ok := allowed[importPath]; !ok {
				t.Errorf("%s imports %s; internal/search may import only stdlib + internal/platform", parsed.name, importPath)
			}
		}
	}
}

func parseRenderGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "render"), "internal/render")
}

func parsePlatformGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "platform"), "internal/platform")
}

func parseSearchGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "search"), "internal/search")
}

func parseGitGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "git"), "internal/git")
}

func parseCommandGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "command"), "internal/command")
}

func parseCLIGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "cli"), "internal/cli")
}

func parseDiscoveryGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "discovery"), "internal/discovery")
}

func parseUIGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "ui"), "internal/ui")
}

func parseOutputGoFiles(t *testing.T) []parsedGoFile {
	t.Helper()
	return parsePackageGoFiles(t, filepath.Join("internal", "output"), "internal/output")
}

func parsePackageGoFiles(t *testing.T, dir, label string) []parsedGoFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := make([]parsedGoFile, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files = append(files, parsedGoFile{name: path, file: file})
	}
	if len(files) == 0 {
		t.Fatalf("no %s production files parsed", label)
	}
	return files
}

func requireNoTypeDecl(t *testing.T, files []parsedGoFile, name string) {
	t.Helper()
	for _, parsed := range files {
		for _, decl := range parsed.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				if typeSpec.Name.Name == name {
					t.Fatalf("stale type %s declared in %s", name, parsed.name)
				}
			}
		}
	}
}

func requireTypeDecl(t *testing.T, files []parsedGoFile, name string) {
	t.Helper()
	for _, parsed := range files {
		for _, decl := range parsed.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				if typeSpec.Name.Name == name {
					return
				}
			}
		}
	}
	t.Fatalf("type %s is not declared", name)
}

func requireFuncDecl(t *testing.T, files []parsedGoFile, name string) {
	t.Helper()
	for _, parsed := range files {
		for _, decl := range parsed.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == name {
				return
			}
		}
	}
	t.Fatalf("function %s is not declared", name)
}

func requireNoDirectCallInFile(t *testing.T, files []parsedGoFile, filename, callee string) {
	t.Helper()
	for _, parsed := range files {
		if parsed.name != filename {
			continue
		}
		ast.Inspect(parsed.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if ok && ident.Name == callee {
				t.Fatalf("%s calls %s directly; route through the discovery boundary", filename, callee)
			}
			return true
		})
		return
	}
	t.Fatalf("file %s was not parsed", filename)
}

// requireCallOnlyInAllowedFiles asserts that every direct call to callee
// across the production files happens inside one of the allowed filenames.
// "Direct call" means the function-name identifier appears as the callee
// of a call expression — calling it via a struct method or via a value of
// the same name does not trip the check (those aren't bare function
// references).
func requireCallOnlyInAllowedFiles(t *testing.T, files []parsedGoFile, callee string, allowed []string) {
	t.Helper()
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for _, parsed := range files {
		if _, ok := allowedSet[parsed.name]; ok {
			continue
		}
		ast.Inspect(parsed.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				// Bare ident form matches only bare callee entries.
				if !strings.Contains(callee, ".") && fun.Name == callee {
					t.Errorf("%s calls %s; expected calls only from %v", parsed.name, callee, allowed)
				}
			case *ast.SelectorExpr:
				if fun.Sel == nil {
					return true
				}
				// pkg.Name callee entries: match strictly on pkgIdent.Name + "." + Sel.Name.
				// Bare callee entries fall back to Sel.Name only, but this is rare
				// (most leaf moves now go through the qualified form).
				if pkgIdent, ok := fun.X.(*ast.Ident); ok {
					qualified := pkgIdent.Name + "." + fun.Sel.Name
					if qualified == callee {
						t.Errorf("%s calls %s; expected calls only from %v", parsed.name, callee, allowed)
					}
				}
				if !strings.Contains(callee, ".") && fun.Sel.Name == callee {
					t.Errorf("%s calls %s; expected calls only from %v", parsed.name, callee, allowed)
				}
			}
			return true
		})
	}
}

func requireInteractivePickersAvoidPersistentSideEffects(t *testing.T, files []parsedGoFile) {
	t.Helper()
	pickerFiles := []string{
		"depth_picker.go",
		"lines_picker.go",
		"recent_picker.go",
		"startup_picker.go",
		"startup_sink_picker.go",
		"startup_undo.go",
	}
	// Undo depends on picker frames being replayable. Temp checkpoint writes
	// and emit-shaped previews are allowed, but final sinks, editor launch,
	// user-file mutation, and git mutation do not belong in picker files.
	for _, callee := range []string{
		"output.EmitOutputPlan",
		"streamToTextClipboard",
		"emitBufferedToTextClipboard",
		"emitBundle",
		"fileclipCopy",
		"ui.WriteClipboardSuccess",
		"runEditHiss",
		"runResetHiss",
		"ResolveEditorCommand",
		"discovery.EnsureGlobalHiss",
		"git.NoOutput",
	} {
		requireNoDirectCallsInFiles(t, files, pickerFiles, callee)
	}
}

func requireNoDirectCallsInFiles(t *testing.T, files []parsedGoFile, filenames []string, callee string) {
	t.Helper()
	fileSet := make(map[string]struct{}, len(filenames))
	for _, name := range filenames {
		fileSet[name] = struct{}{}
	}
	for _, parsed := range files {
		// Match by basename so the filenames list can stay bare
		// regardless of which package the file moved into.
		if _, ok := fileSet[filepath.Base(parsed.name)]; !ok {
			continue
		}
		ast.Inspect(parsed.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				// Bare ident form matches only bare callee entries.
				if !strings.Contains(callee, ".") && fun.Name == callee {
					t.Errorf("%s calls %s; interactive picker frames must stay replayable and side-effect free", parsed.name, callee)
				}
			case *ast.SelectorExpr:
				if fun.Sel == nil {
					return true
				}
				// pkg.Name callee entries: strict pkgIdent.Name + "." + Sel.Name match.
				// Prevents generic method names (.NoOutput, .Capture on unrelated
				// types) from false-positiving when the intent is "no calls into
				// the leaf git package."
				if pkgIdent, ok := fun.X.(*ast.Ident); ok {
					qualified := pkgIdent.Name + "." + fun.Sel.Name
					if qualified == callee {
						t.Errorf("%s calls %s; interactive picker frames must stay replayable and side-effect free", parsed.name, callee)
					}
				}
				if !strings.Contains(callee, ".") && fun.Sel.Name == callee {
					t.Errorf("%s calls %s; interactive picker frames must stay replayable and side-effect free", parsed.name, callee)
				}
			}
			return true
		})
	}
}

// requireFuncsAvoidCalls asserts that none of the named functions directly
// call any of the named callees. Direct-call granularity matches the other
// guards in this file; the guarded handlers are small and delegate only to
// pure checkpoint helpers, so a direct-call check catches a regression that
// reintroduces discovery into a preview handler. Each named function must
// exist, so a rename cannot silently retire the guard.
func requireFuncsAvoidCalls(t *testing.T, files []parsedGoFile, funcNames, callees []string) {
	t.Helper()
	funcSet := make(map[string]struct{}, len(funcNames))
	for _, name := range funcNames {
		funcSet[name] = struct{}{}
	}
	calleeSet := make(map[string]struct{}, len(callees))
	for _, name := range callees {
		calleeSet[name] = struct{}{}
	}
	seen := make(map[string]bool, len(funcNames))
	for _, parsed := range files {
		for _, decl := range parsed.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, ok := funcSet[fn.Name.Name]; !ok {
				continue
			}
			seen[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					// Bare ident match (e.g. discovery.EvaluateScope, collectGitStatusMapForPlan)
					if _, bad := calleeSet[fun.Name]; bad {
						t.Errorf("%s in %s calls %s; internal preview/render handlers must render precomputed payloads, not derive them (Rule 19)", fn.Name.Name, parsed.name, fun.Name)
					}
				case *ast.SelectorExpr:
					if fun.Sel == nil {
						return true
					}
					// Package-qualified match (e.g. git.Capture, search.RunRipgrepFiles).
					// Only fires when the callee entry has the "pkg.Name" form, so a
					// reused method name like .Lines() on an unrelated type does NOT
					// false-positive.
					if pkgIdent, ok := fun.X.(*ast.Ident); ok {
						qualified := pkgIdent.Name + "." + fun.Sel.Name
						if _, bad := calleeSet[qualified]; bad {
							t.Errorf("%s in %s calls %s; internal preview/render handlers must render precomputed payloads, not derive them (Rule 19)", fn.Name.Name, parsed.name, qualified)
						}
					}
				}
				return true
			})
		}
	}
	for _, name := range funcNames {
		if !seen[name] {
			t.Errorf("guarded handler %s not found; update the entry-point contract guard", name)
		}
	}
}

func requireParsedCommandOnlyAtRootBoundary(t *testing.T, files []parsedGoFile) {
	t.Helper()
	for _, parsed := range files {
		for _, decl := range parsed.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Params == nil {
				continue
			}
			if parsedCommandParamAllowed(fn.Name.Name) {
				continue
			}
			for _, field := range fn.Type.Params.List {
				if exprUsesSelector(field.Type, "command", "Parsed") {
					t.Fatalf("%s in %s takes command.Parsed outside the root adapter boundary", fn.Name.Name, parsed.name)
				}
			}
		}
	}
}

func parsedCommandParamAllowed(funcName string) bool {
	return funcName == "run" || strings.HasSuffix(funcName, "FromParsedCommand")
}

// exprUsesSelector reports whether expr references the qualified selector
// pkgName.selName anywhere inside it (param type, return type, etc.). A
// SelectorExpr like command.Parsed is two distinct *ast.Ident nodes —
// "command" and "Parsed" — never a single ident named "command.Parsed",
// so bare-name matching misses it. This walker matches the SelectorExpr
// shape directly.
//
// Limitation: import aliasing (import foo "...command"; foo.Parsed) is
// not handled — the guard would miss aliased forms. catclip doesn't
// alias the command import today; if that changes, resolve the alias
// from parsed.file.Imports first.
func exprUsesSelector(expr ast.Expr, pkgName, selName string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkgIdent.Name == pkgName && sel.Sel.Name == selName {
			found = true
			return false
		}
		return true
	})
	return found
}

// containsString reports whether the slice contains the target value.
// Lifted alongside the previewPlaceholdersStayStandalone guard after
// the v0.6.0 internal/ui move took the original copy with it.
func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
