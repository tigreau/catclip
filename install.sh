#!/usr/bin/env bash
set -Eeuo pipefail

# install.sh prefers a local source build when run from a cloned checkout and
# otherwise falls back to installing a prebuilt release bundle. Packaged
# installs always carry private rg/fzf binaries under share/catclip/bin; runtime
# does not fall back to user PATH copies.
# Native Windows installs use install.ps1 instead of stretching this Bash
# entrypoint across shells.
# Published Linux release bundles are expected to target an Ubuntu LTS baseline
# so catclip, rg, and fzf keep broad glibc compatibility.

PROGRAM_NAME="catclip"
TREE_PROGRAM_NAME="catclip-tree"
RELEASE_BASE_URL="${CATCLIP_RELEASE_BASE_URL:-https://github.com/tigreau/catclip/releases}"
INSTALL_VERSION="${CATCLIP_INSTALL_VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="$PREFIX/bin"
SHARE_DIR="$PREFIX/share/catclip"
TOOLS_DIR="$SHARE_DIR/bin"

if [[ -t 1 && "${TERM:-}" != "dumb" ]]; then
  RESET=$'\033[0m'
  BOLD=$'\033[1m'
  GREEN=$'\033[32m'
  CYAN=$'\033[36m'
  YELLOW=$'\033[33m'
  RED=$'\033[31m'
else
  RESET=''
  BOLD=''
  GREEN=''
  CYAN=''
  YELLOW=''
  RED=''
fi

die() {
  trap - ERR
  printf '%sError:%s %s\n' "$RED" "$RESET" "$*" >&2
  exit 1
}

on_unexpected_error() {
  local exit_code="$1"
  local line_no="$2"
  local cmd="$3"

  trap - ERR
  if [[ "$exit_code" -eq 0 ]]; then
    return 0
  fi

  if [[ "${CATCLIP_INSTALL_DEBUG:-0}" == "1" ]]; then
    printf '%sError:%s install.sh failed at line %s while running: %s\n' \
      "$RED" "$RESET" "$line_no" "$cmd" >&2
  else
    printf '%sError:%s installation failed.\n' "$RED" "$RESET" >&2
    printf '  Re-run with %sCATCLIP_INSTALL_DEBUG=1%s for more details.\n' \
      "$CYAN" "$RESET" >&2
  fi
  exit "$exit_code"
}

install_err_trap() {
  trap 'on_unexpected_error $? $LINENO "$BASH_COMMAND"' ERR
}

install_err_trap

note() {
  printf '%sNote:%s %s\n' "$YELLOW" "$RESET" "$*"
}

run_expected_failure_ok() {
  local status

  trap - ERR
  set +e
  "$@"
  status=$?
  set -e
  install_err_trap
  return "$status"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required"
}

need_go_for_source_build() {
  command -v go >/dev/null 2>&1 || die "Go is required when installing from a source checkout.
  If Go is installed only in your user environment, run ./install.sh without sudo and let the script use sudo only for the final file copy.
  Use Homebrew or the release installer if you do not want to build locally."

  local source_dir="$1"
  local required found

  required="$(required_go_version_from_source "$source_dir")"
  [[ -n "$required" ]] || return 0

  found="$(go env GOVERSION 2>/dev/null || true)"
  found="${found#go}"
  [[ -n "$found" ]] || return 0

  if ! go_version_gte "$found" "$required"; then
    die "Go $required or newer is required to build this checkout.
  Found Go $found.
  Upgrade Go, or use the published release installer instead of building from source."
  fi
}

required_go_version_from_source() {
  local source_dir="$1"
  local version_line

  version_line="$(awk '/^go[[:space:]]+/ { print $2; exit }' "$source_dir/go.mod" 2>/dev/null || true)"
  printf '%s\n' "$version_line"
}

