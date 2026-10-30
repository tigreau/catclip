#!/usr/bin/env bash
set -euo pipefail

# Exercise the real installer using a local release fixture and a private prefix.
# Run with /bin/bash on macOS to cover the system's Bash 3.2, not Homebrew Bash.
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_SCRIPT="${1:-$REPO_DIR/install.sh}"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "$TEST_ROOT"' EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  cat "$TEST_ROOT/stdout" "$TEST_ROOT/stderr" >&2
  exit 1
}

mkdir -p "$TEST_ROOT/payload/bin" "$TEST_ROOT/releases/latest/download" \
  "$TEST_ROOT/stubs" "$TEST_ROOT/checkout"

printf '#!/bin/sh\nprintf "catclip 0.0.0\\n"\n' > "$TEST_ROOT/payload/catclip"
printf '#!/bin/sh\nexit 0\n' > "$TEST_ROOT/payload/bin/rg"
cp "$TEST_ROOT/payload/bin/rg" "$TEST_ROOT/payload/bin/fzf"
chmod +x "$TEST_ROOT/payload/catclip" "$TEST_ROOT/payload/bin/"*
printf '0.0.0\n' > "$TEST_ROOT/payload/VERSION"

# Do not consult the host's Homebrew installation or compile the real project.
printf '#!/bin/sh\nexit 1\n' > "$TEST_ROOT/stubs/brew"
cat > "$TEST_ROOT/stubs/go" <<'EOF'
#!/bin/sh
case "$1" in
  env) printf 'go1.26.0\n' ;;
  build)
    shift
    while [ "$#" -gt 0 ]; do
      if [ "$1" = '-o' ]; then
        cp "$CATCLIP_TEST_BINARY" "$2"
        exit $?
      fi
      shift
    done
    exit 1
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$TEST_ROOT/stubs/"*

case "$(uname -s)" in
  Darwin) OS_NAME=darwin ;;
  Linux) OS_NAME=linux ;;
  *) printf 'Unsupported test platform\n' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH_NAME=amd64 ;;
  arm64|aarch64) ARCH_NAME=arm64 ;;
  *) printf 'Unsupported test architecture\n' >&2; exit 1 ;;
esac
ASSET_NAME="catclip_${OS_NAME}_${ARCH_NAME}.tar.gz"
RELEASE_DIR="$TEST_ROOT/releases/latest/download"
tar -czf "$RELEASE_DIR/$ASSET_NAME" -C "$TEST_ROOT/payload" catclip VERSION bin
(
  cd "$RELEASE_DIR"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$ASSET_NAME"
  else
    sha256sum "$ASSET_NAME"
  fi
) > "$RELEASE_DIR/checksums.txt"

export PATH="$TEST_ROOT/stubs:$PATH"
export PREFIX="$TEST_ROOT/prefix"
export CATCLIP_RELEASE_BASE_URL="file://$TEST_ROOT/releases"
export CATCLIP_INSTALL_VERSION=latest CATCLIP_INSTALL_DEBUG=0 CATCLIP_SKIP_VERIFY=0
export CATCLIP_TEST_BINARY="$TEST_ROOT/payload/catclip"
export CATCLIP_RG="$TEST_ROOT/payload/bin/rg" CATCLIP_FZF="$TEST_ROOT/payload/bin/fzf"

cp "$INSTALL_SCRIPT" "$TEST_ROOT/checkout/install.sh"
printf 'module fixture\n\ngo 1.26\n' > "$TEST_ROOT/checkout/go.mod"
touch "$TEST_ROOT/checkout/main.go"
cp "$TEST_ROOT/payload/VERSION" "$TEST_ROOT/checkout/VERSION"
# A piped install must select the release even when cwd is a source checkout.
cd "$TEST_ROOT/checkout"

assert_installed() {
  local method="$1"
  [[ ! -s "$TEST_ROOT/stderr" ]] || fail 'successful install printed an error'
  grep -q '^Done\.$' "$TEST_ROOT/stdout" || fail 'missing completion message'
  [[ "$("$PREFIX/bin/catclip" --version)" == 'catclip 0.0.0' ]] || fail 'wrong binary'
  [[ "$(cat "$PREFIX/share/catclip/INSTALL_METHOD")" == "$method" ]] || fail 'wrong install method'
  [[ -x "$PREFIX/share/catclip/bin/rg" && -x "$PREFIX/share/catclip/bin/fzf" ]] || fail 'missing bundled tools'
}

# Use a real pipe, matching curl | bash, and assert stderr as well as exit status.
cat install.sh | "$BASH" > "$TEST_ROOT/stdout" 2> "$TEST_ROOT/stderr"
assert_installed direct-release
printf 'PASS: fresh piped release install\n'

# Simulate replacing a source installation, without uninstalling it first.
printf 'source\n' > "$PREFIX/share/catclip/INSTALL_METHOD"
cat install.sh | "$BASH" > "$TEST_ROOT/stdout" 2> "$TEST_ROOT/stderr"
assert_installed direct-release
grep -q 'Existing catclip installation' "$TEST_ROOT/stdout" || fail 'replacement was not detected'
printf 'PASS: piped release replaces existing source install\n'

"$BASH" install.sh > "$TEST_ROOT/stdout" 2> "$TEST_ROOT/stderr"
assert_installed source
printf 'PASS: local checkout still selects a source build\n'

cp install.sh "$TEST_ROOT/install.sh"
"$BASH" "$TEST_ROOT/install.sh" > "$TEST_ROOT/stdout" 2> "$TEST_ROOT/stderr"
assert_installed direct-release
printf 'PASS: standalone installer still selects a release\n'

# An actual error after source detection must still stop the installer.
export PREFIX="$TEST_ROOT/bad-checksum-prefix"
printf '%064d  %s\n' 0 "$ASSET_NAME" > "$RELEASE_DIR/checksums.txt"
if cat install.sh | "$BASH" > "$TEST_ROOT/stdout" 2> "$TEST_ROOT/stderr"; then
  fail 'invalid checksum was accepted'
fi
grep -q 'Error: installation failed' "$TEST_ROOT/stderr" || fail 'unexpected-error handler was disabled'
[[ ! -e "$PREFIX/bin/catclip" ]] || fail 'installed a bundle with an invalid checksum'
printf 'PASS: checksum failure still aborts with an error\n'

rm "$RELEASE_DIR/$ASSET_NAME"
if cat install.sh | "$BASH" > "$TEST_ROOT/stdout" 2> "$TEST_ROOT/stderr"; then
  fail 'missing release was accepted'
fi
grep -q 'Error: failed to download' "$TEST_ROOT/stderr" || fail 'missing download error'
[[ ! -e "$PREFIX/bin/catclip" ]] || fail 'installed a missing release'
printf 'PASS: download failure still aborts with an error\n'
