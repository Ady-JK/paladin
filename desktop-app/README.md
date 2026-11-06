# paladin desktop (Electron + agent-host sidecar)

## Architecture

```
desktop-app/
├── electron/
│   ├── main.ts      # window + sidecar spawn + JSONL bridge + WebContentsViews
│   ├── pluginHost.ts # sandboxed plugin:// panels and cards
│   └── preload.ts   # window.paladin bridge (stream demux, views, shell, windows)
├── src/             # renderer: the classic paladin UI (Tailwind, vanilla TS)
│   ├── main.ts      # bootstrap (imports styles.css so dev picks up utilities)
│   ├── core/        # api (bridge invoke), bridge (bridge probe+types), dom/format/state/types
│   ├── features/    # chat, sessions, models, web, sidePane, pluginPanels, settings, …
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

The Agent header keeps the new side chat, Web menu, local browser, terminal,
history, and close actions visible in either layout, even when the side pane is
closed. The history menu contains the Split/Tabs choice: split shows the agent
chat beside a side view, while tabs show one full-width view at a time. Tabs mode
includes the Agent conversation, side chats, subagents, terminals, web tabs, and
the session browser; switching layouts keeps their state. Clicking an
`http://localhost` or `http://127.0.0.1` link in an agent message opens that
session's local browser tab inside Paladin. Other `https` links open in a web tab
(the integrated browser below); non-http schemes still go to the system.

The local browser is separate from the integrated web tabs. Its navigation is
limited to `http://localhost` and `http://127.0.0.1`; session pages use separate
in-memory partitions; a hidden parking window keeps each page interactive while
another session is visible. Electron opens a random Chromium debugging port bound
to `127.0.0.1` for the life of the app so bundled Playwright can control those
pages. Software on the same computer with access to that port can inspect other
open Electron pages.

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

The app has one window. There is no separate Chat window; websites are tabs in
the agent side pane.

- **Web tabs** are for logged-in chat sites (Gemini, ChatGPT, Claude, DeepSeek)
  and any `https` address. The title bar's **Web** menu opens a saved site or a
  blank address bar; each tab has back/forward, reload, an address bar and
  open-in-system-browser. In split layout the page sits beside the agent; in
  tabs layout either takes the full width. The conversation tab is labelled
  **Agent**.
- **One shared login jar.** Web tabs share one persistent partition
  (`persist:paladin-sites`), so a Google login in one page carries over to
  "Sign in with Google" in any other and survives restarts. Per-page partitions
  isolated `accounts.google.com` cookies, forcing a second login; the old
  `persist:paladin-gemini` / `persist:paladin-chatgpt` jars are abandoned, which
  costs one re-login.
- **Every open tab stays alive.** Switching away detaches and throttles the
  `WebContentsView` but keeps its renderer and JS state, so a chat can finish
  answering while you read the agent and switching back is instant. Closing the
  tab destroys the view; reloading the renderer drops every `web:` view. Open
  tabs are per session and are not restored across app restarts; the saved site
  list and logins are.
- **Truthful toolbar.** Main reports `views:state` (`name,url,title,canGoBack,
  canGoForward`) after navigations, so link clicks and SPA route changes update
  the tab title and address bar instead of snapping back to the original URL.
  `views:back` / `views:forward` drive the real navigation history.
- **Saved sites** live in `localStorage` under `paladin.sites`, with
  `paladin.sites.seeded` so deleting a default keeps it deleted. A fresh profile
  seeds Gemini, ChatGPT, Claude and DeepSeek; all are ordinary, removable
  entries. Only `https` URLs are accepted.
- **The local browser is separate.** The globe button opens the agent's local
  browser tab: localhost/local apps, an empty in-memory per-session partition,
  element picking and pass-to-agent. It never shares cookies with web tabs.
  Conversation `https` links open web tabs; `localhost` links open this browser.
