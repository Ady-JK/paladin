// Use the already locked Playwright test dependency as the source of the
// runtime CDP client. No registry change or browser download is needed:
// Electron supplies Chromium, and the bundled client connects to it.
import { cpSync, mkdirSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { stdout } from "node:process";

const require = createRequire(import.meta.url);
const testRequire = createRequire(require.resolve("@playwright/test"));
const source = path.dirname(testRequire.resolve("playwright-core/package.json"));
const target = path.resolve("dist-electron/vendor/playwright-core");
rmSync(target, { recursive: true, force: true });
mkdirSync(path.dirname(target), { recursive: true });
cpSync(source, target, { recursive: true, dereference: true });
stdout.write("Bundled Playwright CDP client from the existing lockfile\n");
