import { _electron as electron, expect, test } from "@playwright/test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

test("native website follows its surface in split and full layouts at different zoom levels", async () => {
  test.setTimeout(60_000);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-web-bounds-"));
  const app = await electron.launch({
    args: [".", `--user-data-dir=${path.join(root, "profile")}`, "--disable-gpu"],
    cwd: path.resolve(__dirname, ".."),
    env: {
      ...process.env,
      PALADIN_DATA_DIR: path.join(root, "data"),
      PALADIN_KEYSTORE: "memory",
      ELECTRON_START_URL: "http://localhost:5173",
    },
  });
  try {
    const page = await app.firstWindow();
    await page.locator("#paneWebBtn").click();
    await expect(page.locator("#webMenu")).toBeVisible();
    if (process.env.PALADIN_WEB_PROOF_DIR) {
      fs.mkdirSync(process.env.PALADIN_WEB_PROOF_DIR, { recursive: true });
      await page.screenshot({
        path: path.join(process.env.PALADIN_WEB_PROOF_DIR, "browser-menu.png"),
        animations: "disabled",
      });
    }
    await page.locator('[data-web-open="https://chatgpt.com/"]').click();
    await expect(page.locator(".web-surface")).toBeVisible();
    for (const zoom of [1, 1.2]) {
      await app.evaluate(
        ({ BrowserWindow }, factor) => BrowserWindow.getAllWindows()[0].webContents.setZoomFactor(factor),
        zoom,
      );
      for (const layout of ["split", "tabs"]) {
        if (layout === "tabs") await page.locator("#paneLayoutBtn").click();
        await expect
          .poll(async () => {
            const rect = await page.locator(".web-surface").evaluate((el) => {
              const r = el.getBoundingClientRect();
              return { x: r.x, y: r.y, width: r.width, height: r.height };
            });
            const bounds = await app.evaluate(({ BrowserWindow }) => {
              const children = BrowserWindow.getAllWindows()[0].contentView.children;
              return children[children.length - 1]?.getBounds() ?? { x: 0, y: 0, width: 0, height: 0 };
            });
            return Math.max(
              ...(["x", "y", "width", "height"] as const).map((key) => Math.abs(bounds[key] - rect[key] * zoom)),
            );
          })
          .toBeLessThan(3);
      }
      await page.locator("#paneLayoutBtn").click();
    }
  } finally {
    await app.close();
    fs.rmSync(root, { recursive: true, force: true });
  }
});
