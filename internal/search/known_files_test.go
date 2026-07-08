package search

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// The two extension lists must be disjoint: a name in both is a build
// bug, not a runtime preference.
func TestKnownFilesListsDoNotOverlap(t *testing.T) {
	for ext := range knownTextExts {
		if _, clash := knownBinaryExts[ext]; clash {
			t.Errorf("extension %q present in both knownTextExts and knownBinaryExts", ext)
		}
	}
}

// Collision dispositions from the plan's "List design" table. Each row is
// a name where a naive list would silently misclassify one variant; the
// disposition below is deliberate. Do not "fix" a row without re-reading
// RESOLVED_PLAN_binary_detection_replacement.md.
func TestKnownFilesCollisionDispositions(t *testing.T) {
	inText := func(ext string) bool { _, ok := knownTextExts[ext]; return ok }
	inBinary := func(ext string) bool { _, ok := knownBinaryExts[ext]; return ok }

	neither := []string{"out", "obj", "stl", "key", "crt", "plist", "strings", "db", "dat", "pak", "pb", "ini", "rc"}
	for _, ext := range neither {
		if inText(ext) {
			t.Errorf(".%s must NOT be in knownTextExts (bimodal; residue decides)", ext)
		}
		if inBinary(ext) {
			t.Errorf(".%s must NOT be in knownBinaryExts (text variants exist; a wrong blocklist entry silently drops files)", ext)
		}
	}

	text := []string{"ts", "mts", "snap", "lock", "pem", "svg"}
	for _, ext := range text {
		if !inText(ext) {
			t.Errorf(".%s must be in knownTextExts (source-tree bias; sink gate backstops)", ext)
		}
	}

	binary := []string{"der", "png", "exe", "gz", "woff2", "sqlite"}
	for _, ext := range binary {
		if !inBinary(ext) {
			t.Errorf(".%s must be in knownBinaryExts", ext)
		}
	}
}

