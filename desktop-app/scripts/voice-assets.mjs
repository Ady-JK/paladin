#!/usr/bin/env node
// voice-assets.mjs downloads the Moonshine runtime + model files and
// builds the stdio helper that `electron/voice.ts` spawns.
//
//   node scripts/voice-assets.mjs            # the default (Tiny) model
//   node scripts/voice-assets.mjs --all      # every model in the registry
//   node scripts/voice-assets.mjs --force    # redownload and rebuild
//
// Output lives in desktop-app/voice/ (gitignored): the helper, its
// shared libraries, and models/<id>/. `make voice` is the friendly door.

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, statSync } from "node:fs";
import { copyFile, mkdir, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
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

function runtimeAsset() {
  const key = `${process.platform}-${process.arch}`;
  const asset = registry.runtime[key];
  if (!asset) {
    throw new Error(`no Moonshine runtime for ${key} (supported: ${Object.keys(registry.runtime).join(", ")})`);
  }
  return asset;
}

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

async function ensureRuntime() {
  // macOS ships a static libmoonshine.a; Linux ships libmoonshine.so.
  const libNames =
    process.platform === "win32"
      ? ["moonshine.dll", "libmoonshine.dll"]
      : process.platform === "darwin"
        ? ["libmoonshine.a", "libmoonshine.dylib"]
        : ["libmoonshine.so"];
  if (
    !force &&
    libNames.some((name) => existsSync(path.join(voiceDir, "lib", name))) &&
    existsSync(path.join(voiceDir, "include", "moonshine-c-api.h"))
  ) {
    return;
  }
  const asset = runtimeAsset();
  const tarball = path.join(voiceDir, asset.name);
  if (force || !existsSync(tarball)) {
    await download(asset.url ?? `${RELEASE_BASE}/${asset.name}`, tarball, asset.sha256);
  }
  const extract = path.join(voiceDir, ".extract");
  await rm(extract, { recursive: true, force: true });
  await mkdir(extract, { recursive: true });
  execFileSync("tar", ["-xzf", tarball, "-C", extract], { stdio: "inherit" });
  const top = (await readdir(extract)).find((name) => !name.startsWith("."));
  if (!top) throw new Error("the runtime archive was empty");
  const root = path.join(extract, top);
  const libSrc = path.join(root, "lib");
  const includeSrc = path.join(root, "include");
  const libDst = path.join(voiceDir, "lib");
  const includeDst = path.join(voiceDir, "include");
  await rm(libDst, { recursive: true, force: true });
  await rm(includeDst, { recursive: true, force: true });
  await mkdir(libDst, { recursive: true });
  await mkdir(includeDst, { recursive: true });
  for (const name of await readdir(libSrc)) {
    if (name.startsWith("._")) continue;
    const source = path.join(libSrc, name);
    if (!statSync(source).isFile()) continue;
    await copyFile(source, path.join(libDst, name));
  }
  for (const name of await readdir(includeSrc)) {
    if (name.startsWith("._")) continue;
    const source = path.join(includeSrc, name);
    if (!statSync(source).isFile()) continue;
    await copyFile(source, path.join(includeDst, name));
  }
  await rm(extract, { recursive: true, force: true });
  await rm(tarball, { force: true });
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

async function buildHelper() {
  await copyFile(path.join(appDir, "voice-helper", "THIRD-PARTY-NOTICES.md"), path.join(voiceDir, "NOTICES.md"));
  const source = path.join(appDir, "voice-helper", "voice-helper.cpp");
  const include = path.join(voiceDir, "include");
  const lib = path.join(voiceDir, "lib");
  const out = path.join(voiceDir, process.platform === "win32" ? "voice-helper.exe" : "voice-helper");
  const cxx = process.env.CXX || (process.platform === "darwin" ? "clang++" : "c++");
  const args = ["-O2", "-std=c++17", "-I", include, source, "-L", lib, "-lmoonshine"];
  if (process.platform === "darwin") args.push("-Wl,-rpath,@loader_path/lib");
  else if (process.platform !== "win32") args.push("-Wl,-rpath,$ORIGIN/lib");
  args.push("-o", out);
  process.stdout.write(`  ⚒ ${cxx} voice-helper.cpp\n`);
  execFileSync(cxx, args, { stdio: "inherit" });
}

async function main() {
  await mkdir(voiceDir, { recursive: true });
  process.stdout.write("voice: runtime\n");
  await ensureRuntime();
  process.stdout.write("voice: models\n");
  await ensureModels();
  process.stdout.write("voice: helper\n");
  await buildHelper();
  process.stdout.write(`voice: ready in ${path.relative(appDir, voiceDir)}/\n`);
}

main().catch((error) => {
  process.stderr.write(`voice: ${error instanceof Error ? error.message : String(error)}\n`);
  if (strict) process.exit(1);
  process.stderr.write("voice: dictation stays unavailable until `make voice` succeeds\n");
  process.exit(0);
});
