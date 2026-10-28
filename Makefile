# Root Makefile — thin wrappers over go/pnpm.
.PHONY: help dev desktop-dev desktop-build fmt vet test agent-host agent-host-release clean

help:
	@grep -E '^[a-zA-Z_-]+:' Makefile | cut -d: -f1 | sort

# Fast loop: compile the sidecar + the Electron main/preload only, then
# run Vite and Electron. No renderer bundle, no packaging.
dev: agent-host
	cd desktop-app && pnpm install --prefer-offline && pnpm build:electron && pnpm dev

# Build the sidecar first, then run the Electron UI against Vite.
desktop-dev: dev

# Release: sidecar + renderer + electron packaging.
desktop: clean agent-host-release
	cd desktop-app && pnpm install --prefer-offline && pnpm build

agent-host:
	cd core && go build -o bin/agent-host ./cmd/agent-host

agent-host-release:
	cd core && go build -trimpath -ldflags "-s -w" -o bin/agent-host ./cmd/agent-host

fmt:
	cd core && gofmt -l .

vet:
	cd core && go vet ./...

test:
	cd core && go test ./...

clean:
	rm -rf core/bin desktop-app/dist desktop-app/dist-electron desktop-app/release
