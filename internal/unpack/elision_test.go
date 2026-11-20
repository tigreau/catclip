package unpack

import (
	"path/filepath"
	"testing"
)

func TestPlaceholderLines(t *testing.T) {
	for _, line := range []string{
		"// ... rest unchanged", "\t// ...", "    # ... existing code ...", "/* ... rest of the function ... */",
		"<!-- ... other items ... -->", "{/* ... */}", "...", "…", "// … same as before", "-- ... remaining columns",
	} {
		if !isPlaceholderLine(line) {
			t.Errorf("should be a placeholder: %q", line)
		}
	}
	for _, line := range []string{
		"func f(args ...string) {", "return [...items, x]", "\tf(xs...)", "// wait...", "// Loading... please wait",
		"// TODO: handle errors", "fmt.Println(\"...\")", "// see https://example.com/...",
	} {
		if isPlaceholderLine(line) {
			t.Errorf("should be code: %q", line)
		}
	}
}

// A shortened entry blocks: written as received, the placeholder would delete
// the code it stands for. A placeholder already in the local file is kept.
func TestBuildPlanBlocksShortenedEntries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n\nfunc A() {\n\tone()\n\ttwo()\n}\n")
	mustWrite(t, filepath.Join(root, "b.go"), "package b\n\n// ...\nfunc B() {}\n")
	b, err := Parse([]byte("<file path=\"a.go\" lines=\"3-6\" out-of=\"6\">\nfunc A() {\n\tone()\n\t// ... rest unchanged\n}\n</file>\n" +
		"<file path=\"b.go\">\npackage b\n\n// ...\nfunc B() { return }\n</file>\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(root, b)
	if err != nil {
		t.Fatal(err)
	}
	blocked := p.Blocked()
	if len(blocked) != 1 || blocked[0].Path != "a.go" || blocked[0].Problem != ProblemShortened || blocked[0].Detail != `"// ... rest unchanged"` {
		t.Fatalf("blocked %+v", blocked)
	}
	if len(p.Changes) != 1 || p.Changes[0].Path != "b.go" {
		t.Fatalf("b.go keeps its own // ... line and is a normal change: %+v", p.Changes)
	}
}