go_version_gte() {
  local found="$1"
  local required="$2"
  local found_major found_minor required_major required_minor

  found="${found%%[^0-9.]*}"
  required="${required%%[^0-9.]*}"

  found_major="${found%%.*}"
  found_minor="${found#*.}"
  found_minor="${found_minor%%.*}"
  required_major="${required%%.*}"
  required_minor="${required#*.}"
  required_minor="${required_minor%%.*}"

  [[ -n "$found_major" && -n "$found_minor" && -n "$required_major" && -n "$required_minor" ]] || return 0

  if (( found_major > required_major )); then
    return 0
  fi
  if (( found_major < required_major )); then
    return 1
  fi
  (( found_minor >= required_minor ))
}

build_from_source_checkout() {
  local source_dir="$1"
  local binary_file="$2"

  (
    cd "$source_dir"
    go build -trimpath -o "$binary_file" ./cmd/catclip
  )
}

ensure_source_fzf_is_compatible() {
  local fzf_bin="$1"

  if ! printf 'a\n' | "$fzf_bin" --info=inline-right --filter a >/dev/null 2>&1; then
    die "Your local fzf is too old for catclip source installs.
  catclip's current picker UI requires an fzf build that supports --info=inline-right.
  Upgrade fzf to >= v0.71.0, or use the published release installer instead of building from source."
  fi

  if ! printf 'a\n' | "$fzf_bin" --bind 'multi:refresh-preview' --filter a >/dev/null 2>&1; then
    die "Your local fzf is too old for catclip source installs.
  catclip's multi-select behavior requires an fzf build that supports multi bind events.
  Upgrade fzf to >= v0.71.0, or use the published release installer instead of building from source."
  fi
}

ensure_source_rg_is_compatible() {
  local rg_bin="$1"
  local source_dir="$2"
  local tmp_file

  if ! "$rg_bin" --files --hidden -0 "$source_dir" >/dev/null 2>&1; then
    die "Your local ripgrep is too old for catclip source installs.
  catclip's current file discovery requires an rg build that supports --files with -0 output.
  Upgrade ripgrep to >= v14.1.1, or use the published release installer instead of building from source."
  fi

  tmp_file="$(mktemp)"
  printf 'catclip-rg-check\n' >"$tmp_file"
  if ! "$rg_bin" --color=never --no-messages --files-with-matches -0 -m 1 -e 'catclip-rg-check' -- "$tmp_file" >/dev/null 2>&1; then
    rm -f "$tmp_file"
    die "Your local ripgrep is too old for catclip source installs.
  catclip's current content filtering requires an rg build that supports --files-with-matches with -0 output.
  Upgrade ripgrep to >= v14.1.1, or use the published release installer instead of building from source."
  fi

  if ! "$rg_bin" --pcre2 -e 'a' /dev/null >/dev/null 2>&1; then
    # ripgrep exits with code 2 if the flag is unsupported/unavailable.
    if [[ $? -eq 2 ]]; then
      rm -f "$tmp_file"
      die "Your local ripgrep is too old for catclip source installs.
  catclip requires a ripgrep build with PCRE2 support.
  Upgrade ripgrep to >= v14.1.1, or use the published release installer instead of building from source."
    fi
  fi

  rm -f "$tmp_file"
}

resolve_bundled_tool_source_into() {
  local out_var="$1"
  local env_var="$2"
  local tool_name="$3"
  local override resolved

  override="${!env_var:-}"
  if [[ -n "$override" ]]; then
    if [[ "$override" == *"/"* ]]; then
      [[ -x "$override" ]] || die "$tool_name override at $override is not executable"
      printf -v "$out_var" '%s' "$override"
      return 0
    fi
    resolved="$(command -v "$override" || true)"
    [[ -n "$resolved" ]] || die "$tool_name override '$override' not found"
    printf -v "$out_var" '%s' "$resolved"
    return 0
  fi

  resolved="$(command -v "$tool_name" || true)"
  [[ -n "$resolved" ]] || die "'$tool_name' is required for local source installs because catclip packages a private bundled copy with every install.
  Install $tool_name first, or set $env_var to an executable path."
  printf -v "$out_var" '%s' "$resolved"
}

