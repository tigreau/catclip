.PHONY: dev test run clean release-local release-clean catclip

BIN_DIR := bin
DIST_DIR := dist

UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# Pinned versions
RG_V := 14.1.1
FZF_V := 0.71.0
FZF_TAG := v$(FZF_V)

# Platform-specific asset URLs for manual install hints.
ifeq ($(UNAME_S),Darwin)
	ifeq ($(UNAME_M),arm64)
		RG_URL := https://github.com/BurntSushi/ripgrep/releases/download/$(RG_V)/ripgrep-$(RG_V)-aarch64-apple-darwin.tar.gz
		FZF_URL := https://github.com/junegunn/fzf/releases/download/$(FZF_TAG)/fzf-$(FZF_V)-darwin_arm64.tar.gz
	else
		RG_URL := https://github.com/BurntSushi/ripgrep/releases/download/$(RG_V)/ripgrep-$(RG_V)-x86_64-apple-darwin.tar.gz
		FZF_URL := https://github.com/junegunn/fzf/releases/download/$(FZF_TAG)/fzf-$(FZF_V)-darwin_amd64.tar.gz
	endif
else ifeq ($(UNAME_S),Linux)
	ifeq ($(UNAME_M),aarch64)
		RG_URL := https://github.com/BurntSushi/ripgrep/releases/download/$(RG_V)/ripgrep-$(RG_V)-aarch64-unknown-linux-gnu.tar.gz
		FZF_URL := https://github.com/junegunn/fzf/releases/download/$(FZF_TAG)/fzf-$(FZF_V)-linux_arm64.tar.gz
	else
		RG_URL := https://github.com/BurntSushi/ripgrep/releases/download/$(RG_V)/ripgrep-$(RG_V)-x86_64-unknown-linux-musl.tar.gz
		FZF_URL := https://github.com/junegunn/fzf/releases/download/$(FZF_TAG)/fzf-$(FZF_V)-linux_amd64.tar.gz
	endif
endif

# make dev — setup local toolchain and build catclip for development.
# If the system tools are missing or too old, print a copy-pasteable manual
# install command for the pinned local version.
dev: $(BIN_DIR)/rg $(BIN_DIR)/fzf catclip
	@printf '\nDev toolchain ready in ./%s/\n' "$(BIN_DIR)"
	@printf 'catclip uses these scoped binaries to ensure feature compatibility.\n\n'
	@printf 'Run locally:\n'
	@printf '  ./catclip --help\n'

catclip:
	@go build -o catclip ./cmd/catclip

$(BIN_DIR)/rg:
	@mkdir -p $(BIN_DIR)
	@rg_path=$$(command -v rg 2>/dev/null); \
	rg_detected="not found"; \
	capable=0; \
	if [ -n "$$rg_path" ]; then \
		rg_detected=$$("$$rg_path" --version 2>/dev/null | head -n 1 || printf 'not found'); \
		capable=1; \
		"$$rg_path" --files --hidden -0 . >/dev/null 2>&1 || capable=0; \
		tmp=$$(mktemp); \
		printf 'catclip-rg-check\n' > "$$tmp"; \
		"$$rg_path" --color=never --no-messages --files-with-matches -0 -m 1 -e 'catclip-rg-check' -- "$$tmp" >/dev/null 2>&1 || capable=0; \
		"$$rg_path" --pcre2 -e 'a' /dev/null >/dev/null 2>&1; \
		if [ $$? -eq 2 ]; then capable=0; fi; \
		rm -f "$$tmp"; \
	fi; \
	if [ $$capable -eq 1 ]; then \
		ln -sf "$$rg_path" $(BIN_DIR)/rg; \
		printf '  rg -> %s\n' "$$rg_path"; \
	else \
		printf 'Error: rg too old or missing PCRE2 support.\n\n' >&2; \
		printf 'Required: ripgrep >= %s with PCRE2\n' "$(RG_V)" >&2; \
		printf 'Detected: %s\n\n' "$$rg_detected" >&2; \
		printf 'Install local pinned version:\n' >&2; \
		printf '  curl -fL '\''%s'\'' | tar -xz && mv ripgrep-*/rg %s/rg && rm -rf ripgrep-*\n' "$(RG_URL)" "$(BIN_DIR)" >&2; \
		exit 1; \
	fi

