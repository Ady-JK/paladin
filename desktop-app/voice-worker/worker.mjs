// voice-worker.mjs runs the Moonshine WASM transcriber inside an Electron
// utility process.
//
// The renderer captures 16 kHz mono audio and sends it to the main process,
// which forwards it here as structured-clone messages. This process owns the
// WebAssembly module and the streaming transcriber, sends line events back,
// and exits when the main process drops it after an idle period.
//
// Nothing here is platform-specific: the same WebAssembly module and the same
// model files run on Linux, macOS and Windows, so there is no native helper to
// compile or ship per platform.
//
// Messages in:
//   { type: "load", dir, arch }   load (or reuse) a model and open a stream
//   { type: "audio", audio }      one Float32Array of 16 kHz mono samples
//   { type: "stop" }              flush the final line and finish
//   { type: "cancel" }            discard the take
//   { type: "shutdown" }          release everything and exit
//
// Messages out:
//   { event: "ready", arch, dir }
//   { event: "line", index, id, text, complete, latencyMs? }
//   { event: "stopped" } | { event: "cancelled" } | { event: "error", message }
//
// A line is re-sent only when it changed; a completed line never changes.

import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const wasmEntry = path.join(here, "wasm", "dist", "transcriber.js");
const SAMPLE_RATE = 16000;
const UPDATE_INTERVAL = 0.1;

// Canonical filenames the binding understands. Passing only these keeps a
// model directory's metadata (model.json) out of the file map.
const MODEL_FILES = new Set([
  "adapter.ort",
  "cross_kv.ort",
  "decoder_kv.ort",
  "decoder_kv_with_attention.ort",
  "decoder_model_merged.ort",
  "decoder_with_attention.ort",
  "encoder.ort",
  "encoder_model.ort",
  "frontend.model.ort",
  "frontend.ort",
  "frontend.weights.ort",
  "spelling_cnn.ort",
  "streaming_config.json",
  "tokenizer.bin",
]);

const send = (message) => process.parentPort.postMessage(message);
const log = (message) => process.stderr.write(`[voice-worker] ${message}\n`);

let Transcriber = null;
let transcriber = null;
let stream = null;
let loadedDir = null;
let loadedArch = null;
let indexById = new Map();
let nextIndex = 0;

function readModelFiles(dir) {
  const files = {};
  for (const name of readdirSync(dir)) {
    if (!MODEL_FILES.has(name)) continue;
    const file = path.join(dir, name);
    try {
      if (!statSync(file).isFile()) continue;
    } catch {
      continue;
    }
    files[name] = new Uint8Array(readFileSync(file));
  }
  return files;
}

function lineIndex(line) {
  let index = indexById.get(line.id);
  if (index === undefined) {
    index = nextIndex++;
    indexById.set(line.id, index);
  }
  return index;
}

function emitLine(line, complete) {
  const event = { event: "line", index: lineIndex(line), id: line.id, text: line.text, complete };
  if (line.lastTranscriptionLatencyMs) event.latencyMs = line.lastTranscriptionLatencyMs;
  send(event);
}

function closeStream() {
  if (!stream) return;
  try {
    stream.close();
  } catch (error) {
    log(`close: ${error instanceof Error ? error.message : String(error)}`);
  }
  stream = null;
}

async function load(message) {
  const { dir, arch } = message;
  if (!Transcriber) {
    // Import the STT entry point, not the package barrel: the archive also
    // ships TTS and agent-flow modules this worker never uses.
    const binding = await import(new URL("./wasm/dist/transcriber.js", import.meta.url).href);
    Transcriber = binding.Transcriber;
  }
  closeStream();
  if (!transcriber || loadedDir !== dir || loadedArch !== arch) {
    if (transcriber) transcriber.close();
    transcriber = null;
    transcriber = await Transcriber.load({ files: readModelFiles(dir), modelArch: arch });
    loadedDir = dir;
    loadedArch = arch;
  }
  indexById = new Map();
  nextIndex = 0;
  stream = transcriber.createStream({ updateInterval: UPDATE_INTERVAL });
  stream.addListener({
    onLineStarted: (event) => emitLine(event.line, false),
    onLineTextChanged: (event) => emitLine(event.line, false),
    onLineUpdated: (event) => emitLine(event.line, Boolean(event.line.isComplete)),
    onLineCompleted: (event) => emitLine(event.line, true),
    onLineSpeakersChanged: () => {},
    onError: (event) => {
      const error = event?.error;
      send({ event: "error", message: error instanceof Error ? error.message : String(error) });
    },
  });
  stream.start();
  send({ event: "ready", arch, dir });
}

function audio(message) {
  if (!stream) return;
  const samples = message.audio instanceof Float32Array ? message.audio : new Float32Array(message.audio);
  stream.addAudio(samples, SAMPLE_RATE);
  stream.transcribe();
}

function stop() {
  if (!stream) {
    send({ event: "stopped" });
    return;
  }
  const finishing = stream;
  stream = null;
  finishing.stop();
  finishing.close();
  send({ event: "stopped" });
}

function cancel() {
  closeStream();
  send({ event: "cancelled" });
}

function shutdown() {
  closeStream();
  if (transcriber) transcriber.close();
  transcriber = null;
  send({ event: "shutdown" });
  setTimeout(() => process.exit(0), 20);
}

process.parentPort.on("message", (event) => {
  const message = event.data;
  if (!message || typeof message !== "object") return;
  try {
    if (message.type === "load") {
      load(message).catch((error) => {
        send({ event: "error", message: error instanceof Error ? error.message : String(error) });
      });
    } else if (message.type === "audio") {
      audio(message);
    } else if (message.type === "stop") {
      stop();
    } else if (message.type === "cancel") {
      cancel();
    } else if (message.type === "shutdown") {
      shutdown();
    }
  } catch (error) {
    send({ event: "error", message: error instanceof Error ? error.message : String(error) });
  }
});

log(`ready (wasm: ${wasmEntry})`);
