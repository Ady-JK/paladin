// livebudget.spec.ts covers the composer's spend chip keeping up with a
// running turn: it reads the ledger on every chat/usage signal, the same
// moment the header pill does, instead of waiting for turn/done.
import { test, expect, type Page } from "@playwright/test";

// budgetLedger stores the spend the stub reports, so a spec can move it
// while a turn is still running.
async function seedLedger(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as any).__e2eBudget = { defaultUsd: 0, sessions: {}, spentUsd: 0, hasCost: true, calls: 0 };
  });
}

test("the spend chip moves during a turn, not only when it ends", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await seedLedger(page);
  await page.addInitScript(() => {
    // The turn bills twice: the ledger grows, and each write emits the
    // chat/usage signal the sidecar sends after every LLM call.
    (window as any).__e2eRunTurn = async (_params: unknown, h: any) => {
      const b = (window as any).__e2eBudget;
      b.spentUsd = 0.42;
      b.calls = 1;
      h?.onUsage?.();
      await new Promise((resolve) => setTimeout(resolve, 400));
      b.spentUsd = 1.37;
      b.calls = 2;
      h?.onUsage?.();
      await new Promise((resolve) => setTimeout(resolve, 600));
      return { reply: "Done.", stats: { durationMs: 1000, tokens: 100 } };
    };
  });
  await page.goto("/");
  await page.fill("#promptInput", "spend some money");
  await page.click("#sendBtn");

  const chip = page.locator("#budgetChip");
  // Mid-turn: the first call's spend is already on the chip while the
  // turn is still running (the working bar is up for the whole turn).
  await expect(chip).toContainText("$0.42", { timeout: 4000 });
  await expect(page.locator("#workingBar")).toBeVisible();

  // The second call moves it again, still before the turn ends.
  await expect(chip).toContainText("$1.37", { timeout: 4000 });
  await expect(page.locator("#workingBar")).toBeVisible();

  // Only when the turn ends does the bar go away, with the final figure.
  await expect(page.locator("#workingBar")).toBeHidden({ timeout: 5000 });
  await expect(chip).toContainText("$1.37");
});
