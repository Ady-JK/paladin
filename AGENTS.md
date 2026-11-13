# AGENTS.md

Paladin is an AI coding agent desktop app: a Go core (the "agent-host" sidecar) that
runs agent turns, tools, MCP servers and sandboxing, plus an Electron desktop UI. The
two halves talk over **one JSON object per line** on stdio — no HTTP, no SSE, no
WebSocket. `HOW-IT-WORKS.md` is the full architecture doc (830 lines, accurate and
worth reading before large changes); `desktop-app/PROTOCOL.md` is the wire spec.

## Layout

| Path | What lives there |
|---|---|
| `core/cmd/agent-host/` | Sidecar entry point, JSONL dispatcher and the RPC table (`methods.go`) |
| `core/internal/agent/` | The per-turn agent loop, retries, compaction, tool batching |
| `core/internal/runtime/` | Turn hub (scheduling), sessions, subagents, MCP wiring, undo, worktrees |
| `core/internal/tools/` | Built-in tools (`read/write/edit/shell/grep/glob/list/ask/browser/web_fetch/visual/todo`) |
| `core/internal/inference/` | Model providers and streaming |
| `core/internal/permission/`, `sandbox/`, `redact/` | Approval gate, OS sandbox, stored-key masking |
| `core/internal/transcript/`, `session/`, `memory/`, `atomicfile/` | Persistence |
| `core/internal/{hooks,lsp,mcp,skills,extension,commands,prompt}/` | Plug-in surfaces |
| `desktop-app/electron/` | Electron main + preload: spawns the sidecar, IPC bridge, `WebContentsView`s (web tabs + local browser), sandboxed `plugin://` host |
| `desktop-app/src/core/` | `api.ts` (the single bridge seam), `state.ts`, `dom.ts`, `format.ts`, `types.ts` |
| `desktop-app/src/features/` | 50 feature modules, one concern each (`chat`, `live`, `turnView`, `sessions`, `sidePane`, `web`, `pluginPanels`, …) |
| `desktop-app/src/styles/` | Tailwind v4 theme (`base.css`) + one sheet per feature |
| `desktop-app/e2e/` | 60 Playwright specs + `paladin-stub.js` (a fake `window.paladin`) |
| `docs/PLUGIN-UI.md` | Plugin panel/card guide; `HOW-IT-WORKS.md` and `desktop-app/PROTOCOL.md` are the architecture and wire specs |
| `core/bin/`, `desktop-app/{dist,dist-electron,release}/` | Build output — never committed |

No CI config exists in the repo; `make test` and `make lint` are the only gates.

## Commands

Install (Go needs no step; deps resolve on build):

```sh
cd desktop-app && pnpm install
```

Dev loop (builds the Go sidecar, Electron main, then Vite + Electron):

```sh
make app-dev      # = voice + core build + pnpm build:electron + pnpm dev
make              # same, it is the default goal
make voice        # fetch the Moonshine runtime + Tiny model and build voice-helper
make voice-all    # also fetch the Small and Medium dictation models
```

Voice assets live in `desktop-app/voice/`: the compressed runtime archives
(`runtimes/`), the Tiny model (`models/`) and the Linux x64 helper
(`bin/linux-x64/voice-helper`) are committed, so `make voice` on Linux is
fully offline and needs no C++ compiler. Other platforms compile the helper
once (or drop a binary in `voice/bin/<platform>-<arch>/`). The extracted
`lib/`, headers and notices are gitignored. `make app` and `make app-dev` run
`make voice` first; an offline checkout still builds, with the microphone
button disabled if the helper could not be produced. Models are swappable at
runtime: any directory under `voice/models/` or `<data>/voice/models/` with a
`model.json` appears in the composer's model menu.

Build a release (static stripped sidecar + electron-builder → `desktop-app/release/`):

```sh
make app                          # runs make clean first
cd desktop-app && pnpm build      # UI only
```

Lint / format / type-check:

```sh
make lint         # golangci-lint + deadcode, then eslint + knip + prettier --check
make fmt          # golangci-lint fmt (gofmt+goimports), then prettier --write
cd desktop-app && pnpm typecheck  # tsc src+electron, then tsc -p e2e/tsconfig.json
```

Tests:

```sh
make test         # go test ./...  then  pnpm typecheck + tsc electron + playwright test
cd core && go test ./...
cd desktop-app && pnpm exec playwright test
```

