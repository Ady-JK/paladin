import { existsSync, readdirSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, defineConfig } from "@playwright/test";

// playwrightCache is where `playwright install` puts browsers on this OS.
function playwrightCache(): string {
  if (process.env.PLAYWRIGHT_BROWSERS_PATH) return process.env.PLAYWRIGHT_BROWSERS_PATH;
  if (process.platform === "darwin") return path.join(os.homedir(), "Library", "Caches", "ms-playwright");
  if (process.platform === "win32") {
    return path.join(process.env.LOCALAPPDATA ?? path.join(os.homedir(), "AppData", "Local"), "ms-playwright");
  }
  return path.join(process.env.XDG_CACHE_HOME ?? path.join(os.homedir(), ".cache"), "ms-playwright");
}

// newestCachedShell finds the newest headless shell already installed,
// so a Playwright upgrade does not force a browser download.
function newestCachedShell(): string | undefined {
  const cache = playwrightCache();
  if (!existsSync(cache)) return undefined;
  const builds = readdirSync(cache)
    .filter((name) => name.startsWith("chromium_headless_shell-"))
    .sort((a, b) => Number(b.split("-")[1]) - Number(a.split("-")[1]));
  for (const build of builds) {
    for (const platformDir of readdirSync(path.join(cache, build))) {
      for (const file of ["chrome-headless-shell", "chrome-headless-shell.exe"]) {
        const candidate = path.join(cache, build, platformDir, file);
        if (existsSync(candidate)) return candidate;
      }
    }
  }
  return undefined;
}

// browserPath picks, in order: PALADIN_E2E_BROWSER, Brave on Linux dev
// boxes, Playwright's own Chromium, then any cached headless shell.
function browserPath(): string | undefined {
  const candidates = [process.env.PALADIN_E2E_BROWSER, "/usr/bin/brave-browser"];
  const explicit = candidates.find((candidate) => candidate && existsSync(candidate));
  if (explicit) return explicit;
  if (existsSync(chromium.executablePath())) return undefined;
  return newestCachedShell();
}

const executablePath = browserPath();

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:5173",
    launchOptions: executablePath ? { executablePath } : {},
  },
  webServer: {
    command: "pnpm dev:renderer",
    port: 5173,
    reuseExistingServer: true,
  },
});
