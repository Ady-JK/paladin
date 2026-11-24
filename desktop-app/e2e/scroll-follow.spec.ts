// scroll-follow.spec.ts covers the transcript while a reply streams. A reader
// who scrolls up stays where they scrolled, even while the reply keeps
// growing the transcript; the jump-to-latest button brings them back; and a
// reader at the end keeps following the stream.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
});

// startStream sends a message whose turn stays open: the spec drives its
// handlers (window.__handlers) and ends it with window.__finish.
async function startStream(page: Page): Promise<void> {
  await page.setViewportSize({ width: 1200, height: 800 });
  await page.goto("/");
  await page.evaluate(() => {
    const w = window as any;
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__handlers = handlers;
        w.__finish = () => resolve({ reply: "", stopped: false });
      });
  });
  await page.fill("#promptInput", "think hard");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("think hard");
}

// streamThinking streams the model's thinking. The block folds at its own
// height cap, so it grows the transcript only a little.
async function streamThinking(page: Page, pieces: number): Promise<void> {
  await page.evaluate(async (n) => {
    const handlers = (window as any).__handlers;
    for (let i = 0; i < n; i++) {
      handlers.onReasoning(`step ${i}: weighing the options and writing the reasoning down in full.\n`);
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
  }, pieces);
}

// streamAnswer streams the reply itself: paragraphs that keep the transcript
// growing, the way a long answer does.
async function streamAnswer(page: Page, pieces: number): Promise<void> {
  await page.evaluate(async (n) => {
    const handlers = (window as any).__handlers;
    for (let i = 0; i < n; i++) {
      handlers.onDelta(`Paragraph ${i}: the change touches the reader's view, so it is worth a careful look.\n\n`);
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
  }, pieces);
}

const fromBottom = (page: Page): Promise<number> =>
  page.locator("#chatView").evaluate((el) => Math.round(el.scrollHeight - el.scrollTop - el.clientHeight));

test("a reader who scrolls up is not pulled back while the reply streams", async ({ page }) => {
  await startStream(page);
  await streamThinking(page, 20);
  await streamAnswer(page, 30);
  await expect.poll(() => fromBottom(page), "a fresh stream follows the end").toBeLessThan(40);

  await page.locator("#chatView").hover();
  await page.mouse.wheel(0, -500);
  await expect.poll(() => fromBottom(page)).toBeGreaterThan(200);

  await streamAnswer(page, 40);
  expect(await fromBottom(page), "more output must not drag the reader down").toBeGreaterThan(200);
  await expect(page.locator("#jumpLatestBtn")).toBeVisible();
});

test("a small scroll up is kept, not undone by the next token", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 30);
  await page.locator("#chatView").hover();
  await page.mouse.wheel(0, -20);
  await expect.poll(() => fromBottom(page)).toBeGreaterThan(5);
  await streamAnswer(page, 30);
  expect(await fromBottom(page)).toBeGreaterThan(40);
});

test("jump to latest returns the reader to the end and follows again", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 30);
  await page.locator("#chatView").hover();
  await page.mouse.wheel(0, -500);
  await streamAnswer(page, 20);
  await expect(page.locator("#jumpLatestBtn")).toBeVisible();

  await page.locator("#jumpLatestBtn").click();
  await expect.poll(() => fromBottom(page)).toBeLessThan(40);
  await expect(page.locator("#jumpLatestBtn")).toBeHidden();

  await streamAnswer(page, 30);
  await expect.poll(() => fromBottom(page), "after jumping, the stream is followed").toBeLessThan(40);
});

test("scrolling back to the end resumes following", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 30);
  await page.locator("#chatView").hover();
  await page.mouse.wheel(0, -500);
  await streamAnswer(page, 10);
  await page.mouse.wheel(0, 2000);
  await expect(page.locator("#jumpLatestBtn")).toBeHidden();

  await streamAnswer(page, 30);
  await expect.poll(() => fromBottom(page)).toBeLessThan(40);
});

