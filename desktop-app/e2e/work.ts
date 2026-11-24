// work.ts: a finished turn folds its working into "Worked for …". Specs that
// look inside the working (tool rows, thinking blocks) open the fold first.
import { expect, type Page } from "@playwright/test";

// openWork waits for the turn's fold and opens every one on the page.
export async function openWork(page: Page): Promise<void> {
  await expect(page.locator("details.worked-fold").first()).toBeAttached();
  await page.evaluate(() => {
    for (const fold of Array.from(document.querySelectorAll<HTMLDetailsElement>("details.worked-fold"))) {
      fold.open = true;
    }
  });
}
