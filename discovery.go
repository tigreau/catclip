package catclip

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type textClassifier func(relPath, absPath string) (bool, error)

// discoverFilesUnder enumerates text files under rootAbs using ripgrep.
// When rootBypass is set the call switches to --no-ignore so callers acting
// on an --include'd subtree can recover paths blocked by .hiss/.gitignore.
func discoverFilesUnder(workingDir, rootAbs, rootRel string, classifyText textClassifier, rootBypass *blockInfo) ([]fileEntry, error) {
	rootRel = normalizeRelPath(rootRel)
	rels, err := ripgrepListUnder(workingDir, rootRel, rootBypass != nil)
	if err != nil {
		return nil, err
	}
	files := make([]fileEntry, 0, len(rels))
	for _, rel := range rels {
		absPath := filepath.Join(workingDir, filepath.FromSlash(rel))
		text, err := classifyText(rel, absPath)
		if err != nil {
			return nil, err
		}
		if !text {
			continue
		}
		entry := fileEntry{
			AbsPath: absPath,
			RelPath: rel,
		}
		if rootBypass != nil {
			entry = withAllowedByInclude(entry, *rootBypass)
		}
		files = append(files, entry)
	}
	_ = rootAbs
	return files, nil
}

// discoverFilesByBasenameUnder is the basename-filtered variant of
// discoverFilesUnder.
func discoverFilesByBasenameUnder(workingDir, rootAbs, rootRel, baseName string, classifyText textClassifier, rootBypass *blockInfo) ([]fileEntry, error) {
	rootRel = normalizeRelPath(rootRel)
	rels, err := ripgrepListUnder(workingDir, rootRel, rootBypass != nil)
	if err != nil {
		return nil, err
	}
	files := make([]fileEntry, 0)
	for _, rel := range rels {
		if path.Base(rel) != baseName {
			continue
		}
		absPath := filepath.Join(workingDir, filepath.FromSlash(rel))
		text, err := classifyText(rel, absPath)
		if err != nil {
			return nil, err
		}
		if !text {
			continue
		}
		entry := fileEntry{
			AbsPath: absPath,
			RelPath: rel,
		}
		if rootBypass != nil {
			entry = withAllowedByInclude(entry, *rootBypass)
		}
		files = append(files, entry)
	}
	_ = rootAbs
	return files, nil
}

// ripgrepListUnder returns the rg-discovered file list under rootRel.
// When noIgnore is true rg ignores .gitignore/.hiss; otherwise the global
// .hiss is layered onto the default gitignore-aware enumeration.
func ripgrepListUnder(workingDir, rootRel string, noIgnore bool) ([]string, error) {
	opts := ripgrepFileOptions{NoIgnore: noIgnore}
	if !noIgnore {
		hissPath, err := readableHissPath()
		if err != nil {
			return nil, err
		}
		opts.HissPath = hissPath
	}
	if rootRel != "." && rootRel != "" {
		opts.Paths = []string{rootRel}
	}
	rels, err := runRipgrepFiles(workingDir, opts)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." {
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

func knownTextLikeFile(relPath string) bool {
	if _, ok := knownTextBasenames[strings.ToLower(path.Base(relPath))]; ok {
		return true
	}
	_, ok := knownTextExts[shellStyleExtension(relPath)]
	return ok
}

func shellStyleExtension(relPath string) string {
	base := strings.ToLower(path.Base(relPath))
	lastDot := strings.LastIndexByte(base, '.')
	if lastDot <= 0 || lastDot == len(base)-1 {
		return ""
	}
	return base[lastDot+1:]
}

var knownTextBasenames = map[string]struct{}{
	"makefile": {}, "gemfile": {}, "rakefile": {}, "guardfile": {}, "vagrantfile": {}, "berksfile": {}, "capfile": {},
	"dockerfile": {}, "containerfile": {}, "jenkinsfile": {}, "procfile": {},
	"cmakelists.txt": {}, "configure": {}, "configure.ac": {},
	".gitignore": {}, ".gitattributes": {}, ".gitmodules": {}, ".gitkeep": {}, ".keep": {},
	".git-blame-ignore-revs": {},
	".dockerignore":          {}, ".helmignore": {}, ".slugignore": {},
	".vscodeignore": {}, ".npmignore": {}, ".eslintignore": {},
	".editorconfig": {}, ".eslintrc": {}, ".prettierrc": {}, ".stylelintrc": {},
	".babelrc": {}, ".npmrc": {}, ".yarnrc": {}, ".nvmrc": {}, ".browserslistrc": {},
	".flake8": {}, ".pylintrc": {}, ".rubocop.yml": {},
	".htaccess": {}, ".mailmap": {}, ".sequelizerc": {},
	"license": {}, "licence": {}, "authors": {}, "contributors": {}, "changelog": {}, "todo": {},
	"codeowners": {}, "version": {}, "readme": {},
}

var knownTextExts = map[string]struct{}{
	"html": {}, "htm": {}, "css": {}, "scss": {}, "sass": {}, "less": {}, "js": {}, "jsx": {}, "mjs": {}, "cjs": {}, "ts": {}, "tsx": {}, "mts": {}, "cts": {},
	"json": {}, "yaml": {}, "yml": {}, "xml": {}, "toml": {}, "ini": {}, "cfg": {}, "conf": {}, "properties": {}, "env": {}, "lock": {},
	"md": {}, "markdown": {}, "txt": {}, "text": {}, "rst": {}, "adoc": {},
	"py": {}, "pyw": {}, "pyi": {}, "ipynb": {}, "rb": {}, "erb": {}, "haml": {}, "pl": {}, "pm": {}, "lua": {}, "sh": {}, "bash": {}, "zsh": {}, "fish": {}, "bat": {}, "cmd": {}, "ps1": {}, "psm1": {}, "psd1": {},
	"c": {}, "cc": {}, "cpp": {}, "cxx": {}, "h": {}, "hh": {}, "hpp": {}, "hxx": {}, "go": {}, "rs": {}, "swift": {}, "kt": {}, "kts": {}, "scala": {}, "cs": {}, "fs": {}, "vb": {}, "vbs": {}, "java": {}, "jsp": {}, "php": {}, "sql": {},
	"gd": {}, "godot": {}, "shader": {}, "unity": {}, "qml": {},
	"mk": {}, "cmake": {}, "gradle": {},
	"groovy": {}, "gvy": {}, "tf": {}, "hcl": {},
	"sln": {}, "csproj": {}, "vbproj": {}, "fsproj": {},
	"r": {}, "rmd": {}, "clj": {}, "cljs": {}, "ex": {}, "exs": {}, "erl": {}, "hrl": {}, "elm": {}, "nim": {}, "zig": {}, "v": {}, "d": {}, "m": {}, "mm": {},
	"hs": {}, "lhs": {}, "jl": {}, "cl": {}, "lisp": {}, "scm": {}, "ss": {}, "rkt": {}, "asm": {}, "s": {},
	"csv": {}, "tsv": {}, "graphql": {}, "gql": {}, "proto": {}, "sol": {}, "patch": {}, "diff": {},
	"vim": {}, "dart": {}, "vue": {}, "svelte": {}, "astro": {}, "tex": {}, "j2": {}, "ejs": {}, "hbs": {}, "mustache": {}, "liquid": {}, "pug": {}, "jade": {},
	"tsbuildinfo": {}, "info": {}, "local": {}, "development": {}, "production": {}, "staging": {}, "test": {}, "example": {},
	"log": {}, "out": {}, "err": {}, "pid": {}, "seed": {}, "snap": {},
	"code-snippets": {}, "code-workspace": {}, "tmlanguage": {},
	"desktop": {}, "template": {}, "spec": {}, "ps1xml": {},
}

func containsParentTraversal(value string) bool {
	normalized := strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func dedupeEntriesByPath(entries []fileEntry) []fileEntry {
	if len(entries) == 0 {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].RelPath != entries[j].RelPath {
			return entries[i].RelPath < entries[j].RelPath
		}
		return entryModePriority(entries[i].Mode) > entryModePriority(entries[j].Mode)
	})

	out := entries[:1]
	for _, entry := range entries[1:] {
		last := &out[len(out)-1]
		if entry.RelPath != last.RelPath {
			out = append(out, entry)
			continue
		}
		if linesEntriesShouldCoexist(*last, entry) {
			out = append(out, entry)
			continue
		}
		mergeFileEntry(last, entry)
	}
	return out
}