$(BIN_DIR)/fzf:
	@mkdir -p $(BIN_DIR)
	@fzf_path=$$(command -v fzf 2>/dev/null); \
	fzf_detected="not found"; \
	capable=0; \
	if [ -n "$$fzf_path" ]; then \
		fzf_detected=$$("$$fzf_path" --version 2>/dev/null | head -n 1 || printf 'not found'); \
		capable=1; \
		printf 'a\n' | "$$fzf_path" --info=inline-right --filter a >/dev/null 2>&1 || capable=0; \
		printf 'a\n' | "$$fzf_path" --bind 'multi:refresh-preview' --filter a >/dev/null 2>&1 || capable=0; \
	fi; \
	if [ $$capable -eq 1 ]; then \
		ln -sf "$$fzf_path" $(BIN_DIR)/fzf; \
		printf '  fzf -> %s\n' "$$fzf_path"; \
	else \
		printf 'Error: fzf too old or missing required feature support.\n\n' >&2; \
		printf 'Required: fzf >= %s\n' "$(FZF_V)" >&2; \
		printf 'Detected: %s\n\n' "$$fzf_detected" >&2; \
		printf 'Install local pinned version:\n' >&2; \
		printf '  curl -fL '\''%s'\'' | tar -xz -C %s\n' "$(FZF_URL)" "$(BIN_DIR)" >&2; \
		exit 1; \
	fi

RIPGREP_VERSION := 14.1.1
FZF_VERSION := 0.71.0
RELEASE_PLATFORM ?= all
ifneq ($(strip $(PLATFORM)),)
RELEASE_PLATFORM := $(PLATFORM)
endif
VALID_RELEASE_PLATFORMS := all darwin linux windows

ifneq ($(filter release-local,$(MAKECMDGOALS)),)
ifeq ($(filter $(RELEASE_PLATFORM),$(VALID_RELEASE_PLATFORMS)),)
$(error RELEASE_PLATFORM must be one of: $(VALID_RELEASE_PLATFORMS))
endif
endif

define build_release
	@RIPGREP_VERSION=$(RIPGREP_VERSION) FZF_VERSION=$(FZF_VERSION) DIST_DIR=$(DIST_DIR) \
		scripts/build-release.sh $(1)
endef

test: dev
	go test ./...

run: dev
	go run ./cmd/catclip $(ARGS)

clean:
	rm -rf $(BIN_DIR)

# make release-local [PLATFORM=all|darwin|linux|windows] — cross-build catclip for every platform
# the CI workflow ships, bundle rg/fzf, and pack tar.gz/zip into dist/.
# Mirrors .github/workflows/release.yml so artifacts match the release.
release-local:
	@mkdir -p $(DIST_DIR)
	@printf 'Building local release artifacts for PLATFORM=%s\n' "$(RELEASE_PLATFORM)"
ifneq ($(filter $(RELEASE_PLATFORM),all darwin),)
	$(call build_release,darwin amd64 x86_64-apple-darwin tar.gz darwin_amd64 tar.gz tar "")
	$(call build_release,darwin arm64 aarch64-apple-darwin tar.gz darwin_arm64 tar.gz tar "")
endif
ifneq ($(filter $(RELEASE_PLATFORM),all linux),)
	$(call build_release,linux amd64 x86_64-unknown-linux-musl tar.gz linux_amd64 tar.gz tar "")
	$(call build_release,linux arm64 aarch64-unknown-linux-gnu tar.gz linux_arm64 tar.gz tar "")
endif
ifneq ($(filter $(RELEASE_PLATFORM),all windows),)
	$(call build_release,windows amd64 x86_64-pc-windows-msvc zip windows_amd64 zip zip ".exe")
endif
	@printf '\nLocal release artifacts:\n'
	@ls -lh $(DIST_DIR)/catclip_$(if $(filter all,$(RELEASE_PLATFORM)),*,$(RELEASE_PLATFORM)_*)

release-clean:
	rm -rf $(DIST_DIR)
