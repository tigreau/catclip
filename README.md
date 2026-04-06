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
- 🎛️ **Flexible ignores** - `--exclude "*.css"` to skip, `--include` to allow blocked files or directories from `.gitignore` or `.hiss`, `--only` to narrow allowed targets safely
- 🕒 **Recent-file stage** - `--recent` sorts the current file set by newest mtime first, with an optional top-N limit
- 🛡️ **Secret protection** - Blocks `.env`, keys, credentials

---

## Installation

### Homebrew (macOS / Linux Recommended)
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
Published Linux release bundles target an Ubuntu LTS baseline and should be validated there for `glibc` compatibility across `catclip`, `catclip-tree`, bundled `rg`, and bundled `fzf`.

### PowerShell install script (Windows)
```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/tigreau/catclip/main/install.ps1 | iex"
```

This installs the latest published native Windows release bundle into `%LOCALAPPDATA%\Programs\catclip`.
PowerShell is the supported installer entrypoint for native Windows; `install.sh` remains Unix-only.
When using the default Windows install root, the installer adds `%LOCALAPPDATA%\Programs\catclip\bin` to your user `PATH` automatically.
Open a new PowerShell or CMD session after install before running `catclip` by name.
WSL remains a separate Linux environment; use the Linux installer inside the WSL guest instead of the native Windows install root.
Native Windows packaged installs keep the same private bundled-tool model as macOS/Linux:

- `catclip.exe`
- `catclip-tree.exe`
- private bundled `rg.exe`
- private bundled `fzf.exe`
- version metadata under `share\catclip`

**Requirements**: Clipboard tool (auto-detected)
- macOS: Built-in `pbcopy`
- Linux: `xclip`, `xsel`, or `wl-clipboard`
- Windows: Built-in `clip.exe`

**Bundled with catclip**:
- `ripgrep` for Git-visible file discovery and `--contains`
- `fzf` for fuzzy target resolution

Packaged installs always carry private bundled `rg` and `fzf` binaries. If one is missing, the install is incomplete and should be reinstalled instead of relying on a system fallback.
Normal users should use the published release bundles. Source installs are a developer-only path.

<details><summary>Manual install (Windows release bundle)</summary>

Download the release archive that matches your architecture:

- `catclip_windows_amd64.zip` for `x86_64` / `amd64`
- `catclip_windows_arm64.zip` for `arm64`

```powershell
$InstallRoot = Join-Path $env:LOCALAPPDATA "Programs\catclip"
$BinDir = Join-Path $InstallRoot "bin"
$ShareDir = Join-Path $InstallRoot "share\catclip"
$ToolsDir = Join-Path $ShareDir "bin"

New-Item -ItemType Directory -Force -Path $BinDir, $ToolsDir | Out-Null
Expand-Archive -LiteralPath .\catclip_windows_amd64.zip -DestinationPath .
Copy-Item .\catclip.exe (Join-Path $BinDir "catclip.exe") -Force
Copy-Item .\catclip-tree.exe (Join-Path $BinDir "catclip-tree.exe") -Force
Copy-Item .\VERSION (Join-Path $ShareDir "VERSION") -Force
Copy-Item .\bin\rg.exe (Join-Path $ToolsDir "rg.exe") -Force
Copy-Item .\bin\fzf.exe (Join-Path $ToolsDir "fzf.exe") -Force
```

Add `%LOCALAPPDATA%\Programs\catclip\bin` to your user `PATH` if it is not already exported.
The PowerShell installer does this automatically only for the default install root.

The global config (`~/.config/catclip/.hiss`) is created automatically on first run.
On Windows, `catclip --hiss` opens that file in `notepad.exe` by default unless
`VISUAL` or `EDITOR` is set. catclip checks `VISUAL` first, then `EDITOR`,
then falls back to `notepad.exe`; `VISUAL` is the richer preferred editor
slot, while `EDITOR` is the general fallback editor slot.
</details>

<details><summary>Manual install (Linux release bundle)</summary>

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
Published Linux release bundles target Ubuntu LTS and should be validated there so `catclip`, `catclip-tree`, `rg`, and `fzf` keep broad `glibc` compatibility.

The global config (`~/.config/catclip/.hiss`) is created automatically on first run.
</details>

<details><summary>Build from source (developer-only)</summary>

```bash
git clone https://github.com/tigreau/catclip.git
cd catclip
./install.sh
```

Windows PowerShell from a cloned checkout:

```powershell
winget install -e --id GoLang.Go --source winget
winget install -e --id BurntSushi.ripgrep.MSVC --source winget
winget install -e --id junegunn.fzf --source winget

# Start a new PowerShell session after winget installs complete.
.\install.ps1
```

When run from a cloned checkout, the platform installer builds the checked-out source instead of downloading a release bundle.
Go is only required for this source-install path, and it must satisfy the version declared in `go.mod`.
`rg` and `fzf` are also required once at install time for source installs, because the install scripts copy your current local binaries into the packaged install under `share/catclip/bin/`.
On Windows, the intended developer setup is the native toolchain plus Windows-native helper binaries:

