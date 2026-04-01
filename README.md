# catclip - conCATenate to CLIPboard

One command to copy your entire codebase to clipboard for AI assistants.
```bash
catclip src  # That's it.
```
Don't worry about accidentally copying that `package-lock.json` or creating a `.gitignore` before first run.



---

## Features

- ⚡ **Instant** - Zero setup, smart defaults, copies 5000+ files in seconds
- 🔍 **Fuzzy when needed** - `catclip components` resolves directly when unique or with bundled `fzf` when multiple matches remain
- 📄 **Near-instant filename lookup** - `catclip Footer.tsx` or shorthands like `Foo` resolve exact file names across the repo almost instantly
- 🧩 **Multiple targets** - `catclip README.md src docs` in one run
- 🧾 **File headers in output** - each file is wrapped in `<file path="path/to/file">` tags
- 🌳 **Visual preview** - Tree view with file count, size, and token estimate before copying
- 🙈 **Git-aware** - Respects safe discovery rules from `.gitignore` and `.hiss`, filters by staged/unstaged/untracked in git repos, and can output diffs instead of full files
- 🎛️ **Flexible ignores** - `--exclude "*.css"` to skip, `--include` to authorize blocked files or directories from `.gitignore` or `.hiss`, `--only` to narrow authorized targets safely
- 🛡️ **Secret protection** - Blocks `.env`, keys, credentials

---

## Installation

### Homebrew (Recommended)
```bash
brew tap tigreau/catclip && brew install catclip
```
Packaged installs are expected to include catclip plus private bundled `rg` and `fzf` helpers. Runtime does not fall back to arbitrary user `PATH` copies.

### Direct install script (macOS / Linux)
```bash
curl -fsSL https://raw.githubusercontent.com/tigreau/catclip/main/install.sh | bash
```

If `curl` is not available:
```bash
wget -qO- https://raw.githubusercontent.com/tigreau/catclip/main/install.sh | bash
```

This installer downloads the latest prebuilt release bundle. It does not require Go.

**Requirements**: Clipboard tool (auto-detected)
- macOS: Built-in ✓
- Linux: `xclip` or `wl-clipboard`

**Bundled with catclip**:
- `ripgrep` for Git-visible file discovery and `--contains`
- `fzf` for fuzzy target resolution

Packaged installs always carry private bundled `rg` and `fzf` binaries. If one is missing, the install is incomplete and should be reinstalled instead of relying on a system fallback.

<details><summary>Manual install (Linux binary, no script)</summary>

Download the release archive that matches your architecture:

- `catclip_linux_amd64.tar.gz` for `x86_64`
- `catclip_linux_arm64.tar.gz` for `aarch64` / `arm64`

```bash
PREFIX="${PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
SHARE_DIR="$PREFIX/share/catclip"

mkdir -p "$BIN_DIR" "$SHARE_DIR"
tar -xzf catclip_linux_amd64.tar.gz
install -m 755 catclip "$BIN_DIR/catclip"
install -m 755 catclip-tree "$BIN_DIR/catclip-tree"
install -m 644 VERSION "$SHARE_DIR/VERSION"
install -d "$SHARE_DIR/bin"
install -m 755 bin/rg "$SHARE_DIR/bin/rg"
install -m 755 bin/fzf "$SHARE_DIR/bin/fzf"
```

If `~/.local/bin` is not already on `PATH`, add it in your shell profile.

The global config (`~/.config/catclip/.hiss`) is created automatically on first run.
</details>

<details><summary>Build from source</summary>

```bash
git clone https://github.com/tigreau/catclip.git
cd catclip
./install.sh
```

When run from a cloned checkout, `./install.sh` builds the checked-out source instead of downloading a release bundle.
Go is only required for this source-install path, and it must satisfy the version declared in `go.mod`.
`rg` and `fzf` are also required once at install time for source installs, because `./install.sh` copies your current local binaries into the packaged install under `share/catclip/bin/`.
The local `rg` and `fzf` must also be new enough for catclip's current feature set; older distro builds may be rejected during install even if the binaries themselves are present.
Published release installs do not need local Go / rg / fzf because the release archive already contains the bundled copies.

Manual local build (developer-only, not a full packaged install):
```bash
go build ./cmd/catclip
```
This raw binary does not include the private bundled `rg`/`fzf` tools. For a normal local install, use `./install.sh`.
</details>

<details><summary>Updating & Uninstalling</summary>
Use the section that matches how you installed catclip.

```bash
# Homebrew
brew upgrade catclip
brew uninstall catclip

# Direct script / local install
./uninstall.sh

# Manual binary install
rm -f "$HOME/.local/bin/catclip" \
      "$HOME/.local/bin/catclip-tree" \
      "$HOME/.local/share/catclip/VERSION" \
      "$HOME/.local/share/catclip/bin/rg" \
      "$HOME/.local/share/catclip/bin/fzf"
```
</details>

