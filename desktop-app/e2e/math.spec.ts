// LaTeX math in agent Markdown: $...$ and $$...$$ render through KaTeX,
// while prices, code and escaped dollar signs stay literal, and TeX source
// never becomes markup.
import { test, expect } from "@playwright/test";
import { renderMarkdown } from "../src/core/format";

test("an agent reply shows math as KaTeX in the transcript", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      { event: "chat/delta", delta: "Ratio $\\leftrightarrow$ here.\n\n$$E = mc^2$$" },
    ];
  });
  await page.fill("#promptInput", "explain");
  await page.click("#sendBtn");
  const messages = page.locator("#messages");
  await expect(messages.locator(".katex")).toHaveCount(2);
  await expect(messages.locator(".katex-display")).toHaveCount(1);
  await expect(messages).not.toContainText("$");
  await expect
    .poll(() =>
      page.evaluate(() => {
        let loaded = false;
        document.fonts.forEach((f) => {
          if (f.family.includes("KaTeX") && f.status === "loaded") loaded = true;
        });
        return loaded;
      }),
    )
    .toBe(true);
});

test("inline and display math render through KaTeX", () => {
  const inline = renderMarkdown("Ratio is $\\leftrightarrow$ here.");
  expect(inline).toContain('<span class="katex">');
  expect(inline).not.toContain("$\\leftrightarrow$");
  expect(inline).toContain("Ratio is ");

  const display = renderMarkdown("Before\n\n$$\nE = mc^2\n$$\n\nAfter");
  expect(display).toContain('<span class="katex-display">');
  expect(display).toContain("After");
});

test("math works alongside Markdown emphasis and links", () => {
  const html = renderMarkdown("**Speed** $<1\\text{ms}$ and [docs](https://example.com/a_b_c) with *x*");
  expect(html).toContain("<strong>Speed</strong>");
  expect(html).toContain('<span class="katex">');
  expect(html).toContain('data-ext="https://example.com/a_b_c"');
  expect(html).toContain("<em>x</em>");
});

test("dollar amounts and escaped dollars stay plain text", () => {
  const prices = renderMarkdown("It costs $5 and $10 today.");
  expect(prices).not.toContain("katex");
  expect(prices).toContain("$5 and $10");

  const escaped = renderMarkdown("Price \\$5 and \\$6 $x$");
  expect(escaped).toContain("Price $5 and $6");
  expect(escaped).toContain('<span class="katex">');
});

test("dollar signs inside code are never math", () => {
  const span = renderMarkdown("Use `$x$` literally.");
  expect(span).toContain('<code class="md-code">$x$</code>');
  expect(span).not.toContain("katex");

  const fence = renderMarkdown("```\n$$y$$\n```\n\nafter $z$");
  expect(fence).toContain("<code>$$y$$</code>");
  expect(fence.match(/class="katex"/g)).toHaveLength(1);
});

test("TeX source is never markup and bad TeX does not throw", () => {
  const hostile = renderMarkdown('$\\text{<img src=x onerror="window.__pwned=1">}$');
  expect(hostile).not.toContain("<img");
  expect(hostile).not.toMatch(/"\s*onerror=/);

  const broken = renderMarkdown("$\\notARealCommand{x}$");
  expect(broken).toContain('<span class="katex">');
  expect(broken).toContain("#cc0000");
});

test("math inside a table cell keeps its pipes and the table structure", () => {
  const html = renderMarkdown("| Op | Cost |\n| --- | --- |\n| abs | $|x|$ |");
  expect(html).toContain('<table class="md-table">');
  expect(html.match(/<td /g)).toHaveLength(2);
  expect(html).toContain('<span class="katex">');
});

test("placeholder markers typed by the agent are dropped", () => {
  const html = renderMarkdown("odd \uE000a\uE001 text");
  expect(html).toContain("odd a text");
  expect(html).not.toContain("\uE000");
});