- `winget install -e --id GoLang.Go --source winget`
- `winget install -e --id BurntSushi.ripgrep.MSVC --source winget`
- `winget install -e --id junegunn.fzf --source winget`

The local `rg` and `fzf` must also be new enough for catclip's current feature set; older distro builds may be rejected during install even if the binaries themselves are present.
Published release installs do not need local Go / rg / fzf because the release archive already contains the bundled copies.
On Windows, `install.ps1` also adds the default `%LOCALAPPDATA%\Programs\catclip\bin` directory to your user `PATH`; open a new PowerShell or CMD session afterward.
For normal installs, prefer the published release bundles instead of a source build.

Manual local build (developer-only, not a full packaged install):
```bash
go build ./cmd/catclip
```
This raw binary does not include the private bundled `rg`/`fzf` tools. For a normal local install, use the platform installer instead.
</details>

<details><summary>Updating & Uninstalling</summary>
Use the section that matches how you installed catclip.

```bash
# Homebrew
brew upgrade catclip
brew uninstall catclip

# macOS / Linux direct script / local install
./uninstall.sh
```

```powershell
# Windows PowerShell direct script / local install
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/tigreau/catclip/main/uninstall.ps1 | iex"

# Windows local checkout
.\uninstall.ps1
```

The PowerShell uninstaller removes the default user `PATH` entry for `%LOCALAPPDATA%\Programs\catclip\bin` when applicable.

```bash
# Linux manual release-bundle install
rm -f "$HOME/.local/bin/catclip" \
      "$HOME/.local/bin/catclip-tree" \
      "$HOME/.local/share/catclip/VERSION" \
      "$HOME/.local/share/catclip/bin/rg" \
      "$HOME/.local/share/catclip/bin/fzf"
```
</details>

## Quick Start
```bash
# Open the safe picker (interactive terminal):
catclip

# Pick files or folders, then pick a filter (interactive terminal):
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
# Allow a blocked directory for this run:
catclip --include tests

# Allow a blocked file for this run:
catclip --include .env.production

# Allow and narrow a blocked target safely:
catclip --include coverage --only "*.json"

# Output to screen (stdout) instead of clipboard:
catclip src --print

# Preview what would be copied (fast dry-run):
catclip src --preview

# Newest files first:
catclip src --recent 5

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

## Filters and Scopes

Filters run left to right. Changing the order changes the result.

Use shell globs like `*.ts` for `--only` / `--exclude`. `--contains` uses
regex.

```bash
catclip src --only "*.ts" "*.tsx"                       # keep matching files
catclip src --exclude "*.css" "*.svg"                  # skip matching files
catclip src --only "*.tsx" --exclude "Header.tsx" "Login.tsx" --recent 5
```

These two commands are different:

```bash
catclip src --recent 10 --only "*.ts"   # take the 10 newest files, then keep the .ts ones
catclip src --only "*.ts" --recent 10   # keep .ts first, then take the 10 newest of that set
```

Use `--then` to start a new target group with different filters:

```bash
catclip src --only "*.ts" --then docs
```

Without `--then`, all targets share the same filters:

```bash
catclip src lib --only "*.ts"
```

`--include` is different: it allows ignored files or folders into the current
scope before later filters run.

```bash
catclip --include tests
catclip --include coverage --only "*.json"
```

## Git, Search, and Recent Files

```bash
catclip --changed                    # staged + unstaged + untracked
catclip --staged --diff             # staged changes as patches
catclip src --contains "TODO"       # content search (regex)
catclip src --contains "TODO" --snippet
catclip src --recent 3
```

`--changed` is git-only. With `--diff`, tracked files emit patches and
untracked files still emit full content.

`--contains` searches file contents with regex. `--snippet` keeps only the
matching blocks instead of the full file.

`--recent` works in git and non-git directories.

Bare `--recent` keeps all files and changes payload order only; the tree
preview stays path-sorted.

For exact stage ordering, overlap rules, pattern edge cases, and startup
picker behavior, run `catclip --help-all`.

---

## Configuration

catclip uses `~/.config/catclip/.hiss` plus the local project `.gitignore` as
its safe discovery baseline. On first run, catclip creates the default
`.hiss` and applies it immediately.

That path stays the same on Windows in `v0.3.3`. When opening it with
`catclip --hiss`, catclip checks `VISUAL` first, then `EDITOR`, and only then
uses the platform default editor fallback.

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
catclip src --exclude "*.css"              # skip CSS files
catclip --include tests                    # allow ignored tests/
catclip --include .env.production          # allow a blocked file
catclip --include coverage --only "*.json" # allow ignored coverage/, then narrow
```

---

## Common Options

- `--preview` shows what would be copied without copying
- `-p`, `--print` writes to stdout instead of the clipboard
- `-q`, `--quiet` suppresses normal stderr output
- `-y`, `--yes` skips confirmation for large copies
- `-t`, `--no-tree` skips the tree preview

For the full flag list, run `catclip --help`.

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

PRs welcome! Keep changes portable across macOS, Linux, and Windows, and preserve CLI/output parity.

1. Fork & clone
2. Create branch: `git checkout -b feature/name`
3. Submit PR
