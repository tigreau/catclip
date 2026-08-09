# catclip

Copy code context for AI assistants. One command, smart defaults, no setup.

```bash
catclip src                # copy src/ to your clipboard
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

These examples use one project with a React app in `src/`, Go code in `cmd/`
and `internal/`, and documentation in `docs/`.

```bash
# Targets
catclip src                            # a folder
catclip Button.tsx                     # a file (finds it anywhere)
catclip btn                            # fuzzy picker
catclip src internal docs              # multiple targets
catclip "*.go"                         # glob pattern — all Go files
catclip src "*.go"                     # union: src/ + all Go files

# Filtering
catclip src --only "*.tsx"             # keep only TSX components
catclip src --exclude "*.css"          # skip the styles
catclip internal --only "handler/"     # keep files under handler/ directories
catclip internal --exclude "handler/"  # skip files under handler/ directories
catclip src --recent 5                 # 5 most recently modified
catclip src --size 0 100               # files up to 100 KiB, largest first
catclip src --depth 1                  # just the top level of src
catclip src --contains TODO            # files mentioning TODO
catclip src --snippet useAuth          # smart block: smallest enclosing unit
catclip src --snippet useAuth 3        # each match plus 3 lines of context

# Git
catclip . --changed                    # all uncommitted files
catclip . --changed-diff               # uncommitted changes as patches
catclip . --staged-diff                # staged changes as patches

# Output
catclip src --paths                    # bare file paths, one per line
catclip src -p                         # print to stdout instead of clipboard
catclip cmd/api/main.go -r             # raw file body, no wrappers

# --then — like running two catclip commands and combining results
catclip . --paths --then src           # project structure + full React files
catclip src --only "*.tsx" --then docs --recent 5  # TSX components, plus 5 newest docs
```

Filters run left to right. Order matters:

```bash
catclip src --recent 10 --only "*.tsx"  # 10 newest, then keep TSX
catclip src --only "*.tsx" --recent 10  # TSX first, then 10 newest of those
```

For the full command reference: `catclip --help` or `catclip --help-all`.
For smart snippet boundaries, see
[Snippet Smart Block Rules](SNIPPET_SMART_BLOCK_RULES.md).

---

## Big copies become a file, automatically

Large copies (over 4 KB) are saved to `~/Documents/catclip/` and placed on your clipboard as a *file* — paste it into a chat as an attachment instead of a giant blob. Smaller copies stay plain text.

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
irm https://raw.githubusercontent.com/tigreau/catclip/main/install.ps1 | iex
```

Clipboard tool required: `pbcopy` (macOS, built-in), `clip.exe` (Windows, built-in), `wl-clipboard` (Linux — Wayland-only, install needed).

<details><summary>X11 is unsupported</summary>

catclip is Wayland-only on Linux: X11 sessions are refused at startup, which
excludes Cinnamon, MATE, Xfce, and any GNOME/KDE session started in X11 mode.
Verified working: GNOME 46+ (older GNOME warns), KDE Plasma on Wayland, and WSL.
Other Wayland compositors such as Sway or Hyprland should work but are untested.

</details>

<br>

catclip bundles its own `fzf` and `ripgrep` — no external dependencies needed.

<details><summary>Manual install, build from source, updating, uninstalling</summary>

#### Manual install (Windows release bundle)

Download `catclip_windows_amd64.zip` from releases.

```powershell
$InstallRoot = Join-Path $env:LOCALAPPDATA "Programs\catclip"
$BinDir = Join-Path $InstallRoot "bin"
$ShareDir = Join-Path $InstallRoot "share\catclip"
$ToolsDir = Join-Path $ShareDir "bin"

New-Item -ItemType Directory -Force -Path $BinDir, $ToolsDir | Out-Null
Expand-Archive -LiteralPath .\catclip_windows_amd64.zip -DestinationPath .
Copy-Item .\catclip.exe (Join-Path $BinDir "catclip.exe") -Force
Remove-Item (Join-Path $BinDir "catclip-tree.exe") -Force -ErrorAction SilentlyContinue
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
install -m 755 catclip "$PREFIX/bin/"
rm -f "$PREFIX/bin/catclip-tree"
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

To update, re-run the install one-liner — it always fetches the latest release.

Uninstall (Homebrew):
```bash
brew uninstall catclip
```

Uninstall (macOS / Linux):
```bash
curl -fsSL https://raw.githubusercontent.com/tigreau/catclip/main/uninstall.sh | bash
```

Uninstall (Windows PowerShell):
```powershell
irm https://raw.githubusercontent.com/tigreau/catclip/main/uninstall.ps1 | iex
```

For a checkout-local install, run `./uninstall.sh` from the repo root instead.

</details>

---

## Configuration

catclip skips `.gitignored` paths and paths matched by `.hiss` (the ignore config at `~/.config/catclip/.hiss`). Created automatically on first run.

```bash
catclip --hiss             # edit ignore rules
catclip --hiss-reset       # restore defaults
catclip src/generated      # read this exact ignored folder
catclip src --no-ignore    # read visible and ignored text files under src
```

An exact ignored file or folder can be named directly. Ignore rules nested
inside an explicitly named folder still apply. Use `--no-ignore` when you want
to disable `.gitignore` and `.hiss` filtering throughout the selected targets.

---

## Contributing

PRs welcome! Keep changes portable across macOS, Linux, and Windows.
