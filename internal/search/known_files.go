package search

import (
	"path"
	"strings"
)

// Name-based text/binary classification for the hybrid Stage 2 classifier.
//
// THE DEFINITION (RULES.md rule 11): a file is binary ⇔ the full-file
// scan `rg --files-without-match --text -e '\x00'` says so — i.e. a NUL
// byte anywhere in rg's DECODED view of the file. rg BOM-sniffs before
// matching, so BOM'd UTF-16 transcodes and is TEXT by the definition
// (verified 2026-07-04: desktop.ini with FF FE BOM is "without match");
// only BOM-less UTF-16 and genuine binary carry NULs the pattern sees.
// The lists below are read-avoidance approximations of that definition,
// never a competing one. Membership bars are asymmetric because the
// failure modes differ:
//
//   - knownBinaryExts (blocklist): a wrong entry silently DROPS a text
//     file from output with no signal — the worst failure class. An
//     extension qualifies only if its format structurally guarantees NUL
//     bytes (container headers, length fields, machine code). "Usually
//     binary" is not enough; bimodal names stay off the list.
//   - knownTextExts / knownTextBasenames (allowlist): a wrong entry
//     admits binary bytes that fail visibly at the sink. Medium bar —
//     population NUL-free in practice.
//   - Anything the lists cannot decide goes to the residue, which pays
//     the definitional full NUL scan (see runRipgrepNulScanFiles).
//
// Collision dispositions (pinned by TestKnownFilesCollisionDispositions;
// see RESOLVED_PLAN_binary_detection_replacement.md "List design"):
//
//   .out          → neither (a.out is THE compiled executable)
//   .ts/.mts/.cts → text   (TypeScript; MPEG-TS video loses the tie)
//   .snap         → text   (Jest snapshots; squashfs loses the tie)
//   .lock         → text   (Cargo.lock / yarn.lock / Gemfile.lock)
//   .obj          → neither (Wavefront 3D models are text)
//   .stl          → neither (ASCII and binary variants exist)
//   .key          → neither (PEM private keys are text; Keynote is binary)
//   .crt          → neither (PEM text or DER binary under the same name)
//   .plist        → neither (XML or binary plist)
//   .strings      → neither (macOS UTF-16 variants; residue decides
//                   per file under the BOM-aware definition)
//   .db/.dat/.pak → neither (genuinely bimodal)
//   .pb           → neither (.pbtxt precedent; text protobuf dumps exist)
//   .ini/.rc      → neither (Windows UTF-16 variants are common; residue
//                   gives exact per-file conformance with the definition.
//                   Note: BOM'd UTF-16 like desktop.ini is TEXT by the
//                   definition — rg transcodes BOM'd files before the NUL
//                   pattern runs, verified 2026-07-04 — and desktop.ini
//                   itself is excluded by a default .hiss name rule
//                   (ignore.go), not by classification. Only BOM-less
//                   UTF-16 classifies binary.)
//
// Provenance: knownTextExts/knownTextBasenames resurrect the v0.5.0 lists
// deleted in e3f0346 ("ripgrep is the sole content authority"), with a
// CHANGED CONTRACT: entries here are never content-checked, so the old
// list's `.out` entry is removed and the additions were re-audited against
// the NUL rule. knownBinaryExts is new (the old code had no blocklist).

type nameClass uint8

const (
	nameClassUnknown nameClass = iota
	nameClassText
	nameClassBinary
)

// classifyPathByName classifies rel by basename/extension alone. It never
// touches the filesystem. nameClassUnknown means "the residue scan must
// decide" — it is not an error and not a default to text or binary.
func classifyPathByName(rel string) nameClass {
	base := strings.ToLower(path.Base(rel))
	if _, ok := knownTextBasenames[base]; ok {
		return nameClassText
	}
	ext := shellStyleExtension(rel)
	if ext == "" {
		return nameClassUnknown
	}
	if _, ok := knownBinaryExts[ext]; ok {
		return nameClassBinary
	}
	if _, ok := knownTextExts[ext]; ok {
		return nameClassText
	}
	return nameClassUnknown
}

// shellStyleExtension returns the lowercased last dot-segment of the
// basename ("archive.tar.gz" → "gz", "component.d.ts" → "ts"), or "" for
// no extension, a leading-dot-only name (".gitignore"), or a trailing dot.
// Identical semantics to the v0.5.0 helper.
func shellStyleExtension(relPath string) string {
	base := strings.ToLower(path.Base(relPath))
	lastDot := strings.LastIndexByte(base, '.')
	if lastDot <= 0 || lastDot == len(base)-1 {
		return ""
	}
	return base[lastDot+1:]
}

