// escaping.spec.ts: text Paladin did not write (model output, tool
// arguments, URLs, file and server names) must never become markup. A
// quote that closes an attribute could add an event handler, and code in
// the main window can reach the bridge.
import { test, expect } from "@playwright/test";
import { escapeAttr, escapeHtml, renderMarkdown, revealHidden } from "../src/core/format";

const BREAKOUT = `"style="animation-name:shimmer;animation-duration:1s"onanimationstart="window.__pwned=1`;

test("escapeHtml is safe inside quoted attributes", () => {
  expect(escapeHtml(`a<b>&"c'`)).toBe("a&lt;b&gt;&amp;&quot;c&#39;");
  expect(escapeAttr(`"x'`)).toBe("&quot;x&#39;");
  const holder = `<span title="${escapeHtml(BREAKOUT)}">x</span>`;
  expect(holder).not.toContain(`"onanimationstart`);
});

test("markdown links keep their URL, titles still work, and quotes cannot break out", () => {
  const titled = renderMarkdown(`[docs](https://example.com/a?q=1&r=2 "The docs")`);
  expect(titled).toContain(`data-ext="https://example.com/a?q=1&amp;r=2"`);
  expect(titled).toContain(">docs</a>");
  const quoted = renderMarkdown(`see [it](https://example.com/?x=${BREAKOUT})`);
  expect(quoted).not.toMatch(/"\s*onanimationstart=/);
  const bare = renderMarkdown(`see https://example.com/it's. and https://example.com/${BREAKOUT}`);
  expect(bare).toContain(`data-ext="https://example.com/it&#39;s"`);
  expect(bare).not.toMatch(/"\s*onanimationstart=/);
});

test("a malicious URL on a web approval card and a todo status stay text", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  const url = `https://evil.example/?q=${BREAKOUT}`;
  await page.evaluate(
    ({ url, breakout }) => {
      (window as any).__e2eChatEvents = [
        {
          event: "chat/tool",
          tool: {
            id: "t1",
            name: "todo_write",
            arguments: JSON.stringify({ todos: [{ content: "x", status: breakout }] }),
            result: "",
            status: "running",
          },
        },
        {
          event: "chat/tool",
          tool: { id: "w1", name: "web_fetch", arguments: JSON.stringify({ url }), result: "", status: "running" },
        },
        {
          event: "chat/approval",
          approval: {
            id: "w1",
            tool: "web_fetch",
            kind: "web",
            host: "evil.example",
            url,
            reasons: ["a new site"],
            choices: ["once", "session", "deny"],
            state: "pending",
          },
        },
      ];
    },
    { url, breakout: BREAKOUT },
  );
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");
  const card = page.locator('.approval-card[data-approval="w1"]');
  await expect(card).toBeVisible();
  await expect(card.locator(".approval-web-url")).toHaveAttribute("title", url);
  await page.waitForTimeout(1500);
  expect(await page.evaluate(() => (window as any).__pwned)).toBeUndefined();
  expect(await page.locator("[onanimationstart]").count()).toBe(0);
});

test("invisible and direction-changing characters are shown on approval cards", () => {
  const cmd = "ls \u202e;hs.lave tac";
  const html = revealHidden(escapeHtml(cmd));
  expect(html).toContain("⟨U+202E⟩");
  expect(html).not.toContain("\u202e");
  expect(revealHidden("tab\there\nnewline")).toBe("tab\there\nnewline");
  expect(revealHidden(escapeHtml("zero\u200bwidth"))).toContain("⟨U+200B⟩");
});
