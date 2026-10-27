import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:5173",
    launchOptions: {
      executablePath: "/usr/bin/brave-browser",
    },
  },
  webServer: {
    command: "pnpm dev:renderer",
    port: 5173,
    reuseExistingServer: true,
  },
});
