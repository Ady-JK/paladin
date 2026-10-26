# paladin desktop (Electron + agent-host sidecar)

## Architecture

```
desktop-app/
├── electron/
│   ├── main.ts      # window + sidecar spawn + JSONL bridge + WebContentsViews
│   └── preload.ts   # window.paladin bridge (context isolation, no node in renderer)
├── src/             # renderer: vanilla TS UI (no framework, no Tailwind runtime)
│   ├── main.ts      # bootstrap
│   ├── api.ts       # typed IPC wrapper (invoke + chat stream demux + views)
│   ├── state.ts     # single shared state object
│   ├── chat.ts      # rAF-coalesced streaming, stick-to-bottom scroll
│   ├── sessions.ts  # delegated clicks, debounced search, single-layout history
│   ├── models.ts    # debounced picker, visibility-gated trees, 300-row cap
│   ├── sites.ts     # Agent/Gemini/ChatGPT tabs -> persistent WebContentsViews
│   ├── settings.ts  # provider CRUD
│   └── workspace.ts # fs browser
└── PROTOCOL.md      # JSONL wire spec (Electron main <-> agent-host)
```

`crates/agent-host` is the stdio sidecar: one JSON object per line on
stdin, responses + `chat/delta` / `chat/tool` events on stdout, logs on
stderr. See `PROTOCOL.md`.

## Run

```sh
make desktop-dev        # cargo build -p agent-host, pnpm install, vite + electron
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

- ChatGPT: `WebContentsView`, `persist:paladin-chatgpt`
- Gemini: `WebContentsView`, `persist:paladin-gemini`
- Views stay alive when switching to Agent (hidden, never destroyed);
  only the selected view gets bounds. Bounds come from the renderer's
  `#externalHost` rect, one settle move per layout change.
- Built-in views drop after 60s idle on the Agent tab to reclaim RAM;
  logins persist in the partition.

## Release

```sh
make desktop-build      # release sidecar + vite + electron-builder (deb)
```
