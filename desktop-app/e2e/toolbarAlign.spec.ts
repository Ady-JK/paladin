// toolbarAlign.spec.ts: every control in the composer toolbar shares one
// centre line, and controls of the same kind share one size.
import { test, expect } from "@playwright/test";

test("composer toolbar controls share a centre line and a size scale", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page.locator("#sendBtn")).toBeVisible();

  const m = await page.evaluate(() => {
    const read = (el: Element) => {
      const r = el.getBoundingClientRect();
      return {
        id: el.id || el.className.toString(),
        cy: r.top + r.height / 2,
        h: r.height,
        fs: getComputedStyle(el).fontSize,
      };
    };
    const vis = (el: Element) => el.getBoundingClientRect().width > 0;
    const pick = (sel: string) => Array.from(document.querySelectorAll(sel)).filter(vis).map(read);
    return {
      round: pick("#attachBtn, #voiceBtn, #sendBtn"),
      chips: pick(".composer-toolbar :is(#modelSelectBtn, #thinkingMore, #contextButton, .perm-chip)"),
      all: pick(
        ".composer-toolbar :is(#attachBtn, #modelSelectBtn, #thinkingMore, #contextButton, .perm-chip, #voiceBtn, #voiceMenuBtn, #sendBtn)",
      ),
    };
  });

  expect(m.chips.length).toBeGreaterThan(2);
  for (const c of m.all) expect(Math.abs(c.cy - m.all[0].cy), c.id).toBeLessThan(0.51);
  for (const c of m.round) expect(c.h, c.id).toBe(32);
  for (const c of m.chips) {
    expect(c.h, c.id).toBe(30);
    expect(c.fs, c.id).toBe("13px");
  }
});
