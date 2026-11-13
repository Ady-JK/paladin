import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import process from "node:process";
import { test } from "node:test";
import { fileURLToPath, URL } from "node:url";

test("bundles the locked Playwright CDP client from pnpm's dependency layout", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "paladin-playwright-"));
  const script = fileURLToPath(new URL("./bundle-playwright.mjs", import.meta.url));
  try {
    execFileSync(process.execPath, [script], { cwd: directory, stdio: "pipe" });
    const target = path.join(directory, "dist-electron/vendor/playwright-core");
    const bundled = JSON.parse(readFileSync(path.join(target, "package.json"), "utf8"));
    const require = createRequire(import.meta.url);
    assert.equal(bundled.version, require("@playwright/test/package.json").version);
    assert.equal(typeof require(target).chromium.connectOverCDP, "function");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
