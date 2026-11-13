# voice-worker

`worker.mjs` runs the Moonshine WebAssembly transcriber in an Electron
[`utilityProcess`](https://www.electronjs.org/docs/latest/api/utility-process).
Electron main forks it, forwards 16 kHz mono audio to it, and receives
transcript events back. It is never exposed to the renderer and never opens a
socket.

## Why WebAssembly in a utility process

The model is CPU-heavy and allocates hundreds of megabytes, so it must not run
on the window's thread or inside the sandboxed renderer. A utility process
keeps the model at below-normal priority, lets the app kill it cleanly, and
drops all of its memory when it exits — the same lifecycle the old native
helper had.

The difference is portability. The published `moonshine-voice-wasm` build is a
single WebAssembly module plus a thin TypeScript layer, and the same files run
on Linux, macOS and Windows. There is no per-platform runtime archive, no
prebuilt executable and no C++ compiler: a checkout only needs Node and the
committed assets.

`worker.mjs` deliberately runs in **Node**, not in a renderer. Chromium only
exposes `SharedArrayBuffer` to cross-origin-isolated pages, and the packaged
app loads its UI from `file://`, where that is never true. Node has
`SharedArrayBuffer` and `worker_threads` unconditionally, so the threaded WASM
build runs there without changing how the app is served.

## Messages

Electron main and the worker exchange structured-clone messages:

| From main | Meaning |
|---|---|
| `{ type: "load", dir, arch }` | load (or reuse) a model and open a stream |
| `{ type: "audio", audio: Float32Array }` | 16 kHz mono samples |
| `{ type: "stop" }` | flush the final line and finish |
| `{ type: "cancel" }` | abandon the current dictation |
| `{ type: "shutdown" }` | release everything and exit |

| From the worker | Meaning |
|---|---|
| `{ event: "ready", arch, dir }` | the model is loaded and listening |
| `{ event: "line", index, id, text, complete, latencyMs? }` | a transcript line |
| `{ event: "stopped" }` / `{ event: "cancelled" }` | the take ended |
| `{ event: "error", message }` | the take failed |

A line is re-sent only when the library reports it changed; completed lines
never change again. The renderer keeps the lines by index, shows the last
incomplete one as live text, and inserts the finished lines on `stopped`.

## Building

`make voice` runs `desktop-app/scripts/voice-assets.mjs`, which:

1. extracts the pinned WebAssembly runtime archive from
   `desktop-app/voice/runtimes/` (vendored in git),
2. verifies the Tiny model files already under `voice/models/`,
3. copies this worker and the third-party notices into `desktop-app/voice/`.

No network is needed and nothing is compiled. If the archive or a model file is
missing, the script falls back to the pinned GitHub release / Moonshine CDN and
verifies its SHA-256. `make voice-all` fetches Small and Medium from the CDN if
they are not present. The extracted runtime, the copied worker and the notices
are gitignored; the source, registry, model and runtime archive are tracked.

## Adding a model

Drop a directory under `<data>/voice/models/<id>/` containing the Moonshine
`.ort` files and a `model.json`:

```json
{
  "id": "small-streaming-en",
  "label": "Moonshine Small Streaming (English)",
  "arch": 4,
  "language": "en",
  "description": "Best balance of accuracy and speed."
}
```

`arch` is the Moonshine constant: Tiny Streaming `2`, Small Streaming `4`,
Medium Streaming `5`. Paladin lists every valid directory in the composer's
dictation menu; nothing needs editing.
