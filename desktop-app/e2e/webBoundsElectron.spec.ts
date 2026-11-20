// webBoundsElectron.spec.ts drives a real Electron window, because the thing
// it checks only exists there: a native page is a WebContentsView positioned
// by pixel bounds in the main process, so no stub can show whether it lines
// up with the HTML surface it is supposed to cover. The failure it guards
// against is a page left behind by an animation: a view does not inherit a
// CSS transition, so without per-frame repositioning it stays at its old
// rectangle and sticks out past the panel it belongs to.
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

// surfaceOf reads the page's CSS rectangle, and boundsOf the native view's
// pixel rectangle in the main process.
async function surfaceOf(app: Awaited<ReturnType<typeof electron.launch>>): Promise<number[]> {
  const page = await app.firstWindow();
  return page.locator(".web-surface").evaluate((el) => {
    const r = el.getBoundingClientRect();
    return [r.x, r.y, r.width, r.height];
  });
}
async function boundsOf(app: Awaited<ReturnType<typeof electron.launch>>): Promise<number[]> {
  return app.evaluate(({ BrowserWindow }) => {
    const children = BrowserWindow.getAllWindows()[0].contentView.children;
    const b = children[children.length - 1]?.getBounds();
    return b ? [b.x, b.y, b.width, b.height] : [0, 0, 0, 0];
  });
}

test("the native page keeps up with its surface while a panel slides", async () => {
  // Regression: the sessions drawer pushed the card and the page stayed at
  // its old rectangle for the whole 200ms transition — up to ~176px out of
  // place at 1920 wide, which paints the page over its neighbours. A view
  // cannot be clipped and does not inherit a CSS transition, so it has to
  // be repositioned every frame the surface moves.
  test.setTimeout(90_000);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-anim-bounds-"));
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
    await page.locator('[data-web-open="https://chatgpt.com/"]').click();
    await expect(page.locator(".web-surface")).toBeVisible();
    // This spec describes the pane beside the conversation being pushed by a
    // drawer, which is the split layout; the app opens in tabs.
    if ((await page.locator("#paneLayoutBtn").getAttribute("data-layout")) !== "split") {
      await page.locator("#paneLayoutBtn").click();
    }
    await expect(page.locator("#paneLayoutBtn")).toHaveAttribute("data-layout", "split");
    // Wait for the page to be up at its final width, not mid-attach, or the
    // "it widened" assertion below compares against a half-placed view.
    await expect.poll(() => boundsOf(app).then((b) => b[2])).toBeGreaterThan(400);

    // Record every rectangle the page is actually given while the drawer
    // opens. Reading them from the main process afterwards keeps the trace
    // out of the race the sampler had: page and window are two processes,
    // and a sample taken across an await can compare a new surface with an
    // older bounds.
    const before = (await surfaceOf(app))[0];
    await app.evaluate(({ BrowserWindow }) => {
      const w = globalThis as unknown as { __moves: Array<{ x: number; at: number }> };
      w.__moves = [];
      const view = BrowserWindow.getAllWindows()[0].contentView.children.at(-1)!;
      const original = view.setBounds.bind(view);
      view.setBounds = (b: Electron.Rectangle) => {
        w.__moves.push({ x: b.x, at: Date.now() });
        original(b);
      };
    });
    await page.locator("#agentTab").click();
    await page.waitForTimeout(900);
    const moves = await app.evaluate(({ BrowserWindow }) => {
      const w = globalThis as unknown as { __moves: Array<{ x: number; at: number }> };
      return { moves: w.__moves, settled: BrowserWindow.getAllWindows()[0].contentView.children.at(-1)!.getBounds() };
    });

    const xs = moves.moves.map((m) => m.x);
    // Several in-between positions, not one jump from closed to open: that
    // is what makes the page look attached to the panel while it slides.
    // Without per-frame following the only move arrives after the 200ms
    // transition has already finished, so a single rect at the end would
    // satisfy "it ends in the right place" while looking broken throughout.
    expect(new Set(xs).size).toBeGreaterThan(2);
    // The first moves happen while the panel is still travelling. Counted by
    // position in the trace, not by wall clock: on a loaded machine the
    // first frames can be late and a time window reads as "no movement"
    // even though the page is tracking the panel frame by frame.
    const settledX = moves.settled.x;
    const travelling = xs.filter((x) => x !== settledX);
    expect(travelling.length).toBeGreaterThan(0);
    // And it ends exactly on the surface's own rectangle.
    const after = await surfaceOf(app);
    expect(Math.abs(moves.settled.x - after[0])).toBeLessThan(3);
    expect(after[0]).toBeGreaterThan(before + 50);
  } finally {
    await app.close();
    fs.rmSync(root, { recursive: true, force: true });
  }
});
