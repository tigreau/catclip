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

	requireNoTypeDecl(t, files, "runConfig")
	requireNoTypeDecl(t, files, "parsedInvocation")
	requireTypeDecl(t, files, "DiscoveryResult")
	requireTypeDecl(t, files, "DiagnosticSummary")
	requireFuncDecl(t, files, "discoverInvocation")

	requireNoDirectCallInFile(t, files, "cli.go", "evaluateScope")
	requireParsedCommandOnlyAtRootBoundary(t, files)

	// Bundled-rg lookup is the rg boundary: only ripgrep.go (which defines
	// the function and wraps every rg invocation in it) and bundled_tools.go
	// (the startup tool-availability check) may call ripgrepBinary directly.
	// Any new caller is a rg leak — bypasses the wrappers, escapes future
	// migration to internal/search/, and breaks the v0.5.3 package-extraction
	// boundary before it lands. Add the file to the allowlist only if it
	// genuinely needs the bare binary path (not the wrapped operations).
	requireCallOnlyInAllowedFiles(t, files, "ripgrepBinary",
		[]string{"ripgrep.go", "bundled_tools.go"})
	requireInteractivePickersAvoidPersistentSideEffects(t, files)
	requireInternalRenderHandlersAvoidDerivation(t, files)
	requirePreviewPlaceholdersStayStandalone(t, files)
}

// previewCommandBuilders are the functions that emit fzf preview/reload command
// strings forwarded to the shell (sh on POSIX, `cmd /s /c` on Windows). Every
// fzf placeholder ({2}, {+2}, {q}, …) in their output must be a standalone,
// whitespace-delimited token — never concatenated into a compound string —
// because cmd.exe and POSIX sh quote embedded substitutions differently.
// Compound paths must be assembled in Go on the receiving side (e.g.
// catclip-tree's --input-dir/--input-stem). This bug class has regressed and
// been re-fixed four times; see
// docs/versions/v0.5.0/reports/RESOLVED_BUG_windows_preview_posix_shell.md and
// RULES.md Rule 18.
//
// SCOPE LIMIT: this guard enforces only the *standalone-token* half of the
// rule (no concatenation). It does NOT and cannot enforce the *trivial-value*
// half — that the value fzf substitutes is a bare number/key with no spaces or
// shell metacharacters — because that depends on runtime row data. A green
// result means "placeholder not concatenated," not "safe to push any value
// through fzf." Keep substituted values trivial and quote hazardous paths
// (absolute temp paths, etc.) in Go via shellQuoteArg as fixed args. This is
// why `--input-file {N}` with a full-path column is unsafe even though it would
// pass this guard.
var previewCommandBuilders = map[string]struct{}{
	"fzfFileSetPreviewCommand":                  {},
	"fzfDiffFilePreviewCommand":                 {},
	"fzfPreviewCommand":                         {},
	"fzfContentPreviewCommand":                  {},
	"fzfContentMatchListCommand":                {},
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
var previewBuilderFiles = []string{
	"resolver.go", "startup_picker.go", "depth_picker.go",
	"recent_picker.go", "lines_picker.go", "startup_sink_picker.go",
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
		inBuilderFile := containsString(previewBuilderFiles, parsed.name)
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
				for _, loc := range anyPlaceholder.FindAllStringIndex(val, -1) {
					if !placeholderStandalone(val, loc) {
						t.Errorf("%s: %s embeds fzf placeholder %q in literal %q; fzf placeholders must be standalone whitespace-delimited arguments — assemble compound paths in Go on the receiving side (Rule 18, Windows cmd.exe). See RESOLVED_BUG_windows_preview_posix_shell.md.", parsed.name, name, val[loc[0]:loc[1]], val)
					}
				}
				for _, frag := range []string{"set --", "if [ ", "; then", `"$@"`} {
					if strings.Contains(val, frag) {
						t.Errorf("%s: %s contains POSIX shell fragment %q; preview commands must be straight program pipelines with no shell scripting — cmd.exe cannot run them (Rule 18). Push conditionals into Go-side --internal-* subcommands.", parsed.name, name, frag)
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
// (rg via applyPrediscoveredScopeTail → applyScopeStages; git via
// encodeTreePayloadFromPlan → collectGitStatusMapForPlan) is *transitive*
// and cannot be guarded here while everything is one package — that
// becomes a link-time guarantee at the v0.6.0 catclip-render package
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
			"runInternalPrediscoveredTreePayload",
			"runInternalPrediscoveredTreePreview",
			"runInternalLinesPreview",
			"runInternalPrediscoveredContentMatchList",
			"runInternalFilePreview",
			"runInternalContentCheckpointTreePayload",
			"runInternalRecentPreview",
			"runInternalSinkPreview",
			"runInternalSinkToggle",
		},
		[]string{
			// discovery / scope evaluation
			"evaluateScope", "discoverInvocation", "discoverFilesUnder",
			// git
			"collectGitStatusMapForPlan", "collectGitStatusMapForPathspecs",
			"collectGitStatusOutput", "runGit", "runGitCapture",
			"runGitLines", "runGitNoOutput",
			// ripgrep
			"ripgrepBinary", "ripgrepListUnder", "runRipgrepFiles",
			"runRipgrepMatchLines", "runRipgrepMatches",
			"runRipgrepTextFiles", "runRipgrepVisibleFiles",
		},
	)
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
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == callee {
				t.Errorf("%s calls %s; expected calls only from %v", parsed.name, callee, allowed)
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
		"emitOutputPlan",
		"streamToTextClipboard",
		"emitBufferedToTextClipboard",
		"emitBundle",
		"fileclipCopy",
		"writeClipboardSuccess",
		"runEditHiss",
		"runResetHiss",
		"resolveEditorCommand",
		"ensureGlobalHiss",
		"runGitNoOutput",
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
		if _, ok := fileSet[parsed.name]; !ok {
			continue
		}
		ast.Inspect(parsed.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if ok && ident.Name == callee {
				t.Errorf("%s calls %s; interactive picker frames must stay replayable and side-effect free", parsed.name, callee)
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
				ident, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if _, bad := calleeSet[ident.Name]; bad {
					t.Errorf("%s in %s calls %s; internal preview/render handlers must render precomputed payloads, not derive them (Rule 19)", fn.Name.Name, parsed.name, ident.Name)
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
				if exprUsesIdent(field.Type, "parsedCommand") {
					t.Fatalf("%s in %s takes parsedCommand outside the root adapter boundary", fn.Name.Name, parsed.name)
				}
			}
		}
	}
}

func parsedCommandParamAllowed(funcName string) bool {
	return funcName == "run" || strings.HasSuffix(funcName, "FromParsedCommand")
}

func exprUsesIdent(expr ast.Expr, name string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}
