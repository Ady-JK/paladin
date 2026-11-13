# voice-helper

A ~30 KB C++ program that turns the Moonshine C API into a line-oriented
stdio service. Electron main spawns it, writes framed audio/commands to
stdin, and reads one JSON object per line from stdout. It is never exposed
to the renderer and never opens a socket.

## Why a child process

The Moonshine/ONNX model is CPU-heavy and allocates hundreds of megabytes.
Running it in Electron main would freeze the window, and running it in the
renderer would put a native model inside the sandboxed UI. A separate
process keeps the model at below-normal priority, lets the app kill it
cleanly, and drops all of its memory when it exits.

## Framing

Each stdin frame is `u8 type`, `u32le payload length`, then the payload:

| type | payload | meaning |
|---|---|---|
| 1 | float32le PCM | 16 kHz mono audio (any chunk size) |
| 2 | `u32le arch` + UTF-8 model dir | load a model and open a stream |
| 3 | — | flush and finish the current dictation |
| 4 | — | abandon the current dictation |
| 5 | — | unload and exit |

stdout is newline-delimited JSON:

```json
{"event":"ready","arch":2,"dir":"/path/to/model"}
{"event":"line","index":0,"id":"1694...","text":"hello world","complete":false,"latencyMs":71}
{"event":"stopped"}
{"event":"cancelled"}
{"event":"error","message":"..."}
```

A line is re-sent only when the library reports it changed; completed lines
never change again. The renderer keeps the lines by index, shows the last
incomplete one as live text, and inserts the finished lines on `stopped`.

## Building

`make voice` runs `desktop-app/scripts/voice-assets.mjs`, which:

1. extracts the pinned Moonshine runtime archive (ONNX Runtime +
   `libmoonshine`) from `desktop-app/voice/runtimes/` (vendored in git),
2. verifies the Tiny model files already under `voice/models/`,
3. compiles this file into `desktop-app/voice/voice-helper`.

No network is needed: the runtime archives and the Tiny model are committed.
If a file is missing, the script falls back to the pinned GitHub release /
Moonshine CDN and verifies its SHA-256. `make voice-all` fetches Small and
Medium from the CDN if they are not present. The extracted runtime, helper
binary and notices are gitignored; only the source, registry, model and
compressed runtime archives are tracked.

On Linux the helper links with an `$ORIGIN/lib` rpath. On macOS the script
uses `@loader_path/lib`. On Windows the DLLs sit beside `voice-helper.exe`.

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