homebrew_manages_catclip() {
  if ! command -v brew >/dev/null 2>&1; then
    return 1
  fi

  brew list --versions "$PROGRAM_NAME" >/dev/null 2>&1 && return 0
  brew list --cask --versions "$PROGRAM_NAME" >/dev/null 2>&1 && return 0
  return 1
}

find_local_source_dir() {
  local script_path script_dir
  script_path="${BASH_SOURCE[0]:-}"
  [[ -n "$script_path" ]] || return 1
  [[ -f "$script_path" ]] || return 1
  script_dir="$(cd "$(dirname "$script_path")" 2>/dev/null && pwd)" || return 1

  # A cloned checkout contains the full Go module. Prefer building that exact
  # source tree so install.sh reflects the checked-out code instead of silently
  # replacing it with whatever release is current.
  if [[ -f "$script_dir/go.mod" && -f "$script_dir/main.go" && -f "$script_dir/VERSION" ]]; then
    printf '%s\n' "$script_dir"
    return 0
  fi

  return 1
}

warn_existing_target_install() {
  local target="$1"
  local version_file="$2"

  if [[ ! -e "$target" ]]; then
    return 0
  fi

  note "Existing catclip installation detected at $target."
  if [[ -f "$version_file" ]]; then
    note "Existing version metadata found at $version_file."
  fi
  note "This install will replace the direct-install binary in place and keep ~/.config/catclip/.hiss."
}

install_file() {
  local mode="$1"
  local src="$2"
  local dest="$3"
  local dest_dir
  dest_dir="$(dirname "$dest")"

  # Prefer a normal user-space install first. This keeps local prefixes like
  # /tmp or ~/.local fast and avoids tripping sudo in writable locations.
  if mkdir -p "$dest_dir" 2>/dev/null; then
    if install -m "$mode" "$src" "$dest" 2>/dev/null; then
      return
    fi
  fi

  if [[ -w "$dest_dir" ]]; then
    install -m "$mode" "$src" "$dest"
    return
  fi

  if ! command -v sudo >/dev/null 2>&1; then
    die "cannot write to $dest_dir; re-run with PREFIX=\"$HOME/.local\" or install sudo"
  fi

  sudo mkdir -p "$dest_dir"
  sudo install -m "$mode" "$src" "$dest"
}

remove_file_if_exists() {
  local path="$1"
  local parent_dir

  if [[ ! -e "$path" && ! -L "$path" ]]; then
    return
  fi
  if rm -f "$path" 2>/dev/null; then
    return
  fi

  parent_dir="$(dirname "$path")"
  if [[ -w "$parent_dir" ]]; then
    rm -f "$path"
    return
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    die "cannot remove stale $path; re-run with PREFIX=\"$HOME/.local\" or install sudo"
  fi
  sudo rm -f "$path"
}

download_file() {
  local url="$1"
  local dest="$2"

  if command -v curl >/dev/null 2>&1; then
    if run_expected_failure_ok curl -fsSL "$url" -o "$dest"; then
      return
    fi
    rm -f "$dest"
    die "failed to download $url
  The requested catclip release bundle may not be published yet.
  Try again later, or install from a cloned checkout with ./install.sh."
  fi
  if command -v wget >/dev/null 2>&1; then
    if run_expected_failure_ok wget -qO "$dest" "$url"; then
      return
    fi
    rm -f "$dest"
    die "failed to download $url
  The requested catclip release bundle may not be published yet.
  Try again later, or install from a cloned checkout with ./install.sh."
  fi
  die "curl or wget is required to download catclip"
}

try_download_file() {
  local url="$1"
  local dest="$2"

  if command -v curl >/dev/null 2>&1; then
    if run_expected_failure_ok curl -fsSL "$url" -o "$dest"; then
      return
    fi
    rm -f "$dest"
    return 1
  fi
  if command -v wget >/dev/null 2>&1; then
    if run_expected_failure_ok wget -qO "$dest" "$url"; then
      return
    fi
    rm -f "$dest"
    return 1
  fi
  return 1
}

