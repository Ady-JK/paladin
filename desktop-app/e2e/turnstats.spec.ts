// turnstats.spec.ts covers the one-line summary under a finished turn:
// "10m 9s · 103.9 tok/s", live and from a reopened transcript.
import { expect, test } from "@playwright/test";
import { showSidebar } from "./drawer";

test("a finished turn shows its duration and token rate", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eRunTurn = async () => ({
      reply: "Done.",
      stats: { durationMs: 609_000, tokens: 63_275 },
    });
  });
  await page.goto("/");
  await page.fill("#promptInput", "run the suite");
  await page.click("#sendBtn");

  const stats = page.locator("#messages .turn-stats");
  await expect(stats).toHaveCount(1);
  await expect(stats).toHaveText("10m 9s · 103.9 tok/s");
  await expect(stats).toHaveAttribute("title", "63,275 generated tokens");
});

test("a reopened session shows each turn's summary from the transcript", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    const history = [
      { role: "user", content: "first", createdAt: 1, index: 0, stats: { durationMs: 65_000, tokens: 1_300 } },
      { role: "assistant", content: "one", createdAt: 2 },
      { role: "user", content: "second", createdAt: 3, index: 2 },
      { role: "assistant", content: "two", createdAt: 4 },
    ];
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "sessionHistory") return history;
      if (cmd === "sessionChanges") return { available: false, turns: [], files: [] };
      return base(cmd, args);
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-notify-1"]');

  const stats = page.locator("#messages .turn-stats");
  await expect(stats).toHaveCount(1);
  await expect(stats).toHaveText("1m 5s · 20.0 tok/s");
});
