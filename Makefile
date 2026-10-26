# Root Makefile — thin wrappers over cargo/tauri/pnpm.
.PHONY: help desktop-dev desktop-build fmt clippy test test-e2e clean

help:
	@grep -E '^[a-zA-Z_-]+:' Makefile | cut -d: -f1 | sort

# Run the desktop app in dev mode (Tauri dev server + Rust core).
desktop-dev:
	cd desktop-app && pnpm tauri dev

# Build the desktop app (bundles for the host OS).
desktop-build:
	cd desktop-app && pnpm tauri build

# Format and lint the whole workspace.
fmt:
	cargo fmt --all

clippy:
	cargo clippy --all-targets -- -D warnings

# Unit + integration tests for the core (and everything else in the workspace).
test:
	cargo test --workspace

# UI end-to-end tests (Playwright).
test-e2e:
	cd desktop-app && pnpm test:e2e

clean:
	cargo clean
