# catclip - conCATenate to CLIPboard

Copy code context for AI assistants. One command, smart defaults, no setup.

```bash
catclip src
```

---

## You don't need to remember the flags

catclip has an interactive mode that builds commands for you. Just add `--` and pick from menus:

```bash
catclip                     # pick files or folders from a menu
catclip --                  # pick targets, then pick filters from menus
catclip src --              # pick filters for src from a menu
catclip src -- --           # chain menus to build a full command
```

Every flag, filter, and output mode is reachable through the menus. The resolved command is echoed back so you can reuse it later.

---

## Direct commands (when you know what you want)

```bash
# Targets
catclip src                          # a folder
catclip Button.tsx                   # a file (finds it anywhere)
catclip btn                          # fuzzy match
catclip src lib docs                 # multiple targets
catclip "*.go"                       # glob pattern — all .go files
catclip src "*.go"                   # union: src/ + all .go files

# Filtering
catclip src --only "*.ts"            # keep only .ts files
catclip src --exclude "*.css"        # skip CSS files
catclip src --recent 5               # 5 most recently modified
catclip src --depth 2                # shallow files only
catclip src --contains TODO          # files mentioning TODO
catclip src --snippet TODO           # only the matching blocks

# Git
catclip --changed                    # all changed files
catclip --changed-diff               # changes as patches
catclip --staged-diff                # staged changes as patches

# Output
catclip src --paths                  # bare file paths, one per line
catclip src -p                       # print to stdout instead of clipboard
catclip src/main.go -r -p            # raw file body, no wrappers

# Scopes — like running two catclip commands and combining results
catclip . --paths --then src         # repo structure + full files from src
catclip src --only "*.ts" --then docs --recent 5
```

Filters run left to right. Order matters:

```bash
catclip src --recent 10 --only "*.ts"   # 10 newest, then keep .ts
catclip src --only "*.ts" --recent 10   # .ts first, then 10 newest of those
```

For the full reference: `catclip --help` or `catclip --help-all`.

---

## Installation

### Homebrew (macOS / Linux)
```bash
brew tap tigreau/catclip && brew install catclip
```

### Direct install (macOS / Linux)
```bash
curl -fsSL https://raw.githubusercontent.com/tigreau/catclip/main/install.sh | bash
```

### Windows (PowerShell)
```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/tigreau/catclip/main/install.ps1 | iex"
```

Clipboard tool required: `pbcopy` (macOS, built-in), `xclip`/`xsel`/`wl-clipboard` (Linux), `clip.exe` (Windows, built-in).

catclip bundles its own `fzf` and `ripgrep` — no external dependencies needed.

<details><summary>Manual install, build from source, updating, uninstalling</summary>

#### Manual install (Windows release bundle)

Download `catclip_windows_amd64.zip` or `catclip_windows_arm64.zip` from releases.

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

Add `%LOCALAPPDATA%\Programs\catclip\bin` to your user `PATH`.

#### Manual install (Linux release bundle)

Download `catclip_linux_amd64.tar.gz` or `catclip_linux_arm64.tar.gz` from releases.

```bash
PREFIX="${PREFIX:-$HOME/.local}"
mkdir -p "$PREFIX/bin" "$PREFIX/share/catclip/bin"
tar -xzf catclip_linux_amd64.tar.gz
install -m 755 catclip catclip-tree "$PREFIX/bin/"
install -m 644 VERSION "$PREFIX/share/catclip/"
install -m 755 bin/rg bin/fzf "$PREFIX/share/catclip/bin/"
```

#### Build from source (developer-only)

```bash
git clone https://github.com/tigreau/catclip.git
cd catclip && ./install.sh
```

Requires Go (version in `go.mod`), `rg`, and `fzf` at install time.

#### Updating & uninstalling

```bash
# Homebrew
brew upgrade catclip
brew uninstall catclip

# macOS / Linux script install
./uninstall.sh
```

```powershell
# Windows
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/tigreau/catclip/main/uninstall.ps1 | iex"
```

</details>

---

## Configuration

catclip skips `.gitignored` paths and paths matched by `.hiss` (the ignore config at `~/.config/catclip/.hiss`). Created automatically on first run.

```bash
catclip --hiss             # edit ignore rules
catclip --hiss-reset       # restore defaults
catclip --include tests    # allow an ignored folder for this run
```

---

## Contributing

PRs welcome! Keep changes portable across macOS, Linux, and Windows.
