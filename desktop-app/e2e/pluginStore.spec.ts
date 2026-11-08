import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { readPluginStore, writePluginStore } from "../electron/pluginStore";

test("plugin stores keep prototype keys and reject corrupt data without replacing it", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "paladin-plugin-store-"));
  const file = path.join(dir, ".store.json");
  try {
    writePluginStore(file, "__proto__", { example: true });
    expect(readPluginStore(file)["__proto__"]).toEqual({ example: true });
    writePluginStore(file, "constructor", "saved");
    expect(readPluginStore(file)["constructor"]).toBe("saved");
    writeFileSync(file, "{broken");
    expect(() => writePluginStore(file, "other", 1)).toThrow();
    expect(readFileSync(file, "utf8")).toBe("{broken");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("plugin store does not follow a predictable temp-file or store symlink", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "paladin-plugin-store-"));
  const file = path.join(dir, ".store.json");
  const victim = path.join(dir, "victim.json");
  try {
    writeFileSync(victim, '{"secret":"original"}');
    symlinkSync(victim, file + ".tmp");
    writePluginStore(file, "key", "value");
    expect(readFileSync(victim, "utf8")).toBe('{"secret":"original"}');
    rmSync(file);
    symlinkSync(victim, file);
    expect(() => readPluginStore(file)).toThrow();
    expect(() => writePluginStore(file, "key", "value")).toThrow();
    expect(readFileSync(victim, "utf8")).toBe('{"secret":"original"}');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
