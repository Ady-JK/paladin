// drawers.spec.ts pins the rail drawers as one family: the sessions and files
// panels must be the same width and must animate both ways.
//
// The close half is the regression this file exists for. Each drawer's
// closed rule sets width, max-width and border-width at once; when a
// transition list covered only `width`, the other two snapped to zero on the
// first frame, clamped the width change, and the panel vanished instead of
// sliding shut — while the open direction looked fine.
import { expect, test, type Page } from "@playwright/test";

async function start(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

// sampleStart begins sampling the panel once per frame, in the page, and
// keeps going until sampleStop. Playwright's boundingBox() returns null for a
// visibility:hidden element, so the reads happen in the page: a closed drawer
// still has to report 0 rather than "not found".
async function sampleStart(page: Page, selector: string): Promise<void> {
  await page.evaluate((selector) => {
    const w = window as unknown as { __frames: number[]; __sampling: boolean };
    w.__frames = [];
    w.__sampling = true;
    const tick = (): void => {
      if (!w.__sampling) return;
      const el = document.querySelector<HTMLElement>(selector);
      w.__frames.push(el ? Math.round(el.getBoundingClientRect().width) : -1);
      requestAnimationFrame(tick);
    };
    tick();
  }, selector);
}

async function sampleStop(page: Page): Promise<number[]> {
  return page.evaluate(() => {
    const w = window as unknown as { __frames: number[]; __sampling: boolean };
    w.__sampling = false;
    return w.__frames;
  });
}

// slid samples the panel from before the click until `ms` after it resolves
// and returns the widths it passed through. Sampling does not depend on how
// long Playwright takes to deliver the click, so a slow click cannot hide the
// animation. A snap shows at most two values: the start and the end.
async function slid(page: Page, selector: string, click: () => Promise<void>, ms = 300): Promise<number[]> {
  await sampleStart(page, selector);
  await click();
  await page.waitForTimeout(ms);
  return sampleStop(page);
}

function intermediate(frames: number[]): number[] {
  const first = frames[0];
  const last = frames[frames.length - 1];
  return frames.filter((w) => w !== first && w !== last);
}

test("the sessions drawer animates both open and closed", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  const rail = page.locator("#agentTab");
  const opening = await slid(page, "#sidebar", () => rail.click());
  // Opens from 0 with intermediate widths.
  expect(opening[0]).toBeLessThan(20);
  expect(intermediate(opening).length).toBeGreaterThan(2);

  await page.waitForTimeout(250);
  const closing = await slid(page, "#sidebar", () => rail.click());
  expect(closing[closing.length - 1]).toBe(0);
  expect(intermediate(closing).length, "closing must not snap to zero").toBeGreaterThan(2);
});

test("the files explorer animates both open and closed", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  const rail = page.locator("#ideExplorerToggle");
  // The first click lazily loads the IDE module, mounts the explorer in the
  // shell, and animates it open from width 0 across intermediate widths.
  // Dynamic import on CI/cold run can take ~200ms before animation starts, so
  // measure across a slightly longer window.
  const opening = await slid(page, ".ide-explorer", () => rail.click(), 600);
  const valid = opening.filter((w) => w >= 0);
  expect(valid[0], "first open must start from zero width").toBeLessThan(40);
  expect(intermediate(valid).length, "first open must animate with intermediate widths").toBeGreaterThan(2);

  await page.waitForTimeout(250);
  const closing = await slid(page, ".ide-explorer", () => rail.click());
  // It ends collapsed — a couple of pixels of border may remain, the panel
  // itself must be gone — and it must have passed through intermediate
  // widths on the way, not cut.
  expect(closing[closing.length - 1]).toBeLessThan(40);
  expect(intermediate(closing).length, "closing must not snap to zero").toBeGreaterThan(2);
  await expect(page.locator(".ide-explorer")).toHaveClass(/is-closed/);
});

test("the two rail drawers are the same width and animate both ways", async ({ page }) => {
  await page.setViewportSize({ width: 1500, height: 900 });
  await start(page);
  const drawerWidth = async (selector: string): Promise<number> =>
    page.locator(selector).evaluate((el) => Math.round(el.getBoundingClientRect().width));

  // Sessions.
  await page.locator("#agentTab").click();
  await page.waitForTimeout(300);
  const sessions = await drawerWidth("#sidebar");
  await page.locator("#agentTab").click();
  await page.waitForTimeout(300);

  // Files.
  await page.locator("#ideExplorerToggle").click();
  await page.locator(".ide-explorer").waitFor();
  await page.waitForTimeout(300);
  const files = await drawerWidth(".ide-explorer");

  expect(sessions).toBeGreaterThan(200);
  expect(files, "Files matches Sessions").toBe(sessions);
});

// Visible-while-moving: width alone cannot tell a slide from a blank box
// sliding. Both of these shipped broken with the width probes above green.
async function paint(page: Page, selector: string, ms: number): Promise<{ visibility: string; kid: string }[]> {
  return page.evaluate(
    async ({ selector, ms }) => {
      const out: { visibility: string; kid: string }[] = [];
      const started = performance.now();
      while (performance.now() - started < ms) {
        const el = document.querySelector<HTMLElement>(selector)!;
        const kid = el.firstElementChild as HTMLElement;
        const width = el.getBoundingClientRect().width;
        if (width > 40)
          out.push({ visibility: getComputedStyle(el).visibility, kid: getComputedStyle(kid).visibility });
        await new Promise((r) => requestAnimationFrame(() => r(null)));
      }
      return out;
    },
    { selector, ms },
  );
}

test("the side pane is visible while + slides it open and keeps its contents while it closes", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  const opening = paint(page, "#sidePane", 400);
  await page.click("#paneNewChatBtn");
  const open = await opening;
  expect(open.length).toBeGreaterThan(2);
  expect(
    open.every((f) => f.visibility === "visible"),
    "the pane must not stay hidden until the slide ends",
  ).toBe(true);

  await page.waitForTimeout(250);
  const closing = paint(page, "#sidePane", 400);
  await page.click("#paneCloseBtn");
  const closed = await closing;
  expect(closed.length).toBeGreaterThan(2);
  expect(
    closed.every((f) => f.kid === "visible"),
    "contents must stay painted until the pane is shut",
  ).toBe(true);
});

test("the files explorer keeps its contents painted while it slides shut", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.click("#ideExplorerToggle");
  await expect.poll(async () => (await page.locator(".ide-explorer").boundingBox())?.width ?? 0).toBeGreaterThan(300);
  const closing = paint(page, ".ide-explorer", 400);
  await page.click("#ideExplorerToggle");
  const frames = await closing;
  expect(frames.length, "closing must pass through several widths").toBeGreaterThan(2);
  expect(frames.every((f) => f.kid === "visible")).toBe(true);
});
