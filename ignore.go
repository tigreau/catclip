package catclip

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultHissContents = `# catclip ignore config — gitignore syntax (handed to ripgrep verbatim)
#
# *.log          → pattern, matches anywhere
# build/         → directory named "build", anywhere
# secrets.yaml   → file named "secrets.yaml", anywhere
# /config.json   → file at project root only (leading slash anchors)
# src/index.js   → anchored: matches src/index.js, not foo/src/index.js
# **/snap.png    → matches at any depth
# !keep.log      → re-include after an earlier rule excluded it
#
# Edit with: catclip --hiss

# Secrets (never leak these)
.env
.env.local
.env.*
*.pem
*.key
*.p12
*.pfx
id_rsa
id_ed25519
application.properties
application.yml
secrets.yaml
credentials.json

# Version Control
.git/
.svn/
.hg/

# System / Junk
.DS_Store
.AppleDouble
.LSOverride
*.log
*.tmp
*.bak
*.swp
*.swo

# Text-encoded assets (XML/JSON/legacy formats unlikely to be useful as clipboard content)
*.svg
*.map
*.xpm
*.hex
*.pbm
*.ppm
*.rtf
*.inf
*.eslintcache
desktop.ini

# IDEs & Editors
.idea/
.vscode/
.cursor/
.history/

# JavaScript / Node
node_modules/
bower_components/
jspm_packages/
coverage/
.npm/
.yarn/
.pnpm-store/

# Python
__pycache__/
venv/
.venv/
env/
.pytest_cache/
.mypy_cache/
.tox/
htmlcov/

# Java / Build
target/
build/
dist/
out/
bin/
obj/
.gradle/

# Web Frameworks
.next/
.nuxt/
.serverless/
.turbo/

# Test directories (remove these lines to include tests)
test/
tests/
__tests__/
fixtures/
__fixtures__/

# Lockfiles
package-lock.json
yarn.lock
pnpm-lock.yaml
poetry.lock
Pipfile.lock
Gemfile.lock
composer.lock
Cargo.lock
go.sum
`

func ensureGlobalHiss() (string, error) {
	path := globalHissPath()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(defaultHissContents), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func globalHissPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "catclip", ".hiss")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "catclip", ".hiss")
	}
	return filepath.Join(home, ".config", "catclip", ".hiss")
}

// compileGlob compiles a shell-style glob (used by --only/--exclude scope
// stages) into a regexp. The translator keeps "*" matching across path
// separators, mirroring the original Bash case semantics.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	return compileGlobWithStar(pattern, ".*")
}

func compileGlobWithStar(pattern, star string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			b.WriteString(star)
		case '?':
			if star == "[^/]*" {
				b.WriteString("[^/]")
			} else {
				b.WriteByte('.')
			}
		case '[':
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end >= len(pattern) {
				b.WriteString("\\[")
				continue
			}
			class := pattern[i : end+1]
			if class == "[]" {
				b.WriteString("\\[\\]")
			} else {
				b.WriteString(class)
			}
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