Single test:

```sh
cd core && go test ./internal/agent -run TestDoomLoopBlocksThirdIdenticalCall -v
cd desktop-app && pnpm exec playwright test e2e/chat.spec.ts     # one spec file
cd desktop-app && pnpm exec playwright test -g "sending a message"  # one test by name
```

`make` expects `golangci-lint` and `deadcode` in `$(go env GOPATH)/bin`; override with
`make lint GOLANGCI_LINT=… DEADCODE=…`.

Protocol smoke tests without the UI:

```sh
cd core && go build -o bin/agent-host ./cmd/agent-host
printf '%s\n' '{"id":1,"method":"ping","params":{}}' | core/bin/agent-host
cd desktop-app && ./node_modules/.bin/electron . --smoke-test --headless --disable-gpu --no-sandbox
```

## Conventions

**The RPC contract is co-owned.** `core/cmd/agent-host/methods.go` and
`desktop-app/PROTOCOL.md` must both change together — `TestEveryDocumentedMethodIsRegistered`
fails if a documented method is unregistered *or* a registered method is undocumented.
Adding a UI-facing call means: table entry, PROTOCOL.md row, a wrapper in
`src/core/api.ts`, then the feature module. JSON on the wire is camelCase
(`SessionInfo`, `TurnInfo`, …).

**stdout is reserved for JSONL.** Anything the core prints to stdout corrupts the
protocol; use stderr or the `logging.go` logger.

**Renderer.** Vanilla TypeScript + hand-rolled DOM — there is no React/Vue/Svelte
anywhere, and no router (screens are divs toggled with `hidden`/`flex`). Shared mutable
state lives in the single `src/core/state.ts` object, mutated directly to keep modules
cycle-free. `src/core/api.ts` is the only place allowed to call `window.paladin.invoke`;
feature modules go through it. Event routing lives in `src/features/live.ts`, which keeps
a detached DOM root per session so background sessions keep rendering, and
`turnView.ts`, which ignores any event with `seq <= lastSeq` so a re-attach never
double-paints. Coalesce delta paints to one per ~50 ms, and skip them entirely while a
turn is detached (repaint on attach). Transcripts are paged:
`attach` returns a `HistoryPage`, older rows come from `sessionHistoryPage`, and a
finished turn fetches only `sessionImages` instead of the whole transcript.

**Browser surfaces.** There is no separate Chat window. Websites are tabs in the agent
side pane (`src/features/sidePane.ts`): the title-bar **Web** menu (`src/features/web.ts`)
opens saved sites or any `https:` address, beside the agent in split layout or full width
in the tabs layout. Web tabs share one persistent `persist:paladin-sites` partition, so
logins and Google SSO survive restarts. Each open tab keeps its renderer alive while
hidden; closing the tab (or reloading the renderer) destroys the view. `views:state`
feeds the live page title, address bar and back/forward buttons. The agent's **local
browser** is a separate `browser` tab with an empty per-session in-memory profile, the
element picker and pass-to-agent. Conversation `https` links open web tabs; localhost
links open the local browser. Plugin panels and tool-result cards are sandboxed
`plugin://` iframes, not `WebContentsView`s.

**Styling.** Tailwind v4 with CSS-first `@theme` tokens in `src/styles/base.css`. New
sheets must be imported into `src/styles.css` — cascade order is load-bearing (later
files override earlier ones).

**Go.** Every file write goes through `core/internal/atomicfile` so a crash leaves old or
new content, never a torn file. Errors are returned, not logged and swallowed. `gofmt` +
`goimports` are enforced; `errcheck`, `govet`, `staticcheck`, `gocritic`, `errorlint`,
`nilerr`, `wastedassign` are on (test files are exempt from errcheck/unparam/gocritic/errorlint).

**TypeScript.** `strict`, plus `noUnusedLocals`/`noUnusedParameters`. ESLint is
type-aware and strict about promise handling: `no-floating-promises` and
`await-thenable` are errors, `eqeqeq` requires `===` (null check excepted), `no-empty`
disallows empty catch blocks, and type imports must be inline (`import { type Foo }`).
`knip` treats only `src/styles.css`, `electron/main.ts`, `electron/preload.ts` and
`e2e/**/*.spec.ts` as entry points — a new entry point must be added to `knip.json` or
knip reports it as unused. Prettier: 120 columns, trailing commas everywhere.

