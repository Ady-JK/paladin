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

// widths samples the panel once per frame. Playwright's boundingBox()
// returns null for a visibility:hidden element, so the reads happen in the
// page: a closed drawer still has to report 0 rather than "not found".
async function widths(page: Page, selector: string, ms: number): Promise<number[]> {
  return page.evaluate(
    async ({ selector, ms }) => {
      const out: number[] = [];
      const started = performance.now();
      while (performance.now() - started < ms) {
        const el = document.querySelector<HTMLElement>(selector);
        out.push(el ? Math.round(el.getBoundingClientRect().width) : -1);
        await new Promise((r) => requestAnimationFrame(() => r(null)));
      }
      return out;
    },
    { selector, ms },
  );
}

// slid measures the panel for `ms` after the click and returns the widths it
// passed through. A snap shows at most two values: the start and the end.
async function slid(page: Page, selector: string, click: () => Promise<void>): Promise<number[]> {
  const done = click();
  const frames = await widths(page, selector, 300);
  await done;
  return frames;
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
  await page.locator("#ideExplorerToggle").click();
  await page.locator(".ide-explorer").waitFor();
  await page.waitForTimeout(250);
  const closing = await slid(page, ".ide-explorer", () => page.locator("#ideExplorerToggle").click());
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
