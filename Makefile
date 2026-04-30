.PHONY: dev dev-check test run clean release-local release-clean

BIN_DIR := bin
DIST_DIR := dist
RIPGREP_VERSION := 14.1.1
FZF_VERSION := 0.71.0

# make dev — one-time setup to symlink system rg/fzf into bin/ for go run/test.
dev: $(BIN_DIR)/rg $(BIN_DIR)/fzf
	@printf 'Dev tools ready in %s/\n' "$(BIN_DIR)"

$(BIN_DIR)/rg:
	@mkdir -p $(BIN_DIR)
	@rg_path=$$(command -v rg 2>/dev/null) || { printf 'Error: rg not found on PATH.\n  Install ripgrep first.\n' >&2; exit 1; }; \
	printf 'Checking rg capabilities...\n'; \
	"$$rg_path" --files --hidden -0 . >/dev/null 2>&1 || { printf 'Error: rg too old — missing --files --hidden -0 support.\n  Upgrade ripgrep.\n' >&2; exit 1; }; \
	tmp=$$(mktemp); printf 'catclip-rg-check\n' > "$$tmp"; \
	"$$rg_path" --color=never --no-messages --files-with-matches -0 -m 1 -e 'catclip-rg-check' -- "$$tmp" >/dev/null 2>&1 || { rm -f "$$tmp"; printf 'Error: rg too old — missing --files-with-matches -0 support.\n  Upgrade ripgrep.\n' >&2; exit 1; }; \
	rm -f "$$tmp"; \
	ln -sf "$$rg_path" $(BIN_DIR)/rg; \
	printf '  rg -> %s\n' "$$rg_path"

$(BIN_DIR)/fzf:
	@mkdir -p $(BIN_DIR)
	@fzf_path=$$(command -v fzf 2>/dev/null) || { printf 'Error: fzf not found on PATH.\n  Install fzf first.\n' >&2; exit 1; }; \
	printf 'Checking fzf capabilities...\n'; \
	printf 'a\n' | "$$fzf_path" --info=inline-right --filter a >/dev/null 2>&1 || { printf 'Error: fzf too old — missing --info=inline-right support.\n  Upgrade fzf.\n' >&2; exit 1; }; \
	ln -sf "$$fzf_path" $(BIN_DIR)/fzf; \
	printf '  fzf -> %s\n' "$$fzf_path"

test: dev
	go test ./...

run: dev
	go run ./cmd/catclip $(ARGS)

clean:
	rm -rf $(BIN_DIR)

# make release-local — cross-build catclip + catclip-tree for every platform
# the CI workflow ships, bundle rg/fzf, and pack tar.gz/zip into dist/.
# Mirrors .github/workflows/release.yml so artifacts match the release.
release-local:
	@mkdir -p $(DIST_DIR)
	@RIPGREP_VERSION=$(RIPGREP_VERSION) FZF_VERSION=$(FZF_VERSION) DIST_DIR=$(DIST_DIR) \
		scripts/build-release.sh darwin amd64 x86_64-apple-darwin tar.gz darwin_amd64 tar.gz tar ""
	@RIPGREP_VERSION=$(RIPGREP_VERSION) FZF_VERSION=$(FZF_VERSION) DIST_DIR=$(DIST_DIR) \
		scripts/build-release.sh darwin arm64 aarch64-apple-darwin tar.gz darwin_arm64 tar.gz tar ""
	@RIPGREP_VERSION=$(RIPGREP_VERSION) FZF_VERSION=$(FZF_VERSION) DIST_DIR=$(DIST_DIR) \
		scripts/build-release.sh linux amd64 x86_64-unknown-linux-musl tar.gz linux_amd64 tar.gz tar ""
	@RIPGREP_VERSION=$(RIPGREP_VERSION) FZF_VERSION=$(FZF_VERSION) DIST_DIR=$(DIST_DIR) \
		scripts/build-release.sh linux arm64 aarch64-unknown-linux-gnu tar.gz linux_arm64 tar.gz tar ""
	@RIPGREP_VERSION=$(RIPGREP_VERSION) FZF_VERSION=$(FZF_VERSION) DIST_DIR=$(DIST_DIR) \
		scripts/build-release.sh windows amd64 x86_64-pc-windows-msvc zip windows_amd64 zip zip ".exe"
	@printf '\nLocal release artifacts:\n'
	@ls -lh $(DIST_DIR)/catclip_*

release-clean:
	rm -rf $(DIST_DIR)
