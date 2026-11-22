// splitresize.spec.ts covers the draggable border between the conversation
// and the side pane: it resizes, it remembers the width, it clamps, and a
// double-click returns it to half.
import { expect, test, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";
import { hidePane, showPane, addToPane } from "./modes";

async function openPane(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
  // The border and the half/half split only exist in split layout, which the
  // app no longer opens in.
  await showPane(page);
  await addToPane(page, "chat");
  await expect(page.locator("#sidePane")).toBeVisible();
  // The pane animates open now; drag geometry must be read settled, or the
  // handle moves under the cursor mid-drag. Settled = two equal reads.
  await expect
    .poll(
      async () => {
        const first = await paneWidth(page);
        await page.waitForTimeout(120);
        const second = await paneWidth(page);
        return Math.abs(first - second) < 1;
      },
      { timeout: 4000 },
    )
    .toBe(true);
}

const paneWidth = (page: Page): Promise<number> =>
  page.locator("#sidePane").evaluate((el) => el.getBoundingClientRect().width);

test("the pane opens at half the split by default", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPane(page);
  const available = await page.locator("#agentSplit").evaluate((el) => el.getBoundingClientRect().width);
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeGreaterThan(available * 0.45);
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeLessThan(available * 0.55);
});

test("opening a panel while the pane is closed keeps the split at half", async ({ page }) => {
  // Regression: a window resize used to re-measure the pane. A closed pane
  // is 0px wide, so any panel dispatching "resize" while it was shut (the
  // sessions drawer does exactly that) saved "0" and the pane reopened at
  // its minimum width instead of half.
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPane(page);
  await hidePane(page);
  await expect(page.locator("#sidePane")).toBeHidden();
  await showSidebar(page);
  await page.locator("#agentTab").click();
  await showPane(page);
  await expect(page.locator("#sidePane")).toBeVisible();
  const available = await page.locator("#agentSplit").evaluate((el) => el.getBoundingClientRect().width);
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeGreaterThan(available * 0.4);
});

test("dragging the border resizes the side pane", async ({ page }) => {
  // Wide enough that the conversation's minimum width leaves room to widen the pane.
  await page.setViewportSize({ width: 1900, height: 900 });
  await openPane(page);
  const before = await paneWidth(page);
  const handle = page.locator("#paneSplitResize");
  await expect(handle).toBeVisible();
  const box = (await handle.boundingBox())!;

  await page.mouse.move(box.x + box.width / 2, box.y + 60);
  await page.mouse.down();
  await page.mouse.move(box.x - 120, box.y + 60, { steps: 8 });
  // While the pointer is down only the preview moves: a native browser
  // page cannot be re-laid out per frame without lagging.
  await expect(page.locator(".pane-split-preview")).toBeVisible();
  expect(await paneWidth(page)).toBe(before);
  await page.mouse.up();

  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeGreaterThan(before + 80);
  await expect(page.locator(".pane-split-preview")).toBeHidden();
});

test("the pane width survives a reload", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPane(page);
  const handle = page.locator("#paneSplitResize");
  const box = (await handle.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + 60);
  await page.mouse.down();
  await page.mouse.move(box.x - 150, box.y + 60, { steps: 6 });
  await page.mouse.up();
  const widened = await paneWidth(page);
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeGreaterThan(widened - 5);

  await page.reload();
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
  await addToPane(page, "chat");
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeGreaterThanOrEqual(widened - 2);
});

test("the pane cannot swallow the conversation", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPane(page);
  const handle = page.locator("#paneSplitResize");
  const box = (await handle.boundingBox())!;
  // Drag far left: the cap must leave the chat usable.
  await page.mouse.move(box.x + box.width / 2, box.y + 60);
  await page.mouse.down();
  await page.mouse.move(10, box.y + 60, { steps: 6 });
  await page.mouse.up();
  const available = await page.locator("#agentSplit").evaluate((el) => el.getBoundingClientRect().width);
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeLessThanOrEqual(available * 0.8 + 1);
  const chat = await page.locator("#agentMain").evaluate((el) => el.getBoundingClientRect().width);
  expect(chat).toBeGreaterThan(300);
});

test("double-clicking the border returns the split to half", async ({ page }) => {
  // Wide enough that the conversation's minimum width leaves room to widen the pane.
  await page.setViewportSize({ width: 1900, height: 900 });
  await openPane(page);
  const handle = page.locator("#paneSplitResize");
  const box = (await handle.boundingBox())!;
  const available = await page.locator("#agentSplit").evaluate((el) => el.getBoundingClientRect().width);
  await page.mouse.move(box.x + box.width / 2, box.y + 60);
  await page.mouse.down();
  await page.mouse.move(box.x - 200, box.y + 60, { steps: 6 });
  await page.mouse.up();
  // The pane's width animates to the committed split; wait for it to
  // settle before measuring, or the read lands at the animation's start.
  await expect
    .poll(
      async () => {
        const first = await paneWidth(page);
        await page.waitForTimeout(120);
        const second = await paneWidth(page);
        return Math.abs(first - second) < 1 ? second : -1;
      },
      { timeout: 4000 },
    )
    .toBeGreaterThan(available * 0.5 + 50);
  const widened = await paneWidth(page);
  expect(widened).toBeGreaterThan(available * 0.5 + 50);

  await handle.dblclick();
  await expect.poll(() => paneWidth(page), { timeout: 4000 }).toBeLessThan(widened - 40);
});

test("the border is hidden while the pane is closed", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPane(page);
  await expect(page.locator("#paneSplitResize")).toBeVisible();
  await page.locator("#paneSplitResize").focus();
  await page.mouse.move(0, 0);
});
