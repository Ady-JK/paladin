# paladin desktop (Electron + agent-host sidecar)

## Architecture

```
desktop-app/
├── electron/
│   ├── main.ts      # window + sidecar spawn + JSONL bridge + WebContentsViews
│   └── preload.ts   # window.paladin bridge (stream demux, views, shell, windows)
├── src/             # renderer: the classic paladin UI (Tailwind, vanilla TS)
│   ├── main.ts      # bootstrap (imports styles.css so dev picks up utilities)
│   ├── core/        # api (bridge invoke), tauri (bridge probe+types), dom/format/state/types
│   ├── features/    # chat, sessions, models, sites, settings, workspace, window, tools
│   ├── ui/          # toast, status
│   └── styles.css   # Tailwind v4 theme + components
├── e2e/             # Playwright suite (paladin-stub.js fakes window.paladin)
└── PROTOCOL.md      # JSONL wire spec (Electron main <-> agent-host)
```

`core/cmd/agent-host` is the stdio sidecar (Go): one JSON object per line on
stdin, responses + `chat/delta` / `chat/tool` events on stdout, logs on
stderr. See `PROTOCOL.md`.

## Run

```sh
make dev                # fast loop: sidecar + Electron main/preload, Vite + Electron
make desktop-dev        # same thing (kept as an alias)
```

Headed Electron needs an X server. Headless smoke (CI-friendly):

```sh
cd desktop-app
./node_modules/.bin/electron . --smoke-test --headless --disable-gpu --no-sandbox
# [smoke] PASS: renderer loaded, agent-host ping ok
```

`--smoke-test` loads the window, fails on renderer load errors /
uncaught exceptions, pings the sidecar over JSONL, and exits 0/1.

## Web embeds

- Chat pages: `WebContentsView` with a persistent partition per page;
  Gemini and ChatGPT keep their original `persist:paladin-gemini` /
  `persist:paladin-chatgpt` partitions so existing logins survive.
- Defaults on a fresh profile: Gemini, ChatGPT, Claude, DeepSeek. They are
  ordinary entries and any of them can be removed like a custom page.
- Views stay alive when switching to Agent (hidden, never destroyed);
  only the selected view gets bounds. Bounds come from the renderer's
  `#externalHost` rect, one settle move per layout change.
- Views drop after 60s idle on the Agent tab to reclaim RAM; logins
  persist in the partition.
- The window is frameless: the renderer draws the header and the
  minimize/maximize/close buttons, and `views:setTheme` keeps every guest
  view's pre-paint background in the shell colour so opening a page never
  flashes white in dark mode.

## Release

```sh
make desktop-build      # release sidecar + vite + electron-builder (deb)
```
