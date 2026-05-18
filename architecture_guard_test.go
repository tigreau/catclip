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
