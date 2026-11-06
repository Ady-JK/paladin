import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("sending a message opens a session and shows the reply", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  await expect(page.locator("#messages")).toContainText("hello agent");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello agent");

  // The empty-state hero vanishes so the first user turn docks at the top.
  await expect(page.locator("#messages .greet-hero")).toHaveCount(0);
  const first = page.locator("#messages .turn-row").first();
  await expect(first).toContainText("hello agent");

  const chats = await page.evaluate(() => (window as any).__e2eChats);
  expect(chats.length).toBeGreaterThanOrEqual(1);
  expect(chats[0].message).toBe("hello agent");
  expect(chats[0].resume).toBe(false);
});

test("agent markdown renders while the user bubble stays plain", async ({ page }) => {
  await page.evaluate(() => {
    const prev = (window as any).__e2eRunTurn;
    (window as any).__e2eRunTurn = async (params: any, handlers: any) => {
      const out = await prev(params, handlers);
      return {
        ...out,
        reply: "# Ship it\n\nRun **tests** and *lint*:\n\n- unit\n- e2e\n\n> go fast\n\n[docs](https://example.com)",
      };
    };
  });
  await page.fill("#promptInput", "explain **boldly**");
  await page.click("#sendBtn");

  const rows = page.locator("#messages .turn-row");
  await expect(rows.nth(1).locator("h1")).toHaveText("Ship it");
  await expect(rows.nth(1).locator("strong").first()).toHaveText("tests");
  await expect(rows.nth(1).locator("em").first()).toHaveText("lint");
  await expect(rows.nth(1).locator("ul li")).toHaveCount(2);
  await expect(rows.nth(1).locator("blockquote")).toContainText("go fast");
  await expect(rows.nth(1).locator('a[data-ext="https://example.com"]')).toHaveText("docs");

  // The user bubble shows exactly what was typed: no Markdown parsing.
  await expect(rows.nth(0).locator("strong")).toHaveCount(0);
  await expect(rows.nth(0)).toContainText("explain **boldly**");
});

test("the conversation and composer use the full pane width", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages .turn-row").first()).toBeVisible();

  const width = async (sel: string): Promise<number> => (await page.locator(sel).first().boundingBox())!.width;
  const chat = await width("#chatView");
  const messages = await width("#messages");
  const row = await width("#messages .turn-row");
  const composer = await width("footer > div");
  expect(row).toBeCloseTo(messages, 0);
  expect(composer).toBeCloseTo(messages, 0);
  // Only the 24px gutters (and a possible scrollbar) may remain.
  expect(messages).toBeGreaterThan(chat - 90);
});

test("wide tables scroll sideways and tall tables scroll vertically", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.evaluate(() => {
    const prev = (window as any).__e2eRunTurn;
    (window as any).__e2eRunTurn = async (params: any, handlers: any) => {
      const out = await prev(params, handlers);
      const heads = Array.from({ length: 8 }, (_, i) => `A rather long heading number ${i}`);
      const sep = heads.map(() => "---");
      const head = `| ${heads.join(" | ")} |\n| ${sep.join(" | ")} |`;
      const cells = (n: number) => heads.map((_, i) => `cell ${n}-${i}`).join(" | ");
      const wide = `${head}\n| ${cells(1)} |`;
      const tall = `${head}\n${Array.from({ length: 12 }, (_, i) => `| ${cells(i)} |`).join("\n")}`;
      return { ...out, reply: `Wide\n\n${wide}\n\nTall\n\n${tall}` };
    };
  });
  await page.fill("#promptInput", "show tables");
  await page.click("#sendBtn");

  const wraps = page.locator("#messages .turn-row").nth(1).locator(".md-table-wrap");
  await expect(wraps).toHaveCount(2);
  const sizes = await wraps.evaluateAll((els) =>
    els.map((el) => ({
      tall: el.classList.contains("is-tall"),
      scrollWidth: el.scrollWidth,
      clientWidth: el.clientWidth,
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    })),
  );
  const wide = sizes[0];
  const tallSize = sizes[1];
  if (!wide || !tallSize) throw new Error("expected two table wrappers");
  expect(wide.tall).toBe(false);
  expect(wide.scrollWidth).toBeGreaterThan(wide.clientWidth);
  expect(tallSize.tall).toBe(true);
  expect(tallSize.scrollHeight).toBeGreaterThan(tallSize.clientHeight);
});