## Quick Start
```bash
# Open the safe picker (interactive TTY):
catclip

# Open the modifier picker (interactive TTY):
catclip --

# Copy source directory:
catclip src

# Fuzzy search:
catclip components              # Resolves a unique 'components' dir directly; if multiple exist, fzf lets you choose

# Direct file target:
catclip Button.tsx              # Near-instant exact basename lookup anywhere
catclip Sidebar.tsx             # Another exact basename lookup

# Direct scoped shorthand:
catclip layout/Footer.tsx       # Resolves directly when unique

# File shorthand:
catclip btn.tsx                 # Resolves directly when unique, otherwise uses bundled fzf

# Exact nested path:
catclip src/components/ui/Button.tsx

# Multiple targets at once:
catclip README.md src docs Button.tsx
```

Plain targets stay independent: `catclip src Button.tsx docs` searches `Button.tsx` across the whole repo. Exact paths, exact basenames, and deterministic shorthand resolve directly; bundled `fzf` is only used when shorthand still has multiple viable matches.

<details>
<summary><b>More Examples</b></summary>

```bash
# Authorize a blocked directory for this run:
catclip --include tests

# Authorize a blocked file for this run:
catclip --include .env.production

# Authorize and narrow a blocked target safely:
catclip --include coverage --only "*.json"

# Output to screen (stdout) instead of clipboard:
catclip src --print

# Preview what would be copied (fast dry-run):
catclip src --preview

# Skip files (this run only):
catclip src --exclude "LoginForm.tsx"

# Only files containing a pattern (regex):
catclip src --contains "TODO"

# Only blocks around TODO matches (not full files):
catclip src --contains "TODO" --snippet

# Staged changes as unified diff (great for commit review):
catclip --staged --diff

# All changes as patches + architecture reference:
catclip --changed --diff --then src/api/reference.ts
```

</details>

## Git-Aware Context

### Changed files
```bash
catclip --changed              # All modified: staged + unstaged + untracked
catclip src --changed          # Scoped to src
```

### Composable git filters
Use specific filters instead of `--changed` to narrow what you grab:
```bash
catclip --staged               # Files in the git index (staged for commit)
catclip --unstaged             # Tracked files with uncommitted modifications
catclip --untracked            # New files not yet tracked by git
catclip --staged --untracked   # Combine: staged + new, skip WIP edits
```
`--changed` is shorthand for all three. Each flag implies `--changed` automatically.

### Diff output
Replace full file content with unified git patches:
```bash
catclip --changed --diff       # All modified files as patches
catclip --staged --diff        # Staged changes only — ideal for commit review
catclip --unstaged --diff      # WIP edits — what you're actively changing
```
`--untracked --diff` is rejected because untracked files have no patch.
With `--changed --diff`, tracked files emit patches and untracked files are still
included with their full content.
The tree preview shows `[diff only]` or `[snippet only]` on files with partial output.

### Snippet extraction
With `--contains`, extract only the blank-line-bounded blocks around each match instead of the full file:
```bash
catclip src --contains "TODO" --snippet        # Blocks around each TODO
catclip . --contains "useState" --snippet      # React hook call-sites only
```
If a match's blank-line-bounded block spans the whole file, `--snippet` can
look identical to full-file output for that file even though snippet mode is
active.

### Patterns stay in `--only` / `--exclude`
Pattern filtering is canonical only inside stage modifiers:
```bash
catclip src --only "*.ts"      # canonical pattern filter
catclip src --exclude "*.snap" # canonical exclude filter
catclip "*.ts"                 # errors with a fix-it to --only
catclip .tsx                   # errors with a fix-it to --only "*.tsx"
catclip "*Button*"             # target query; normalizes to Button
```
Quoted wrapper-star target queries still normalize because target lookup is
already fuzzy by default. True patterns and bare extensions are taught back as
explicit `--only` syntax instead of being treated as positional targets.

## Scopes
Use `--then` to apply different modifiers to different targets:
```bash
catclip src --only "*.ts" --then tests --only "*.test.ts"
#   Scope 1: src — only TypeScript source files
#   Scope 2: tests — only test files
```
Overlapping scopes are allowed; final copied files are deduplicated by path.
A bare `--then` starts a normal new scope from `.`; it is not a "remaining
files only" operator. Think of `--then` as the one-command equivalent of
running multiple `catclip` scope commands and unioning their results.
Without `--then`, all targets share the same modifiers:
```bash
catclip src lib --only "*.ts"   # Both filtered to .ts files
```

