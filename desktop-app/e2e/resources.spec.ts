// resources.spec.ts covers per-session CPU/RAM: the core's `resources`
// snapshot plus Electron's own metrics, shown on session rows, in the
// header, the sidebar footer and a Settings pane.
import { test, expect, type Page } from "@playwright/test";

const MB = 1024 * 1024;

async function setup(page: Page, snap: unknown, app: unknown = { cpu: 4, rss: 200 * MB, procs: 5 }): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    ([snap, app]) => {
      const w = window as any;
      w.__paladinResInterval = 150;
      w.__res = snap;
      w.__resCalls = 0;
      const base = w.__paladinBaseInvoke;
      w.__paladinBaseInvoke = async (cmd: string, args: any) => {
        if (cmd === "resources") {
          w.__resCalls++;
          return w.__res;
        }
        if (cmd === "listSessions") {
          const at = Math.floor(Date.now() / 1000);
          return [
            { sessionId: "sess-busy", cwd: "/home/tester/project", createdAt: at, provider: "p", model: "m", messageCount: 2, title: "busy one" },
            { sessionId: "sess-idle", cwd: "/home/tester/project", createdAt: at - 5, provider: "p", model: "m", messageCount: 2, title: "idle one" },
          ];
        }
        if (cmd === "openSession") {
          return { sessionId: args.sessionId, cwd: "/home/tester/project", provider: "p", model: "m" };
        }
        if (cmd === "sessionHistory") return [];
        return base(cmd, args);
      };
      w.paladin.metrics = async () => w.__app;
      w.__app = app;
    },
    [snap, app] as const,
  );
  await page.goto("/");
  await expect(page.locator('[data-session="sess-busy"]')).toBeVisible();
}

const SNAP = {
  at: 1,
  supported: true,
  self: { cpu: 1, rss: 50 * MB, procs: 1 },
  sessions: { "sess-busy": { cpu: 20, rss: 100 * MB, procs: 3 } },
};

test("the footer shows the app total: Electron + core + session trees", async ({ page }) => {
  await setup(page, SNAP);
  await expect(page.locator("#resTotal")).toBeVisible();
  await expect(page.locator("#resTotalLabel")).toHaveText("25% · 350 MB");
});

test("a session row shows its own usage only while it has processes", async ({ page }) => {
  await setup(page, SNAP);
  const busy = page.locator('.session-row:has([data-session="sess-busy"]) .session-res');
  await expect(busy).toHaveText("20% · 100 MB");
  await expect(busy).toHaveAttribute("title", /3 processes/);
  await expect(page.locator('.session-row:has([data-session="sess-idle"]) .session-res')).toHaveCount(0);
});

test("row badges update live without rebuilding the list", async ({ page }) => {
  await setup(page, SNAP);
  const row = page.locator('.session-row:has([data-session="sess-busy"])');
  await expect(row.locator(".session-res")).toHaveText("20% · 100 MB");
  await row.evaluate((el) => ((el as any).__keep = true));
  await page.evaluate(() => {
    (window as any).__res = {
      at: 2,
      supported: true,
      self: { cpu: 1, rss: 50 * 1024 * 1024, procs: 1 },
      sessions: { "sess-busy": { cpu: 150, rss: 1536 * 1024 * 1024, procs: 4 } },
    };
  });
  await expect(row.locator(".session-res")).toHaveText("150% · 1.5 GB");
  expect(await row.evaluate((el) => (el as any).__keep)).toBe(true);
  await page.evaluate(() => ((window as any).__res = { at: 3, supported: true, self: { cpu: 1, rss: 1, procs: 1 }, sessions: {} }));
  await expect(row.locator(".session-res")).toHaveCount(0);
});

test("the header pill follows the open session", async ({ page }) => {
  await setup(page, SNAP);
  await page.click('[data-session="sess-busy"]');
  await expect(page.locator("#resPill")).toBeVisible();
  await expect(page.locator("#resPillLabel")).toHaveText("20% · 100 MB");
  await page.click('[data-session="sess-idle"]');
  await expect(page.locator("#resPillLabel")).toHaveText("idle");
});

test("settings lists the app, the core and each busy session", async ({ page }) => {
  await setup(page, SNAP);
  await page.click("#settingsBtn");
  await page.click("#settingsNavResources");
  const body = page.locator("#resourcesBody");
  await expect(body.locator('[data-res-row="app"]')).toContainText("200 MB");
  await expect(body.locator('[data-res-row="core"]')).toContainText("50 MB");
  await expect(body.locator('[data-res-row="sess-busy"]')).toContainText("busy one");
  await expect(body.locator('[data-res-row="sess-busy"]')).toContainText("20%");
  await expect(body.locator('[data-res-row="total"]')).toContainText("350 MB");
});

test("an unsupported core hides the figures instead of showing zeros", async ({ page }) => {
  await setup(page, { at: 1, supported: false, self: { cpu: 0, rss: 0, procs: 0 }, sessions: {} }, null);
  await page.waitForFunction(() => (window as any).__resCalls > 0);
  await expect(page.locator("#resTotal")).toBeHidden();
  await page.click('[data-session="sess-busy"]');
  await expect(page.locator("#resPill")).toBeHidden();
});

test("polling pauses while the window is hidden", async ({ page }) => {
  await setup(page, SNAP);
  await page.waitForFunction(() => (window as any).__resCalls > 0);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.waitForTimeout(200);
  const before = await page.evaluate(() => (window as any).__resCalls);
  await page.waitForTimeout(600);
  expect(await page.evaluate(() => (window as any).__resCalls)).toBe(before);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => page.evaluate(() => (window as any).__resCalls)).toBeGreaterThan(before);
});
