import { test, expect } from "@playwright/test";

import { showPane } from "./modes";

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

test("the conversation and composer share a centered reading width", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  // Split layout narrows the conversation, which is what gives it a reading
  // width instead of the whole window.
  await showPane(page);
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
  // Centered and inset from its column, rather than the exact pixels of one
  // split ratio: the two side gaps are both present and roughly equal.
  const chatBox = (await page.locator("#chatView").first().boundingBox())!;
  const msgBox = (await page.locator("#messages").first().boundingBox())!;
  const left = msgBox.x - chatBox.x;
  const right = chatBox.x + chatBox.width - (msgBox.x + msgBox.width);
  expect(left).toBeGreaterThan(20);
  expect(right).toBeGreaterThan(20);
  expect(Math.abs(left - right)).toBeLessThan(4);
  expect(messages).toBeLessThan(chat);
  expect(messages).toBeLessThanOrEqual(1100);
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

// A commit hash or a `/command` in a table cell stays whole. The
// transcript sets overflow-wrap: anywhere so prose can break a long
// path, which used to let the browser squeeze a column to a sliver and
// split "61b12f2" into "61b12f" + "2" on the next line.
test("a token inside a table cell never splits mid-word", async ({ page }) => {
  await page.evaluate(() => {
    const prev = (window as any).__e2eRunTurn;
    (window as any).__e2eRunTurn = async (params: any, handlers: any) => {
      const out = await prev(params, handlers);
      const table = [
        "| Commit | Version | Change |",
        "|---|---|---|",
        "| `61b12f2` | 0.10.53 | MCP servers start as soon as their config appears, so the new-tools popup no longer waits for a message |",
        "| `2eabb79` | 0.10.54 | Claude Code and Agent Skills directories are read, appearing in the `/` palette with origin badges (`Claude`, `Claude · project`); Paladin's own skills always win a name |",
      ].join("\n");
      return { ...out, reply: table };
    };
  });
  await page.fill("#promptInput", "show the table");
  await page.click("#sendBtn");

  const cells = page.locator("#messages .turn-row").nth(1).locator(".md-td");
  await expect(cells).toHaveCount(6);

  // Every inline-code span renders on one line: a wrapped token would
  // report two client rects.
  const codeLines = await cells.locator(".md-code").evaluateAll((els) =>
    els.map((el) => {
      const range = document.createRange();
      range.selectNodeContents(el);
      return { text: el.textContent, lines: range.getClientRects().length };
    }),
  );
  expect(codeLines.length).toBeGreaterThan(0);
  for (const code of codeLines) {
    expect(code.lines, `"${code.text}" must not break across lines`).toBe(1);
  }

  // The hash cell stays on one line as text too, and the commit column
  // is wide enough for the whole word. A range over the cell reports two
  // rects whatever happens (the text node plus the inline element), and
  // the cell's computed line-height is not its glyph height, so compare
  // the cell against a known single-line cell of the same table.
  const hash = cells.nth(0);
  await expect(hash).toHaveText("61b12f2");
  const box = await hash.evaluate((el) => {
    const code = el.querySelector(".md-code") as HTMLElement;
    const range = document.createRange();
    range.selectNodeContents(code);
    // A one-line reference: the version cell holds plain text on one line.
    const ref = document.querySelector<HTMLElement>("#messages .md-td:nth-of-type(2)");
    const oneLine = Math.min(
      el.getBoundingClientRect().height,
      ref?.getBoundingClientRect().height ?? el.getBoundingClientRect().height,
    );
    return {
      codeRects: range.getClientRects().length,
      cellHeight: el.getBoundingClientRect().height,
      oneLine,
      cellWidth: el.getBoundingClientRect().width,
    };
  });
  expect(box.codeRects).toBe(1);
  // A wrapped cell is taller than the one-line reference cell.
  expect(box.cellHeight).toBeLessThanOrEqual(box.oneLine + 1);
  expect(box.cellWidth).toBeGreaterThan(40);
});
