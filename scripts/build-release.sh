#!/usr/bin/env bash
# Build one cross-platform catclip release bundle (Go binaries + bundled
# rg/fzf), packed as tar.gz or zip. Mirrors .github/workflows/release.yml
# so locally produced bundles match what CI ships.
#
# Usage:
#   scripts/build-release.sh GOOS GOARCH RG_TRIPLE RG_EXT FZF_TARGET FZF_EXT ARCHIVE_KIND BIN_EXT
#
# Env (with defaults):
#   RIPGREP_VERSION=14.1.1
#   FZF_VERSION=0.71.0
#   DIST_DIR=dist
#   CATCLIP_VERSION=$(cat VERSION)

set -euo pipefail

if [ "$#" -ne 8 ]; then
  printf 'Usage: %s GOOS GOARCH RG_TRIPLE RG_EXT FZF_TARGET FZF_EXT ARCHIVE_KIND BIN_EXT\n' "$0" >&2
  exit 2
fi

goos="$1"
goarch="$2"
rg_triple="$3"
rg_ext="$4"
fzf_target="$5"
fzf_ext="$6"
archive_kind="$7"
bin_ext="$8"

RIPGREP_VERSION="${RIPGREP_VERSION:-14.1.1}"
FZF_VERSION="${FZF_VERSION:-0.71.0}"
DIST_DIR="${DIST_DIR:-dist}"
CATCLIP_VERSION="${CATCLIP_VERSION:-$(cat VERSION)}"

artifact_base="catclip_${goos}_${goarch}"
case "$archive_kind" in
  tar)  artifact="${DIST_DIR}/${artifact_base}.tar.gz" ;;
  zip)  artifact="${DIST_DIR}/${artifact_base}.zip" ;;
  *)    printf 'Error: unknown archive_kind %q (want tar or zip)\n' "$archive_kind" >&2; exit 2 ;;
esac

stage="${DIST_DIR}/.stage_${goos}_${goarch}"
work="${DIST_DIR}/.work_${goos}_${goarch}"
mkdir -p "$DIST_DIR"
rm -rf "$stage" "$work"
mkdir -p "$stage/bin" "$work"

printf '[%s/%s] building catclip + catclip-tree\n' "$goos" "$goarch"
GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
  go build -trimpath -ldflags '-s -w' -o "${stage}/catclip${bin_ext}" ./cmd/catclip
GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
  go build -trimpath -ldflags '-s -w' -o "${stage}/catclip-tree${bin_ext}" ./cmd/catclip-tree

printf '[%s/%s] downloading rg %s\n' "$goos" "$goarch" "$RIPGREP_VERSION"
rg_dir="ripgrep-${RIPGREP_VERSION}-${rg_triple}"
rg_archive="${rg_dir}.${rg_ext}"
curl -fsSL --retry 3 --retry-delay 2 "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/${rg_archive}" \
  -o "${work}/${rg_archive}"
if [ "$rg_ext" = "zip" ]; then
  unzip -q "${work}/${rg_archive}" -d "$work"
  cp "${work}/${rg_dir}/rg.exe" "${stage}/bin/rg.exe"
else
  tar -xzf "${work}/${rg_archive}" -C "$work"
  cp "${work}/${rg_dir}/rg" "${stage}/bin/rg"
  chmod +x "${stage}/bin/rg"
fi

printf '[%s/%s] downloading fzf %s\n' "$goos" "$goarch" "$FZF_VERSION"
fzf_archive="fzf-${FZF_VERSION}-${fzf_target}.${fzf_ext}"
curl -fsSL --retry 3 --retry-delay 2 "https://github.com/junegunn/fzf/releases/download/v${FZF_VERSION}/${fzf_archive}" \
  -o "${work}/${fzf_archive}"
mkdir -p "${work}/fzf-extract"
if [ "$fzf_ext" = "zip" ]; then
  unzip -q "${work}/${fzf_archive}" -d "${work}/fzf-extract"
  cp "${work}/fzf-extract/fzf.exe" "${stage}/bin/fzf.exe"
else
  tar -xzf "${work}/${fzf_archive}" -C "${work}/fzf-extract"
  cp "${work}/fzf-extract/fzf" "${stage}/bin/fzf"
  chmod +x "${stage}/bin/fzf"
fi

printf '%s\n' "$CATCLIP_VERSION" > "${stage}/VERSION"

printf '[%s/%s] packing %s\n' "$goos" "$goarch" "$artifact"
rm -f "$artifact"
if [ "$archive_kind" = "zip" ]; then
  (cd "$stage" && zip -qr "$OLDPWD/$artifact" .)
else
  tar -C "$stage" -czf "$artifact" .
fi

rm -rf "$stage" "$work"
printf '[%s/%s] done -> %s\n' "$goos" "$goarch" "$artifact"
