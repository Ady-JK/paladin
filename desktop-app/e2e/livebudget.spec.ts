// livebudget.spec.ts covers the composer's spend chip keeping up with a
// running turn: it reads the ledger on every chat/usage signal, the same
// moment the header pill does, instead of waiting for turn/done.
import { test, expect } from "@playwright/test";

test("the spend chip moves during a turn, not only when it ends", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eBudget = { defaultUsd: 0, sessions: {}, spentUsd: 0, hasCost: true, calls: 0 };
    // The turn bills twice and then parks until the spec releases it, so
    // each mid-turn assertion is made against a turn that is certainly
    // still running rather than racing its end on a loaded machine.
    (window as any).__e2eRunTurn = async (_params: unknown, h: any) => {
      const w = window as any;
      const b = w.__e2eBudget;
      b.spentUsd = 0.42;
      b.calls = 1;
      h?.onUsage?.();
      await new Promise((resolve) => setTimeout(resolve, 400));
      b.spentUsd = 1.37;
      b.calls = 2;
      h?.onUsage?.();
      // Hold the turn open until the spec says the assertions are done.
      w.__e2eReleased = new Promise((resolve) => {
        w.__e2eRelease = resolve;
      });
      await w.__e2eReleased;
      return { reply: "Done.", stats: { durationMs: 1000, tokens: 100 } };
    };
  });
  await page.goto("/");
  await page.fill("#promptInput", "spend some money");
  await page.click("#sendBtn");

  const chip = page.locator("#budgetChip");
  const bar = page.locator("#workingBar");

  // Mid-turn: the first call's spend reaches the chip while the turn is
  // still running, exactly like the header pill.
  await expect(chip).toContainText("$0.42", { timeout: 8000 });
  await expect(bar).toBeVisible();

  // The second call moves it again, still inside the same turn.
  await expect(chip).toContainText("$1.37", { timeout: 8000 });
  await expect(bar).toBeVisible();

  // The turn kept running the whole time; only now is it let go.
  await page.evaluate(() => (window as any).__e2eRelease());
  await expect(bar).toBeHidden({ timeout: 8000 });
  await expect(chip).toContainText("$1.37");
});
