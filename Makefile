# Root Makefile — thin wrappers over cargo/pnpm.
.PHONY: help desktop-dev desktop-build fmt clippy test agent-host agent-host-release clean

help:
	@grep -E '^[a-zA-Z_-]+:' Makefile | cut -d: -f1 | sort

# Build the sidecar first, then run the Electron UI against Vite.
desktop-dev: agent-host
	cd desktop-app && pnpm install --prefer-offline && pnpm dev

# Release: sidecar + renderer + electron packaging.
desktop-build: agent-host-release
	cd desktop-app && pnpm install --prefer-offline && pnpm build

agent-host-release:
	cargo build --release -p agent-host

fmt:
	cargo fmt --all

clippy:
	cargo clippy --all-targets -- -D warnings

test:
	cargo test --workspace

clean:
	cargo clean
	rm -rf desktop-app/dist desktop-app/dist-electron desktop-app/release
