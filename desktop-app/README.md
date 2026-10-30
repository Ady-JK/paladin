# paladin desktop (Electron + agent-host sidecar)

## Architecture

```
desktop-app/
├── electron/
│   ├── main.ts      # window + sidecar spawn + JSONL bridge + WebContentsViews
│   └── preload.ts   # window.paladin bridge (stream demux, views, shell, windows)
├── src/             # renderer: the classic paladin UI (Tailwind, vanilla TS)
│   ├── main.ts      # bootstrap (imports styles.css so dev picks up utilities)
│   ├── core/        # api (bridge invoke), bridge (bridge probe+types), dom/format/state/types
│   ├── features/    # chat, sessions, models, sites, settings, workspace, window, tools
│   ├── ui/          # toast, status
│   ├── styles.css   # stylesheet entry: imports the sheets below in cascade order
│   └── styles/      # Tailwind v4 theme (base.css) + one sheet per feature
├── e2e/             # Playwright suite (paladin-stub.js fakes window.paladin)
└── PROTOCOL.md      # JSONL wire spec (Electron main <-> agent-host)
```

`core/cmd/agent-host` is the stdio sidecar (Go): one JSON object per line on
stdin, responses + `chat/delta` / `chat/tool` events on stdout, logs on
stderr. See `PROTOCOL.md`.

## Run

```sh
make app-dev    # quick loop: debug sidecar + Electron main/preload, Vite + Electron
make test       # Go tests, UI typecheck and the Playwright UI suite
make fmt        # format Go and UI sources
make lint       # golangci-lint, deadcode, eslint, knip, prettier --check
make clean      # remove every build output
```

The Makefile works with GNU make on macOS, Linux and Windows. Go lint
tools are found in `$(go env GOPATH)/bin`; override with
`GOLANGCI_LINT=…` / `DEADCODE=…`. The UI tests use, in order,
`PALADIN_E2E_BROWSER`, Brave on Linux, Playwright's own Chromium, then
the newest headless shell already in the Playwright cache.

Headed Electron needs an X server. Headless smoke (CI-friendly):

```sh
cd desktop-app
./node_modules/.bin/electron . --smoke-test --headless --disable-gpu --no-sandbox
# [smoke] PASS: renderer loaded, agent-host ping ok
```

`--smoke-test` loads the window, fails on renderer load errors /
uncaught exceptions, pings the sidecar over JSONL, and exits 0/1.

## OpenCode Go

The seeded `Opencode Go` provider targets OpenCode's OpenAI-compatible endpoint
(`https://opencode.ai/inference/go/openai/v1`; the `/zen/go/v1` alias also works).
Every request sends `User-Agent: paladin/1.0` and `x-opencode-session` set to the
transcript session id. Chat requests also send that id as `prompt_cache_key`, so
all rounds of one conversation share OpenCode's routing and prompt-cache bucket.
The model catalog is derived from the configured base URL rather than a hardcoded
gateway URL. Models that OpenCode only exposes over the Responses, Anthropic
Messages, or Google Generative Language APIs are routed to those dialects
automatically; Gemini thought signatures are round-tripped through the
transcript so tool follow-up rounds stay valid.

## Web embeds

- Chat pages: `WebContentsView` sharing one persistent partition
  (`persist:paladin-sites`), so a Google login in one page carries over
  to "Sign in with Google" in any other. (Per-page partitions isolated
  `accounts.google.com` cookies, forcing a second login; the old
  `persist:paladin-gemini` / `persist:paladin-chatgpt` jars are
  abandoned, which costs one re-login.)
- Single live guest: showing a chat page destroys the previous page's
  view outright — each hidden SPA pins a full renderer, and keeping
  visited pages alive ballooned past 2GB. Reloads stay logged in
  (partitions persist on disk).
- Guest pages cannot hijack the mouse: a `dom-ready` stylesheet forces
  `-webkit-app-region: no-drag` on every guest, so a page-declared drag
  region (ChatGPT's logged-in header) can never swallow real clicks
  browser-side; our own title bar stays the drag handle.
- Views stay alive when switching to Agent (hidden, never destroyed);
  only the selected view gets bounds. Bounds come from the renderer's
  `#externalHost` rect, one settle move per layout change.
- Chat mode has no sidebar: pages are browser-style tabs in a fixed-height
  strip above `#externalHost` (× removes a page, + adds one). A native
  view always paints above the renderer's HTML, so nothing may float over
  it; with no sidebar and a fixed strip the page's bounds never change
  after it is shown. In Agent mode the sidebar is a floating drawer over
  the full-width main card (closes on an outside click, Escape, a session
  pick, or clicking the Agent rail button again; there is no separate
  sidebar button).
- Defaults on a fresh profile: Gemini, ChatGPT, Claude, DeepSeek. They are
  ordinary entries and any of them can be removed like a custom page.
- Views drop after 60s idle on the Agent tab to reclaim RAM; logins
  persist in the partition.
- The window is frameless: the renderer draws the header and the
  minimize/maximize/close buttons, and `views:setTheme` keeps every guest
  view's pre-paint background in the shell colour so opening a page never
  flashes white in dark mode.

## Release

```sh
make app       # clean, then a static stripped sidecar + minified renderer + electron-builder
```

`make app` builds the sidecar with `CGO_ENABLED=0 -trimpath -ldflags "-s -w"`
and packages with maximum compression into `desktop-app/release/`
(dmg on macOS, NSIS on Windows, deb on Linux). The sidecar ships as
`agent-host` (`agent-host.exe` on Windows) under the app resources.
