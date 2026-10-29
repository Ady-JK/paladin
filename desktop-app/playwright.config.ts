import { existsSync } from "node:fs";
import { defineConfig } from "@playwright/test";

// Brave on Linux dev boxes; elsewhere fall back to Playwright's Chromium.
const brave = process.env.PALADIN_E2E_BROWSER ?? "/usr/bin/brave-browser";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:5173",
    launchOptions: existsSync(brave) ? { executablePath: brave } : {},
  },
  webServer: {
    command: "pnpm dev:renderer",
    port: 5173,
    reuseExistingServer: true,
  },
});
