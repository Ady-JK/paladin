# Paladin build commands. Works with GNU make on macOS, Linux and
# Windows (cmd.exe or a POSIX shell): recipes only use `cd dir && cmd`,
# and file removal goes through node instead of rm.
#
#   make app      clean, then a fully optimised sidecar + packaged app
#   make app-dev  quick debug sidecar + Electron against the Vite server
#   make test     Go tests, UI typecheck and the Playwright UI suite
#   make fmt      format Go and UI sources
#   make lint     golangci-lint, deadcode and eslint/knip/prettier
#   make clean    remove every build output

.PHONY: app app-dev test fmt lint clean
.DEFAULT_GOAL := app-dev

ifeq ($(OS),Windows_NT)
  EXE := .exe
else
  EXE :=
endif

AGENT_HOST := bin/agent-host$(EXE)

# Go tools installed with `go install` live in GOPATH/bin, which is not
# always on PATH. Override with e.g. `make lint GOLANGCI_LINT=golangci-lint`.
GO_TOOLS_BIN := $(shell go env GOPATH)/bin
GOLANGCI_LINT ?= $(GO_TOOLS_BIN)/golangci-lint
DEADCODE ?= $(GO_TOOLS_BIN)/deadcode

# Release builds are static and stripped (no cgo is used anywhere).
app: export CGO_ENABLED := 0

app:
	$(MAKE) clean
	cd core && go build -trimpath -buildvcs=false -ldflags "-s -w" -o $(AGENT_HOST) ./cmd/agent-host
	cd desktop-app && pnpm install --frozen-lockfile --prefer-offline && pnpm build

app-dev:
	cd core && go build -o $(AGENT_HOST) ./cmd/agent-host
	cd desktop-app && pnpm install --prefer-offline && pnpm build:electron && pnpm dev

test:
	cd core && go test ./...
	cd desktop-app && pnpm typecheck && pnpm exec tsc --noEmit -p tsconfig.electron.json && pnpm exec playwright test

fmt:
	cd core && $(GOLANGCI_LINT) fmt ./...
	cd desktop-app && pnpm -s fmt

lint:
	cd core && $(GOLANGCI_LINT) run ./... && $(DEADCODE) -test ./...
	cd desktop-app && pnpm -s lint

clean:
	node -e "for (const p of process.argv.slice(1)) require('fs').rmSync(p, { recursive: true, force: true })" core/bin desktop-app/dist desktop-app/dist-electron desktop-app/release desktop-app/test-results