normalize_os() {
  case "$(uname -s)" in
    Darwin) printf '%s\n' "darwin" ;;
    Linux) printf '%s\n' "linux" ;;
    MINGW*|MSYS*|CYGWIN*) die "native Windows installs use install.ps1; run the PowerShell installer instead." ;;
    *) die "unsupported operating system: $(uname -s)" ;;
  esac
}

normalize_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s\n' "amd64" ;;
    arm64|aarch64) printf '%s\n' "arm64" ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}

build_release_url() {
  local asset="$1"

  if [[ "$INSTALL_VERSION" == "latest" ]]; then
    printf '%s\n' "$RELEASE_BASE_URL/latest/download/$asset"
    return
  fi

  printf '%s\n' "$RELEASE_BASE_URL/download/$INSTALL_VERSION/$asset"
}

verify_checksum() {
  local asset_name="$1"
  local archive_path="$2"
  local checksums_path="$3"

  if [[ ! -f "$checksums_path" ]]; then
    printf '%sWarning:%s checksums file missing; skipping verification.\n' "$YELLOW" "$RESET" >&2
    return 0
  fi

  if command -v shasum >/dev/null 2>&1; then
    (
      cd "$(dirname "$archive_path")"
      grep "  $asset_name\$" "$checksums_path" | shasum -a 256 -c -
    ) >/dev/null
    return
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    (
      cd "$(dirname "$archive_path")"
      grep "  $asset_name\$" "$checksums_path" | sha256sum -c -
    ) >/dev/null
    return
  fi

  printf '%sWarning:%s no sha256 verifier found; skipping verification.\n' "$YELLOW" "$RESET" >&2
}

need_cmd tar
need_cmd install

OS_NAME="$(normalize_os)"
ARCH_NAME="$(normalize_arch)"
ASSET_NAME="${PROGRAM_NAME}_${OS_NAME}_${ARCH_NAME}.tar.gz"
CHECKSUMS_NAME="checksums.txt"

TMP_ROOT=''
cleanup() {
  if [[ -n "$TMP_ROOT" && -d "$TMP_ROOT" ]]; then
    rm -rf "$TMP_ROOT"
  fi
}
trap cleanup EXIT

printf '%sInstalling catclip...%s\n' "$BOLD" "$RESET"
printf 'Target:   %s%s/%s%s\n' "$CYAN" "$OS_NAME" "$ARCH_NAME" "$RESET"

if homebrew_manages_catclip; then
  die "catclip appears to be managed by Homebrew; use 'brew upgrade catclip' instead."
fi

warn_existing_target_install "$BIN_DIR/$PROGRAM_NAME" "$SHARE_DIR/VERSION"

TMP_ROOT="$(mktemp -d)"

if SOURCE_DIR="$(find_local_source_dir)"; then
  need_go_for_source_build "$SOURCE_DIR"

  printf 'Source:   %s%s%s\n' "$CYAN" "$SOURCE_DIR" "$RESET"
  note "Building from your local checkout so the installed binary matches the code you have checked out."
  note "This avoids replacing your in-progress source tree with the latest published release."
  note "Source installs require local Go, rg, and fzf once at install time so catclip can bundle private copies into the final install."

  VERSION_FILE="$SOURCE_DIR/VERSION"
  BINARY_FILE="$TMP_ROOT/$PROGRAM_NAME"
  RG_FILE="$TMP_ROOT/rg"
  FZF_FILE="$TMP_ROOT/fzf"
  VERSION="$(tr -d '\r' < "$VERSION_FILE" | head -n 1)"
  [[ -n "$VERSION" ]] || die "VERSION file is empty"
  RG_SOURCE=''
  FZF_SOURCE=''
  resolve_bundled_tool_source_into RG_SOURCE CATCLIP_RG rg
  resolve_bundled_tool_source_into FZF_SOURCE CATCLIP_FZF fzf
  ensure_source_rg_is_compatible "$RG_SOURCE" "$SOURCE_DIR"
  ensure_source_fzf_is_compatible "$FZF_SOURCE"
  install -m 755 "$RG_SOURCE" "$RG_FILE"
  install -m 755 "$FZF_SOURCE" "$FZF_FILE"

  printf 'Building %s%s%s from source\n' "$CYAN" "$PROGRAM_NAME" "$RESET"
  if ! run_expected_failure_ok build_from_source_checkout "$SOURCE_DIR" "$BINARY_FILE"; then
    die "failed to build catclip from the local source checkout.
  Ensure Go $(required_go_version_from_source "$SOURCE_DIR") or newer is installed, then try again."
  fi