- **Guest pages cannot hijack the mouse:** a `dom-ready` stylesheet forces
  `-webkit-app-region: no-drag` on every guest, so a page-declared drag region
  (ChatGPT's logged-in header) can never swallow real clicks browser-side; our
  own title bar stays the drag handle.
- **Bounds** come from the active tab's surface rect in the renderer. A native
  view always paints above the renderer's HTML, so modals hide it while they are
  open. The sidebar is a floating drawer over the full-width main card (closes
  on an outside click, Escape, a session pick, or clicking the Agent rail button
  again; there is no separate sidebar button).
- **Plugin UI is not a WebContentsView.** Enabled plugins can add side-pane
  panels and tool-result cards; they are sandboxed iframes served over
  `plugin://` with a strict CSP, a `window.paladin.plugins` bridge, host-rendered
  approvals and per-plugin rate limits. See `docs/PLUGIN-UI.md`.
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
# Headless and remote core

`core/bin/agent-host` is a headless JSONL process. It reads one request per
line from stdin and writes replies and events to stdout; stderr is reserved
for logs. For example, after building the core:

```sh
printf '%s\n' '{"id":1,"method":"ping","params":{}}' | core/bin/agent-host
```

The desktop's **Settings → Connection** page can select **Remote over SSH**.
Enter an SSH target such as `user@host` and the absolute path to `agent-host`
on that machine. The desktop starts one remote headless process through `ssh
-T`; the same JSONL protocol carries chat, browser, terminal, and tool events
in both directions. The host key must already be trusted and key-based login
must work without a password prompt. Paladin uses `BatchMode=yes` and
`StrictHostKeyChecking=yes`, does not change SSH configuration, and does not
open a listening port. Quitting Paladin stops the remote process. The
remote core uses its own data directory, provider keys, workspace paths, and
installed tools. Each desktop connection starts its own process; shared remote
data directories are not a multi-client session server.

The agent browser stays in Electron on the desktop. To preview a development
server running on the remote machine, forward that server's port separately
over SSH to `127.0.0.1` on the desktop, then open that local URL in Paladin.

# Local extension plugins

Extensions add tools of your own. They live under Paladin's data directory
(`PALADIN_DATA_DIR` picks it): a **plugin folder** per extension, with its own
module, its own `node_modules` if it needs dependencies, and an optional
`paladin-plugin.json` manifest.

```sh
core/bin/agent-host extensions init               # hello.ts + paladin.d.ts + extensions.json
core/bin/agent-host extensions init --example-plugin   # also scaffolds hello/
```

Nothing runs until its name is in `config/extensions.json`:

```json
{"enabled":["hello"]}
```

A plugin folder is `config/extensions/hello/` with an `index.ts` (or
`index.js`) whose default export is `{tools: [...], onActivate?, onDeactivate?}`.
Each tool needs a unique `name`, a `description`, an object JSON Schema in
`parameters`, and an `execute(args, context)` function. A folder plugin gets
one long-lived Node process, started on its first call and reused across tool
calls and turns: it is reaped when idle, restarted after a crash, and asked to
shut down when the app exits. Loose files (`hello.ts` listed directly) still
work through the older spawn-per-call path and log a deprecation notice;
moving one into a folder of the same name keeps its tool names.

Tools appear as `extension__<folder>__<tool>` in the model tool list and
Paladin's Tools settings, where each plugin also shows its declared
capabilities, its warm process and any load error, next to a **Reload plugins**
button (the `toolsReload` RPC). Existing sessions still hold back newly
introduced tools until accepted.

Node resolution: the plugin's own `node` from its manifest, then the app's
Electron binary (its bundled Node 24, used when no separate Node is
installed), then `PALADIN_NODE`, then `node` on `PATH` (24 or newer).

Extensions run with the core user's OS permissions, which is why every call
asks until a permission rule allows the plugin (`jira`), one of its tools
(`jira/lookup`) or everything (`*`); the manifest's `permissions` list is
informational only. A failed plugin is skipped and logged, never blocking the
built-in tools. Each call has a timeout (120 s by default, set per plugin) and
a 1 MiB reply cap. TypeScript support is for erasable types; Node's type
stripping does not compile enums or TS path aliases. Extension code runs on
the remote machine when the core is remote. See
`core/internal/extension/README.md` for the full reference.