test("the answer after the thinking block still lands at the end", async ({ page }) => {
  await startStream(page);
  await streamThinking(page, 40);
  // The thinking block folds away when the answer starts, shrinking the
  // transcript under a reader who was at the end.
  await streamAnswer(page, 30);
  await expect.poll(() => fromBottom(page)).toBeLessThan(40);
});

test("scrolling inside thinking does not turn off transcript follow", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 25);
  await streamThinking(page, 35);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  const body = page.locator(".think-block[open] .think-body").last();
  await body.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  await body.hover();
  await page.mouse.wheel(0, -60);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  await expect(page.locator("#jumpLatestBtn")).toBeHidden();
  await streamAnswer(page, 12);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
});

test("an idle bottom follower survives a resized transcript viewport", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 25);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  // A long-running tool can be quiet longer than the old settle observer.
  await page.waitForTimeout(500);
  await page.setViewportSize({ width: 1200, height: 620 });
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  await expect(page.locator("#jumpLatestBtn")).toBeHidden();
  await streamAnswer(page, 10);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
});

test("thinking and grouped tool updates keep following through repeated layout changes", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 25);
  for (let round = 0; round < 5; round++) {
    await streamThinking(page, 12);
    await page.evaluate((n) => {
      const handlers = (window as any).__handlers;
      for (let i = 0; i < 2; i++)
        handlers.onTool({
          id: `read-${n}-${i}`,
          name: "read",
          arguments: JSON.stringify({ path: `/tmp/file-${i}`, reason: "Inspect the next file" }),
          status: "running",
        });
    }, round);
    await page.waitForTimeout(100);
    await page.evaluate((n) => {
      const handlers = (window as any).__handlers;
      for (let i = 0; i < 2; i++)
        handlers.onTool({
          id: `read-${n}-${i}`,
          name: "read",
          arguments: JSON.stringify({ path: `/tmp/file-${i}`, reason: "Inspect the next file" }),
          result: JSON.stringify({ content: "contents", start_line: 1, end_line: 1 }),
          status: "ok",
        });
    }, round);
    await streamAnswer(page, 6);
    await expect.poll(() => fromBottom(page), `round ${round} is still following`).toBeLessThan(2);
    await expect(page.locator("#jumpLatestBtn")).toBeHidden();
  }
});

test("a layout clamp followed by new content does not look like scrolling up", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 25);
  await streamThinking(page, 25);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  await page.evaluate(() => {
    const view = document.querySelector("#chatView") as HTMLElement;
    const thinking = document.querySelector(".think-block[open]") as HTMLDetailsElement;
    thinking.open = false;
    // The browser clamps scrollTop while a tool/thinking block shrinks;
    // another row can grow before that queued scroll event is delivered.
    void view.offsetHeight;
    const output = document.createElement("div");
    output.style.height = "320px";
    document.querySelector("#messages")!.append(output);
    view.dispatchEvent(new Event("scroll"));
  });
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  await streamAnswer(page, 10);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  await expect(page.locator("#jumpLatestBtn")).toBeHidden();
});

test("a wheel event that cannot move the transcript never shows a false jump button", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 25);
  await expect.poll(() => fromBottom(page)).toBeLessThan(2);
  await page.locator("#chatView").evaluate((el) => {
    el.addEventListener("wheel", (event) => event.preventDefault(), { once: true });
    el.dispatchEvent(new WheelEvent("wheel", { deltaY: -20, bubbles: true, cancelable: true }));
  });
  expect(await fromBottom(page)).toBeLessThan(2);
  await expect(page.locator("#jumpLatestBtn")).toBeHidden();
});

test("sending a message brings a scrolled-up reader to the new turn", async ({ page }) => {
  await startStream(page);
  await streamAnswer(page, 30);
  await page.evaluate(() => (window as any).__finish());
  await expect(page.locator("#sendBtn")).toBeEnabled();

  await page.locator("#chatView").hover();
  await page.mouse.wheel(0, -500);
  await page.fill("#promptInput", "and now?");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("and now?");
  await streamAnswer(page, 20);
  await expect.poll(() => fromBottom(page)).toBeLessThan(40);
});