func TestShellStyleExtension(t *testing.T) {
	cases := map[string]string{
		"a.go":                 "go",
		"archive.tar.gz":       "gz",
		"component.d.ts":       "ts",
		"UPPER.PNG":            "png",
		"noext":                "",
		".gitignore":           "",
		"trailingdot.":         "",
		"dir/sub/file.RS":      "rs",
		"dir.with.dots/plain":  "",
		"dir.with.dots/f.yaml": "yaml",
	}
	for in, want := range cases {
		if got := shellStyleExtension(in); got != want {
			t.Errorf("shellStyleExtension(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyPathByName(t *testing.T) {
	cases := map[string]nameClass{
		"src/main.go":        nameClassText,
		"docs/README":        nameClassText, // basename
		"Makefile":           nameClassText, // basename, case-folded
		"BUILD":              nameClassText, // bazel basename
		".hiss":              nameClassText,
		"img/logo.png":       nameClassBinary,
		"dist/app.exe":       nameClassBinary,
		"a.out":              nameClassUnknown, // collision table: never text by name
		"model.obj":          nameClassUnknown,
		"secrets/server.key": nameClassUnknown,
		"data/unknown.xyz":   nameClassUnknown,
		"bin/tool":           nameClassUnknown, // extensionless, not a known basename
	}
	for in, want := range cases {
		if got := classifyPathByName(in); got != want {
			t.Errorf("classifyPathByName(%q) = %d, want %d", in, got, want)
		}
	}
}

// referenceNulScanClassify is the definitional classifier (RULES.md rule
// 11): text ⇔ no NUL in rg's DECODED view. rg BOM-sniffs before matching,
// so BOM'd UTF-16 transcodes and its raw NULs never reach the pattern —
// BOM'd UTF-16 is text by the definition (verified against bin/rg
// 2026-07-04); BOM-less UTF-16 keeps its raw NULs and is binary.
// Implemented independently of rg so the golden test cannot share a bug
// with the code under test.
func referenceNulScanClassify(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	set := map[string]struct{}{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // unreadable → binary, matching rg --no-messages
		}
		if referenceIsBinary(data) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		set[filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func referenceIsBinary(data []byte) bool {
	// UTF-16 BOM → decode; binary ⇔ a decoded U+0000.
	if len(data) >= 2 && ((data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF)) {
		le := data[0] == 0xFF
		for i := 2; i+1 < len(data); i += 2 {
			var r uint16
			if le {
				r = uint16(data[i]) | uint16(data[i+1])<<8
			} else {
				r = uint16(data[i])<<8 | uint16(data[i+1])
			}
			if r == 0 {
				return true
			}
		}
		return false
	}
	return bytes.IndexByte(data, 0) >= 0
}

func resetTextFileSetCache() {
	textFileSetCacheMu.Lock()
	textFileSetCache = map[string]map[string]struct{}{}
	textFileSetCacheMu.Unlock()
}

// TestHybridMatchesFullNulScanGolden pins the rule: the hybrid classifier
// must agree with the definitional full NUL scan on a corpus exercising
// every classification path — known-text names, known-binary names,
// unknown names with and without NULs, empty files of every name class
// (including an empty blocklisted-extension file), and extensionless
// files. The one deliberate divergence (NUL bytes under a known-text
// name) is excluded here and pinned by TestHybridKnownTextNameDivergence.
func TestHybridMatchesFullNulScanGolden(t *testing.T) {
	if _, ok := RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"src/main.go":      []byte("package main\n"),
		"README":           []byte("readme\n"),
		"logo.png":         {0x89, 'P', 'N', 'G', 0x00, 0x01},
		"data/clean.xyz":   []byte("plain text, unknown extension\n"),
		"data/nulled.xyz":  {'h', 'i', 0x00, 'x'},
		"a.out":            {0x7f, 'E', 'L', 'F', 0x00},
		"notes.out":        []byte("text with the collision-table extension\n"),
		"empty.md":         {},
		"empty.png":        {}, // empty blocklisted name: text by the rule
		"empty.xyz":        {},
		"bin/tool":         {0x7f, 'E', 'L', 'F', 0x00, 0x02},
		"scripts/run":      []byte("#!/bin/sh\necho hi\n"),
		"deep/model.obj":   []byte("v 1.0 2.0 3.0\nf 1 2 3\n"),
		"deep/keynote.key": {'P', 'K', 0x03, 0x04, 0x00},
		// BOM'd UTF-16 is TEXT by the definition (rg transcodes before the
		// NUL pattern runs); BOM-less UTF-16 keeps raw NULs and is binary.
		// .ini is deliberately residue (see known_files.go), so both go
		// through the definitional scan and must agree with the reference.
		"win/bom.ini":   {0xFF, 0xFE, '[', 0x00, 'S', 0x00, ']', 0x00},
		"win/nobom.ini": {'[', 0x00, 'S', 0x00, ']', 0x00},
		"win/plain.ini": []byte("[section]\nkey=1\n"),
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resetTextFileSetCache()
	got, err := ResolveTextFileSet(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := referenceNulScanClassify(t, dir)

	sorted := func(m map[string]struct{}) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	gotList, wantList := sorted(got), sorted(want)
	if len(gotList) != len(wantList) {
		t.Fatalf("hybrid disagrees with full NUL scan\nhybrid: %v\nfull:   %v", gotList, wantList)
	}
	for i := range gotList {
		if gotList[i] != wantList[i] {
			t.Fatalf("hybrid disagrees with full NUL scan\nhybrid: %v\nfull:   %v", gotList, wantList)
		}
	}
}

// TestHybridKnownTextNameDivergence pins the ONE accepted divergence from
// the definition: NUL-bearing bytes under a known-text extension classify
// text without being read (the allowlist is trusted; the plan's sink-gate
// decision is the backstop). If this test starts failing because the file
// classifies binary, the allowlist semantics changed — update the plan
// before updating the test.
func TestHybridKnownTextNameDivergence(t *testing.T) {
	if _, ok := RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "misnamed.md"), []byte{'x', 0x00, 'y'}, 0o644); err != nil {
		t.Fatal(err)
	}
	resetTextFileSetCache()
	set, err := ResolveTextFileSet(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["misnamed.md"]; !ok {
		t.Fatal("expected NUL-bearing misnamed.md to classify text by name (accepted divergence); it classified binary")
	}
}

// TestHybridResidueRecording pins the verbose feedback loop: residue
// paths (name-undecidable) are recorded with their text-classification
// count for TextClassificationResidue consumers.
func TestHybridResidueRecording(t *testing.T) {
	if _, ok := RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clean.xyz"), []byte("text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nulled.xyz"), []byte{0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	resetTextFileSetCache()
	textResidueMu.Lock()
	textResiduePaths, textResidueSeen, textResidueText = nil, nil, 0
	textResidueMu.Unlock()

	if _, err := ResolveTextFileSet(dir, nil); err != nil {
		t.Fatal(err)
	}
	paths, textCount := TextClassificationResidue()
	if len(paths) != 2 {
		t.Fatalf("expected 2 residue paths, got %v", paths)
	}
	if textCount != 1 {
		t.Fatalf("expected 1 residue file classified text, got %d", textCount)
	}
}

// TestHybridToleratesUnreadableResidueFile pins the residue scan's exit-2
// tolerance: rg exits 2 when an explicitly listed file cannot be opened
// (even under --no-messages) while still printing the rows it could
// classify. The scan must parse the partial output instead of failing the
// whole run — the unreadable file classifies binary ("cannot prove
// NUL-free"), everything else classifies normally. Live failure pinned:
// 2026-07-04, one unreadable Desktop file killed the entire run with a
// bare "exit status 2".
func TestHybridToleratesUnreadableResidueFile(t *testing.T) {
	if _, ok := RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 does not block reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clean.xyz"), []byte("plain residue text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locked.xyz"), []byte("never read\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "locked.xyz"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(dir, "locked.xyz"), 0o644)

	resetTextFileSetCache()
	set, err := ResolveTextFileSet(dir, nil)
	if err != nil {
		t.Fatalf("expected tolerance for unreadable residue file, got error: %v", err)
	}
	if _, ok := set["clean.xyz"]; !ok {
		t.Fatalf("readable residue file must classify text, got %v", set)
	}
	if _, ok := set["locked.xyz"]; ok {
		t.Fatalf("unreadable residue file must classify binary (absent), got %v", set)
	}
}