Within one scope, modifier stages are sequential.

That means:

- each `--only ...` occurrence is one narrowing stage
- each `--exclude ...` occurrence is one removing stage
- values inside one occurrence OR together
- later stages run after earlier ones
- stage values use four syntax forms:
  - `*.ts` = glob
  - `utils` = bare name (basename `utils` or directory segment `utils`)
  - `src/utils` = anchored path (exact file or subtree)
  - `utils/` or `src/utils/` = subtree filter

Example:

```bash
catclip src \
  --only "*.ts" "*.tsx" \
  --exclude "*.test.tsx" "*.stories.tsx" \
  --only "*Auth*" "*Login*"
```

Meaning:

1. start from `src`
2. keep TypeScript files
3. remove test and story files
4. narrow again to files whose names match `*Auth*` or `*Login*`

`--include` is intentionally different: it is an additive authorization stage,
not a normal sequential filter stage like `--only` / `--exclude`.

---

## Configuration

catclip uses `~/.config/catclip/.hiss` (gitignore-inspired syntax, created on first run) plus the local project `.gitignore` as its safe discovery baseline.

On first run, catclip creates the default `.hiss` and applies it immediately, so discovery is still safe from the start.

That means you usually do not need to explain ignores every run: `.hiss` plus local `.gitignore` already block things like `.env.*`, `credentials.json`, `.idea/`, `.vscode/`, `tests/`, `fixtures/`, `coverage/`, and lockfiles such as `package-lock.json`, `Cargo.lock`, and `go.sum`.

```
# Example .hiss file (trailing / = directory)
node_modules/
*.log
.env
```

Edit config:
```bash
catclip --hiss             # open ignore config in editor
catclip --hiss-reset       # restore defaults
```

For this run only:
```bash
catclip src --exclude "*.test.*"           # skip test files
catclip --include tests                    # authorize ignored tests/
catclip --include .env.production          # authorize a blocked file
catclip --include coverage --only "*.json" # authorize ignored coverage/, then narrow
```

---

## Options

| Flag | Description |
|------|-------------|
| `-h`, `--help` | Show help |
| `-y`, `--yes` | Skip confirmation |
| `-q`, `--quiet` | Suppress normal stderr output; in non-preview runs this also skips tree rendering and confirmation |
| `-v`, `--verbose` | Show phase timings and debug info |
| `--exclude VALUE...` | Add one exclude stage (glob, bare name, anchored path, or trailing `/` subtree) |
| `-p`, `--print` | Output to screen (stdout) instead of clipboard; output spinner stays off so stdout remains clean, and interactive TTY runs add a stderr separator line before payload output |
| `--hiss` | Open ignore config in editor |
| `-t`, `--no-tree` | Skip tree rendering |
| `--hiss-reset` | Restore default ignore config |
| `--only VALUE...` | Add one only stage (glob, bare name, anchored path, or trailing `/` subtree) |
| `--changed` | All modified files: staged + unstaged + untracked (requires Git) |
| `--staged` | Only staged files (git index) |
| `--unstaged` | Only unstaged tracked modifications |
| `--untracked` | Only new untracked files |
| `--diff` | Emit unified diff instead of full file (requires `--changed`, `--staged`, or `--unstaged`; `--changed` may still include untracked files as full content) |
| `--contains PATTERN` | Only files whose contents match regex pattern |
| `--snippet` | With `--contains`: emit only matched blocks (blank-line bounded) |
| `--then` | Start a new scope (separate targets with different modifiers) |
| `--preview` | Show file tree and token count without copying |
| `--include VALUE...` | Authorize one or more ignored targets for this scope |

Full docs: `catclip --help`

Stage semantics:

- `--only` and `--exclude` are sequential stages within a scope
- repeating them later starts another stage, not a global merged set
- `--include` is additive and special: it authorizes ignored targets into the
  current scope before later stages run

---

## Troubleshooting

<details>
<summary><b>No clipboard tool found</b></summary>

Install for your platform:
```bash
# Ubuntu/Debian
sudo apt install xclip  # or wl-clipboard for Wayland

# Fedora
sudo dnf install xclip # or wl-clipboard for Wayland

# Arch
sudo pacman -S xclip # or wl-clipboard for Wayland
```
Or output to screen (stdout): `catclip src --print > code.txt`

</details>

<details>
<summary><b>Directory ignored</b></summary>

Check: `catclip --hiss`
Include this run: use `--include`, optionally with `--only` to narrow
Remove permanently: `catclip --hiss` (delete the line from the config)

</details>

---

## Contributing

PRs welcome! Keep changes portable across macOS and Linux, and preserve CLI/output parity.

1. Fork & clone
2. Create branch: `git checkout -b feature/name`
3. Submit PR