**Tests.** Go tests are plain `TestXxx` funcs with `t.Fatalf` assertions and
`t.Setenv` for env; there is no `t.Parallel()` anywhere in the repo, so keep new tests
serial. Playwright specs drive the real renderer with the fake bridge from
`e2e/paladin-stub.js` and reach into the UI through its real selectors (`#promptInput`,
`#messages`, `#sidebar`); `e2e/drawer.ts` shows the shared helper pattern.

## Gotchas

- **`PALADIN_DATA_DIR`** relocates the whole data directory (`core/internal/config/config.go:52`).
  Default is the OS config dir + `/paladin`. Anything that writes config, sessions, memory or
  snapshots lands there, not in the repo.
- **`PALADIN_KEYSTORE=memory`** keeps provider keys in memory; the sidecar tests rely on it to
  stay hermetic (`core/internal/runtime/runtime.go:139`). Without it, keys go to the OS keychain.
- **`PALADIN_SNAPSHOTS=off`** disables undo snapshots (`core/internal/runtime/undo.go:585`);
  snapshots are a hidden git repo under the data dir, never `.git` in your workspace.
- **`PALADIN_E2E_BROWSER`** picks the Playwright browser; fallback order is that var, then
  `/usr/bin/brave-browser`, then Playwright's Chromium, then any cached headless shell.
- **Web tabs belong to a session.** Opening the Web menu with no open session shows a toast;
  open tabs are not restored across app restarts (the saved site list and logins are). Web
  tabs accept only `https:`; `localhost` and local apps stay in the separate local browser.
- **Plugin UI is local-only.** Panels and cards read local plugin files over `plugin://`; a
  remote SSH core shows an explanatory message instead of the Plugins menu. Panel
  `invokeTool` calls go through the normal permission gate, and the approval card is
  rendered by the host, never by the plugin.
- **Agent shell commands run as jobs.** Each command streams its output to `<session>/tmp/<job>.log`;
  a foreground call waits 60 s and then hands a still-running command to the background with a job id.
  `action: "wait"` blocks on that job (60 s default, 300 s cap) — a `sleep` poll would itself become a
  second job. Nothing is killed on a timer — only `cancel` (or the user, from the running-jobs panel) stops one.
  Every answer that reports a command still running (a foreground yield and a timed-out wait) carries `output`
  with the last 5 lines of the log, so the model can tell a working command from a stuck one without spending
  another wait; it is a bounded read of the log's tail, not the whole file. A `status` check returns no output.
  When a command exits, any processes it left in its process group are killed, and on Linux every child
  carries `Pdeathsig=SIGKILL` (hooks, MCP and LSP servers too). Starting a server with `&` no longer
  survives the call — use `background: true`. Commands run at the same CPU priority as the user's
  terminal. Resource figures come from `/proc` on Linux and `ps` elsewhere, and CPU is a rate between
  samples, not `ps`'s lifetime average.
- **Port 5173 is fixed** (`strictPort` in `vite.config.ts` and the Playwright `webServer`).
  Vite fails rather than picking another port.
- **Only the OpenAI-compatible dialect is implemented.** `ApiTypeAnthropic` and
  `ApiTypeGoogle` exist as constants (`core/internal/config/config.go`) but are not wired;
  OpenCode-gateway models route through extra dialects in `core/internal/inference/opencode.go`.
- **Sandboxing is OS-specific**: macOS Seatbelt, Linux Landlock via a re-exec helper
  (`PALADIN_SANDBOX_HELPER`), and nothing elsewhere — other platforms rely on approvals only
  (`core/internal/sandbox/sandbox.go`).
- **Never commit** `core/bin/`, `core/agent-host`, `desktop-app/{node_modules,dist,dist-electron,release}/`,
  `test-results/`, `playwright-report/`, `.vite/`, or `tmp/`. Root `.gitignore` covers `core/bin/`,
  `tmp/`, `.idea/`; `desktop-app/.gitignore` covers the rest.
- **`desktop-app/release/` and `tmp/` currently exist untracked** in some checkouts (builder
  artifacts and screenshots). Leave them alone; they are output, not source.
- Windows `.exe` naming, `CGO_ENABLED=0` and `-trimpath` are all handled by the Makefile —
  cross-platform packaging goes through `make app`, not ad-hoc `go build`.