else
  ARCHIVE_PATH="$TMP_ROOT/$ASSET_NAME"
  CHECKSUMS_PATH="$TMP_ROOT/$CHECKSUMS_NAME"

  printf 'Downloading %s%s%s\n' "$CYAN" "$ASSET_NAME" "$RESET"
  download_file "$(build_release_url "$ASSET_NAME")" "$ARCHIVE_PATH"

  if [[ "${CATCLIP_SKIP_VERIFY:-0}" != "1" ]]; then
    printf 'Downloading %s%s%s\n' "$CYAN" "$CHECKSUMS_NAME" "$RESET"
    if try_download_file "$(build_release_url "$CHECKSUMS_NAME")" "$CHECKSUMS_PATH"; then
      printf 'Verifying checksum...\n'
      verify_checksum "$ASSET_NAME" "$ARCHIVE_PATH" "$CHECKSUMS_PATH"
    else
      printf '%sWarning:%s failed to download %s; skipping verification.\n' "$YELLOW" "$RESET" "$(build_release_url "$CHECKSUMS_NAME")" >&2
    fi
  fi

  tar -xzf "$ARCHIVE_PATH" -C "$TMP_ROOT"

  VERSION_FILE="$TMP_ROOT/VERSION"
  BINARY_FILE="$TMP_ROOT/$PROGRAM_NAME"
  RG_FILE="$TMP_ROOT/bin/rg"
  FZF_FILE="$TMP_ROOT/bin/fzf"
  [[ -f "$VERSION_FILE" ]] || die "release archive is missing VERSION"
  [[ -f "$BINARY_FILE" ]] || die "release archive is missing $PROGRAM_NAME"
  [[ -f "$RG_FILE" ]] || die "release archive is missing bin/rg"
  [[ -f "$FZF_FILE" ]] || die "release archive is missing bin/fzf"

  VERSION="$(tr -d '\r' < "$VERSION_FILE" | head -n 1)"
  [[ -n "$VERSION" ]] || die "VERSION file is empty"
fi

install_file 755 "$BINARY_FILE" "$BIN_DIR/$PROGRAM_NAME"
remove_file_if_exists "$BIN_DIR/$TREE_PROGRAM_NAME"
install_file 644 "$VERSION_FILE" "$SHARE_DIR/VERSION"
install_file 755 "$RG_FILE" "$TOOLS_DIR/rg"
install_file 755 "$FZF_FILE" "$TOOLS_DIR/fzf"

printf '%sDone.%s\n' "$GREEN" "$RESET"
printf '  Binary:  %s%s%s\n' "$CYAN" "$BIN_DIR/$PROGRAM_NAME" "$RESET"
printf '  Version: %s%s%s\n' "$CYAN" "$VERSION" "$RESET"
printf '  rg:      %s%s%s\n' "$CYAN" "$TOOLS_DIR/rg" "$RESET"
printf '  fzf:     %s%s%s\n' "$CYAN" "$TOOLS_DIR/fzf" "$RESET"
printf '  Config:  %s%s%s\n' "$CYAN" '~/.config/catclip/.hiss' "$RESET"

if [[ "$BIN_DIR" == "$HOME/.local/bin" ]]; then
  case ":${PATH}:" in
    *":$BIN_DIR:"*) ;;
    *) printf '%sNote:%s add %s to PATH if it is not already exported.\n' "$YELLOW" "$RESET" "$BIN_DIR" ;;
  esac
fi
