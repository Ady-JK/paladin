import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readlinkSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import { repairPnpmLinks } from "./repair-pnpm-links.mjs";

test("repairs relocated pnpm hoisted links without changing valid links", () => {
  const root = mkdtempSync(path.join(tmpdir(), "paladin-links-"));
  try {
    const store = path.join(root, "node_modules", ".pnpm");
    const hoisted = path.join(store, "node_modules");
    const actual = path.join(store, "7zip-bin@1", "node_modules", "7zip-bin");
    mkdirSync(actual, { recursive: true });
    mkdirSync(hoisted, { recursive: true });
    const broken = path.join(hoisted, "7zip-bin");
    const valid = path.join(hoisted, "valid");
    symlinkSync(
      path.join(root, "old", "node_modules", ".pnpm", "7zip-bin@1", "node_modules", "7zip-bin"),
      broken,
      "junction",
    );
    symlinkSync(actual, valid, "junction");
    const before = readlinkSync(valid);
    assert.equal(repairPnpmLinks(root), 1);
    assert.equal(existsSync(broken), true);
    assert.equal(readlinkSync(valid), before);
    assert.equal(repairPnpmLinks(root), 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
