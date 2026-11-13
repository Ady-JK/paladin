#!/usr/bin/env node
// voice-assets.mjs prepares the Moonshine WebAssembly runtime and model files
// and installs the worker that `electron/voice.ts` runs in a utility process.
//
//   node scripts/voice-assets.mjs            # the default (Tiny) model
//   node scripts/voice-assets.mjs --all      # every model in the registry
//   node scripts/voice-assets.mjs --force    # redownload and re-extract
//
// The WASM runtime archive and the Tiny model are vendored under voice/, so a
// normal run needs no network. Missing files fall back to the pinned URLs.
// Output lives in desktop-app/voice/ (the extracted runtime, worker copy and
// notices are gitignored; `make voice` is the friendly door).

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { copyFile, cp, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const appDir = path.resolve(here, "..");
const voiceDir = path.join(appDir, "voice");
const registry = JSON.parse(await readFile(path.join(here, "voice-models.json"), "utf8"));
const flags = new Set(process.argv.slice(2));
const all = flags.has("--all");
const force = flags.has("--force");
const strict = process.env.PALADIN_VOICE_STRICT === "1" || all;

const RELEASE = registry.release;
const RELEASE_BASE = `https://github.com/moonshine-ai/moonshine/releases/download/${RELEASE}`;

function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

async function download(url, dest, expected) {
  process.stdout.write(`  ↓ ${url.split("/").slice(-1)[0]}\n`);
  const res = await fetch(url, { redirect: "follow" });
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
  const data = Buffer.from(await res.arrayBuffer());
  if (expected) {
    const got = sha256(data);
    if (got !== expected) throw new Error(`${url}: checksum mismatch (${got})`);
  }
  await mkdir(path.dirname(dest), { recursive: true });
  await writeFile(`${dest}.part`, data);
  await rename(`${dest}.part`, dest);
}

// ensureWasm extracts the single WebAssembly build that runs on every
// platform, so there is no per-platform archive and nothing to compile.
async function ensureWasm() {
  const entry = path.join(voiceDir, "wasm", "dist", "transcriber.js");
  const dest = path.join(voiceDir, "wasm", "dist");
  // The binding's dist/ is ESM but ships without its package manifest, so
  // Node would guess CommonJS and warn on every import.
  const manifest = path.join(path.dirname(dest), "package.json");
  if (!force && existsSync(entry)) {
    await writeFile(manifest, '{\n  "type": "module"\n}\n');
    return;
  }
  const asset = registry.wasm;
  // The archive is vendored under voice/runtimes/ so a clone with no
  // network can still extract it. The expanded files stay gitignored;
  // only the compressed archive is tracked.
  const vendored = path.join(voiceDir, "runtimes", asset.name);
  let archive = vendored;
  if (!existsSync(archive)) {
    archive = path.join(voiceDir, asset.name);
    await download(asset.url ?? `${RELEASE_BASE}/${asset.name}`, archive, asset.sha256);
  } else if (asset.sha256) {
    const got = sha256(await readFile(archive));
    if (got !== asset.sha256) throw new Error(`${archive}: checksum mismatch (${got})`);
  }
  const extract = path.join(voiceDir, ".extract");
  await rm(extract, { recursive: true, force: true });
  await mkdir(extract, { recursive: true });
  execFileSync("tar", ["-xzf", archive, "-C", extract], { stdio: "inherit" });
  const dist = path.join(extract, "dist");
  if (!existsSync(dist)) throw new Error("the WASM archive has no dist/ directory");
  await rm(path.dirname(dest), { recursive: true, force: true });
  await mkdir(path.dirname(dest), { recursive: true });
  // Only the speech-to-text chain is installed: the worker imports
  // transcriber.js, which pulls in module/stream/events/errors/enums/types
  // and the .wasm. TTS, agent-flow, voice-clone, mic capture helpers,
  // source maps and type declarations are not needed at runtime.
  const runtime = new Set([
    "asset-downloader.js",
    "enums.js",
    "errors.js",
    "events.js",
    "module.js",
    "stream.js",
    "transcriber.js",
    "types.js",
    "moonshine.mjs",
    "moonshine.wasm",
  ]);
  await cp(dist, dest, {
    recursive: true,
    filter: (name) => {
      const base = path.basename(name);
      if (base.startsWith("._")) return false;
      if (base.endsWith(".map") || base.endsWith(".d.ts")) return false;
      if (base.endsWith(".js") || base.endsWith(".mjs") || base.endsWith(".wasm")) return runtime.has(base);
      return true;
    },
  });
  await writeFile(manifest, '{\n  "type": "module"\n}\n');
  await rm(extract, { recursive: true, force: true });
  if (archive !== vendored) await rm(archive, { force: true });
}

async function ensureModels() {
  const models = all ? registry.models : registry.models.filter((model) => model.default);
  for (const model of models) {
    const dir = path.join(voiceDir, "models", model.id);
    await mkdir(dir, { recursive: true });
    for (const [name, hash] of Object.entries(model.files)) {
      const dest = path.join(dir, name);
      if (!force && existsSync(dest) && (!hash || sha256(await readFile(dest)) === hash)) continue;
      await download(`${model.baseUrl}/${name}`, dest, hash);
    }
    const manifest = {
      id: model.id,
      label: model.label,
      arch: model.arch,
      language: model.language,
      description: model.description,
    };
    await writeFile(path.join(dir, "model.json"), `${JSON.stringify(manifest, null, 2)}\n`);
    process.stdout.write(`  ✓ ${model.label}\n`);
  }
}

// installWorker copies the tracked worker into voice/, where packaging and
// the dev build both expect it next to wasm/ and models/.
async function installWorker() {
  await copyFile(path.join(appDir, "voice-worker", "worker.mjs"), path.join(voiceDir, "worker.mjs"));
  await copyFile(path.join(appDir, "voice-worker", "THIRD-PARTY-NOTICES.md"), path.join(voiceDir, "NOTICES.md"));
}

async function main() {
  await mkdir(voiceDir, { recursive: true });
  process.stdout.write("voice: wasm runtime\n");
  await ensureWasm();
  process.stdout.write("voice: models\n");
  await ensureModels();
  process.stdout.write("voice: worker\n");
  await installWorker();
  process.stdout.write(`voice: ready in ${path.relative(appDir, voiceDir)}/\n`);
}

main().catch((error) => {
  process.stderr.write(`voice: ${error instanceof Error ? error.message : String(error)}\n`);
  if (strict) process.exit(1);
  process.stderr.write("voice: dictation stays unavailable until `make voice` succeeds\n");
  process.exit(0);
});
