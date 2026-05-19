package catclip

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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
