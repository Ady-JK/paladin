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

## Local browser testing

The shipped `litellm` provider uses the usual `http://127.0.0.1:4000/v1`
address as an editable starting point. In Settings → Providers, connect it
with your proxy URL and key, refresh its model list, and choose a model before
starting an agent turn. The proxy and its credentials remain under your
control; Paladin does not start or bundle a LiteLLM server.

Each Agent session has its own browser tab in the side pane. Open a local URL
there, or let the agent use the `browser` tool to open it, inspect an accessible
snapshot, interact with the page, and take screenshots. The agent's `read`
tool also accepts PNG, JPEG, GIF, and WebP files up to 4 MB and sends them as
image input to a vision-capable model. Browser screenshots are saved under the
session's `browser/screenshots/` directory and sent to the model visually.

The Agent header keeps the new side chat, browser, terminal, history, and close
actions visible in either layout, even when the side pane is closed. The
history menu contains the Split/Tabs choice: split shows chat beside a side
view, while tabs show one full-width view at a time. Tabs mode includes the main chat,
side chats, subagents, and the session browser; switching layouts keeps their
state. Clicking an `http://localhost` or `http://127.0.0.1` link in an agent
message opens that session's browser tab inside Paladin. Other links still open
in the system browser. Repeated local links reuse the browser tab.

Browser navigation and network requests are limited to `http://localhost` and
`http://127.0.0.1`. Session pages use separate in-memory partitions; a hidden
parking window keeps each page interactive while another session is visible.
Electron opens a random Chromium debugging port bound to `127.0.0.1` for the
life of the app so bundled Playwright can control those pages. Software on the
same computer with access to that port can inspect other open Electron pages.

`build:electron` copies the installed, lockfile-resolved `playwright-core` into
the packaged Electron output. Electron supplies Chromium, so this feature does
not download a separate browser. The real browser smoke can be run with:

```sh
cd desktop-app
pnpm build:electron && pnpm build:renderer
pnpm exec electron . --smoke-browser
```

## Workspace terminal

Open the side pane and click the terminal icon to start an interactive shell in
the current session's workspace. Each terminal tab has its own PTY, supports
normal terminal programs, and runs with your OS permissions outside the agent
sandbox. Closing the tab stops its shell. The terminal emulator loads only when
you open a terminal.

At the start of the main composer, `! command` runs the command in the session
workspace and starts an agent turn with its command, exit code, and output.
`!! command` runs and displays the output without starting an agent turn or
adding it to the agent's conversation. Both forms show their output in the
terminal pane. These composer commands are separate from the interactive
terminal shell; they run in the workspace selected for the session.

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