// knownBinaryExts: formats whose specification guarantees NUL bytes.
// Blocklist bar: zero text-variant precedent, ever. When in doubt, leave
// it out — the residue scan is correct, just slower.
var knownBinaryExts = map[string]struct{}{
	// images
	"png": {}, "jpg": {}, "jpeg": {}, "gif": {}, "bmp": {}, "ico": {}, "webp": {},
	"tif": {}, "tiff": {}, "heic": {}, "heif": {}, "avif": {}, "jxl": {}, "psd": {},
	"icns": {}, "cur": {},
	// audio / video
	"mp3": {}, "mp4": {}, "m4a": {}, "m4v": {}, "mov": {}, "avi": {}, "wav": {},
	"webm": {}, "mkv": {}, "flv": {}, "wmv": {}, "flac": {}, "ogg": {}, "oga": {},
	"ogv": {}, "opus": {}, "mid": {}, "midi": {}, "aac": {},
	// archives / packages
	"zip": {}, "tar": {}, "gz": {}, "tgz": {}, "bz2": {}, "tbz2": {}, "xz": {},
	"txz": {}, "zst": {}, "br": {}, "lz4": {}, "lzma": {}, "7z": {}, "rar": {},
	"jar": {}, "war": {}, "ear": {}, "apk": {}, "aab": {}, "ipa": {}, "deb": {},
	"rpm": {}, "dmg": {}, "iso": {}, "msi": {}, "cab": {}, "vsix": {}, "crx": {},
	"xpi": {}, "whl": {}, "egg": {},
	// executables / object code
	"exe": {}, "dll": {}, "so": {}, "dylib": {}, "bin": {}, "o": {}, "a": {},
	"lib": {}, "class": {}, "pyc": {}, "pyd": {}, "pyo": {}, "wasm": {},
	"node": {}, "elc": {}, "beam": {}, "pdb": {},
	// fonts
	"woff": {}, "woff2": {}, "ttf": {}, "otf": {}, "eot": {}, "ttc": {},
	// data / ML
	"sqlite": {}, "sqlite3": {}, "parquet": {}, "orc": {}, "avro": {},
	"feather": {}, "arrow": {}, "onnx": {}, "pt": {}, "pth": {},
	"safetensors": {}, "ckpt": {}, "h5": {}, "hdf5": {}, "npy": {}, "npz": {},
	"pkl": {}, "pickle": {},
	// documents
	"pdf": {}, "doc": {}, "docx": {}, "xls": {}, "xlsx": {}, "ppt": {},
	"pptx": {}, "odt": {}, "ods": {}, "odp": {},
	// certificates / key containers (binary-only encodings; .pem/.crt/.key
	// deliberately absent — PEM text shares those names)
	"der": {}, "p12": {}, "pfx": {}, "jks": {}, "gpg": {},
	// other
	"swf": {}, "blend": {}, "fbx": {}, "glb": {}, "mo": {},
}

// knownTextExts: populations NUL-free in practice. Resurrected from
// e3f0346~1 minus `.out` (a.out) plus the modern-source-tree gap set —
// see the file header for the changed contract.
var knownTextExts = map[string]struct{}{
	"html": {}, "htm": {}, "css": {}, "scss": {}, "sass": {}, "less": {}, "js": {}, "jsx": {}, "mjs": {}, "cjs": {}, "ts": {}, "tsx": {}, "mts": {}, "cts": {},
	"json": {}, "yaml": {}, "yml": {}, "xml": {}, "toml": {}, "cfg": {}, "conf": {}, "properties": {}, "env": {}, "lock": {},
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
	"log": {}, "err": {}, "pid": {}, "seed": {}, "snap": {},
	"code-snippets": {}, "code-workspace": {}, "tmlanguage": {},
	"desktop": {}, "template": {}, "spec": {}, "ps1xml": {},
	// 2026-07-04 additions (see plan "List design"):
	"svg": {}, "ml": {}, "mli": {}, "nix": {}, "mdx": {}, "jsonc": {}, "json5": {},
	"jsonl": {}, "ndjson": {}, "map": {}, "po": {}, "pot": {}, "srt": {}, "vtt": {},
	"glsl": {}, "hlsl": {}, "wgsl": {}, "vert": {}, "frag": {}, "comp": {}, "metal": {},
	"xaml": {}, "storyboard": {}, "xib": {}, "pbxproj": {}, "xcconfig": {},
	"modulemap": {}, "def": {}, "manifest": {}, "pem": {},
	"cue": {}, "dhall": {}, "jsonnet": {}, "libsonnet": {}, "rego": {},
	"gleam": {}, "odin": {}, "purs": {}, "hx": {}, "heex": {},
	"bib": {}, "org": {}, "typ": {}, "prisma": {}, "webmanifest": {},
}

// knownTextBasenames: extensionless (or fixed-name) files matched on the
// lowercased basename. Resurrected from e3f0346~1 plus the 2026-07-04
// additions.
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
	// 2026-07-04 additions:
	"justfile": {}, "caddyfile": {}, "tiltfile": {}, "earthfile": {}, "brewfile": {},
	"build": {}, "workspace": {}, "module.bazel": {}, ".bazelrc": {}, ".buckconfig": {},
	".envrc": {}, ".tool-versions": {}, ".python-version": {}, ".node-version": {}, ".ruby-version": {},
	".clang-format": {}, ".clang-tidy": {},
	".zshrc": {}, ".bashrc": {}, ".bash_profile": {}, ".profile": {}, ".inputrc": {}, ".gitconfig": {},
	"notice": {}, "copying": {}, "copyright": {}, "install": {}, "news": {},
	".hiss": {},
}
