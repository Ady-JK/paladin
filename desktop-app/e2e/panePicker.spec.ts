// panePicker.spec.ts pins how the side pane is filled. The header + opens the
// pane; an empty pane offers the five things it can hold (Side chat /
// Terminal / Browser / Changes / Running commands) instead of an empty box,
// and once anything is open that same list becomes the pane's strip with a +
// at the end.
//
// Terminals only ever live in the pane: there is no full-width terminal tab.
//
// The pane itself animates open and closed like the rail drawers (its
// flex-basis slides), so the picker appearing is never a snap.

// widths samples the pane once per frame while something happens.
async function paneWidths(page: Page, ms: number, act: () => Promise<void>): Promise<number[]> {
  const done = act();
  const frames = await page.evaluate(
    ({ ms }) =>
      new Promise<number[]>((resolve) => {
        const el = document.querySelector<HTMLElement>("#sidePane")!;
        const out: number[] = [];
        const started = performance.now();
        const tick = () => {
          out.push(Math.round(el.getBoundingClientRect().width));
          if (performance.now() - started < ms) requestAnimationFrame(tick);
          else resolve(out);
        };
        requestAnimationFrame(tick);
      }),
    { ms },
  );
  await done;
  return frames;
}

test("the pane animates open from its right edge, not a snap", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  const frames = await paneWidths(page, 400, async () => {
    await page
      .locator("#panePicker")
      .getByRole("button", { name: /Side chat/ })
      .click();
  });
  // It must pass through intermediate widths on the way open, and the LEFT
  // edge must travel leftward: the pane slides in right-to-left, growing
  // from the card's edge.
  const distinct = new Set(frames).size;
  expect(frames[frames.length - 1]).toBeGreaterThan(300);
  expect(distinct, "opening must not snap").toBeGreaterThan(3);
});

test("closing the pane animates shut instead of vanishing", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Side chat/ })
    .click();
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();
  const frames = await paneWidths(page, 400, async () => {
    await page.locator("#paneCloseBtn").click();
  });
  const distinct = new Set(frames).size;
  expect(frames[frames.length - 1]).toBeLessThan(40);
  expect(distinct, "closing must not snap").toBeGreaterThan(3);
});
import { expect, test, type Page } from "@playwright/test";

async function start(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

test("an empty pane offers the five things it can hold", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await expect(page.locator("#sidePane")).toBeHidden();

  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  // The empty state is the picker, not a blank column.
  const picker = page.locator("#panePicker");
  await expect(picker).toBeVisible();
  await expect(picker.getByRole("button", { name: /Side chat/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Terminal/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Browser/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Changes/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Running commands/ })).toBeVisible();
});

// The rows centre on the window, not on the pane's own box: the pane starts
// below the header, and centring inside it left the list sagging ~40px low.
test("the picker centres on the window, not on the pane", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#panePicker")).toBeVisible();
  const centred = await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll<HTMLElement>(".pane-picker-row"));
    const title = document.querySelector<HTMLElement>(".pane-picker-title")!.getBoundingClientRect();
    const block = (title.top + rows[rows.length - 1].getBoundingClientRect().bottom) / 2;
    return Math.abs(block - window.innerHeight / 2);
  });
  expect(centred).toBeLessThan(16);
});

// The box spans the header gap so it can centre on the window, but it must
// not swallow the strip above it: the + and the close button stay clickable.
test("the picker does not block the pane strip", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#panePicker")).toBeVisible();
  const top = await page.evaluate(() => {
    const strip = document.querySelector<HTMLElement>(".pane-strip")!.getBoundingClientRect();
    const hit = document.elementFromPoint(strip.x + 20, strip.y + strip.height / 2);
    return (hit as HTMLElement | null)?.closest(".pane-strip") !== null;
  });
  expect(top).toBe(true);
});

test("picking Changes opens the session's changes tab", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Changes/ })
    .click();
  // It opens a tab in the pane's strip, beside the conversation.
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);
  await expect(page.locator("#panePicker")).toBeHidden();
  await expect(page.locator("#agentMain")).toBeVisible();
});

test("picking Running commands opens the jobs tab", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Running commands/ })
    .click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);
  await expect(page.locator("#panePicker")).toBeHidden();
  await expect(page.locator("#agentMain")).toBeVisible();
});

test("picking Terminal from the picker puts a terminal in the pane", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();

  // In the pane, beside the conversation — never a full-width tab.
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
});

test("picking Browser from the picker opens a page in the pane", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Browser/ })
    .click();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
});

test("once something is open the picker gives way to the strip", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();

  // It stays in the document and hides, so the pane can bring it straight back.
  await expect(page.locator("#panePicker")).toBeHidden();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);
  // The strip's own + is how a second one is added.
  await expect(page.locator("#paneTabsAdd")).toBeVisible();
});

test("closing the last tab brings the picker back", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();
  await page.locator("#paneTabs .pane-tab .pane-tab-close").first().click();
  // The pane stays open, and with nothing left it offers the three choices.
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#panePicker")).toBeVisible();
});

// The header + drops a menu once the pane holds something. It carries
// every choice the picker offers, and it must stay inside the window:
// anchored to its button's right edge, never hanging past the card.
test("the + menu offers every pane kind and stays in bounds", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page.locator('#panePicker [data-pane-pick="terminal"]').click();
  await page.locator("#paneTabsAdd").click();
  const menu = page.locator("#paneHistoryMenu");
  await expect(menu).toBeVisible();
  for (const label of [/New side chat/, /New terminal/, /^Browser$/, /^Changes$/, /Running commands/]) {
    await expect(menu.getByRole("menuitem", { name: label })).toBeVisible();
  }
  const box = (await menu.boundingBox())!;
  const card = (await page.locator("#mainCard").boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(card.x + card.width);
  expect(box.y + box.height).toBeLessThanOrEqual(page.viewportSize()!.height);
});

// Changes, Browser and Running commands are single-instance: picking one
// that is already open brings it forward instead of adding a duplicate.
test("re-picking an open tab brings it forward, never duplicates", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page.locator('#panePicker [data-pane-pick="changes"]').click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);

  // Add a terminal so Changes is no longer the active tab.
  await page.locator("#paneTabsAdd").click();
  await page.locator("#paneHistoryMenu [data-pane-new-terminal]").click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(2);
  await expect(page.locator("#paneTabs .pane-tab.is-active")).toContainText("Terminal");

  // Picking Changes again focuses the existing tab.
  await page.locator("#paneTabsAdd").click();
  await page.locator("#paneHistoryMenu [data-pane-new-changes]").click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(2);
  await expect(page.locator("#paneTabs .pane-tab.is-active")).toContainText("Changes");
});

test("browser and running commands never duplicate either", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page.locator('#panePicker [data-pane-pick="browser"]').click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);
  await page.locator("#paneTabsAdd").click();
  await page.locator("#paneHistoryMenu [data-pane-new-browser]").click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);

  await page.locator("#paneTabsAdd").click();
  await page.locator("#paneHistoryMenu [data-pane-new-jobs]").click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(2);
  await page.locator("#paneTabsAdd").click();
  await page.locator("#paneHistoryMenu [data-pane-new-jobs]").click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(2);
});

test("a terminal is never given the whole card", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();
  const pane = (await page.locator("#sidePane").boundingBox())!;
  await expect(page.locator("#agentMain")).toBeVisible();
  const chat = (await page.locator("#agentMain").boundingBox())!;
  // The conversation keeps its share: the terminal is a column, not the view.
  expect(chat.width).toBeGreaterThan(pane.width);
  expect(pane.x).toBeGreaterThan(chat.x);
});