func dedupeEntriesByPathPreserveOrder(entries []fileEntry) []fileEntry {
	if len(entries) == 0 {
		return entries
	}

	out := make([]fileEntry, 0, len(entries))
	indicesByPath := make(map[string][]int, len(entries))
	for _, entry := range entries {
		indices, ok := indicesByPath[entry.RelPath]
		if !ok {
			indicesByPath[entry.RelPath] = []int{len(out)}
			out = append(out, entry)
			continue
		}
		merged := false
		for _, idx := range indices {
			if !linesEntriesShouldCoexist(out[idx], entry) {
				mergeFileEntry(&out[idx], entry)
				merged = true
				break
			}
		}
		if !merged {
			indicesByPath[entry.RelPath] = append(indices, len(out))
			out = append(out, entry)
		}
	}
	return out
}

// linesEntriesShouldCoexist returns true when two entryModeLines entries for the
// same path carry different ranges and should both survive dedup.
func linesEntriesShouldCoexist(a, b fileEntry) bool {
	if a.Mode != entryModeLines || b.Mode != entryModeLines {
		return false
	}
	// Bare lines (LinesStart == 0) absorbs any ranged entry.
	if a.LinesStart == 0 || b.LinesStart == 0 {
		return false
	}
	// Same range → dedupe.
	if a.LinesStart == b.LinesStart && a.LinesEnd == b.LinesEnd {
		return false
	}
	return true
}

func mergeFileEntry(dst *fileEntry, incoming fileEntry) {
	if entryModePriority(incoming.Mode) > entryModePriority(dst.Mode) {
		*dst = incoming
		return
	}
	if incoming.Mode == entryModeDiff && dst.Mode == entryModeDiff {
		dst.DiffWantStaged = dst.DiffWantStaged || incoming.DiffWantStaged
		dst.DiffWantUnstaged = dst.DiffWantUnstaged || incoming.DiffWantUnstaged
	}
	if incoming.GitVisible && !dst.GitVisible {
		dst.GitVisible = true
	}
	if incoming.AllowedByInclude && !dst.AllowedByInclude {
		dst.AllowedByInclude = true
		dst.BlockSource = incoming.BlockSource
	}
	if dst.TargetRoot == "" && incoming.TargetRoot != "" {
		dst.TargetRoot = incoming.TargetRoot
	}
	if dst.AbsPath == "" && incoming.AbsPath != "" {
		dst.AbsPath = incoming.AbsPath
	}
	if dst.ModTime.IsZero() && !incoming.ModTime.IsZero() {
		dst.ModTime = incoming.ModTime
	}
}

func dedupePreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func entryModePriority(mode entryMode) int {
	switch mode {
	case entryModeDiff:
		return 3
	case entryModeSnippet:
		return 2
	case entryModeLines:
		return 1
	default:
		return 0
	}
}
