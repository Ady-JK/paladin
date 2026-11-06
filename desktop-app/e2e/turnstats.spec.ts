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
    // The core anchors a turn's summary on its last row.
    const history = [
      { role: "user", content: "first", createdAt: 1, index: 0 },
      { role: "assistant", content: "one", createdAt: 2, stats: { durationMs: 65_000, tokens: 1_300 } },
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
  // The summary closes its own turn: it sits after "one" and before the
  // next user row, not up next to the user message.
  const order = await page.evaluate(() =>
    Array.from(document.querySelectorAll("#messages .turn-row, #messages .turn-stats")).map((el) => {
      if (el.classList.contains("turn-stats")) return "stats";
      const bubble = el.querySelector(".bubble-user");
      return bubble ? "user:" + (bubble.textContent || "").trim() : "agent";
    }),
  );
  expect(order).toEqual(["user:first", "agent", "stats", "user:second", "agent"]);
});

test("the summary is docked right, in line with the turn's own rows", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eRunTurn = async () => ({
      reply: "Done.",
      stats: { durationMs: 60_000, tokens: 6_000 },
    });
  });
  await page.goto("/");
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");
  const stats = page.locator("#messages .turn-stats");
  await expect(stats).toHaveCount(1);

  // Right-aligned against the conversation's right edge, not centered:
  // the text ends at (nearly) the same x as the user bubble above it.
  const box = await stats.boundingBox();
  const bubble = await page.locator("#messages .bubble-user").first().boundingBox();
  const pane = await page.locator("#chatView").boundingBox();
  expect(box && bubble && pane).toBeTruthy();
  const statsRight = box!.x + box!.width;
  const bubbleRight = bubble!.x + bubble!.width;
  const paneRight = pane!.x + pane!.width - 24; // #chatView padding
  expect(Math.abs(statsRight - bubbleRight)).toBeLessThan(12);
  expect(Math.abs(statsRight - paneRight)).toBeLessThan(40);
});
