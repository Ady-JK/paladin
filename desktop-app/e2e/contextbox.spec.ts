// contextbox.spec.ts covers the collapsible Context strip above the
// composer: what paladin loads into the system prompt by itself, and the
// injected text behind each row.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("the context box collapses to a summary of what loads automatically", async ({ page }) => {
  const box = page.locator("#contextBox");
  await expect(box).toBeVisible();
  // Collapsed by default: one line, no rows.
  await expect(page.locator("#contextSummary")).toContainText("2 instruction files");
  await expect(page.locator("#contextSummary")).toContainText("55 chars");
  await expect(page.locator("#contextList")).toBeHidden();

  await page.click("#contextToggle");
  await expect(box).toHaveAttribute("data-open", "1");
  const rows = page.locator("#contextList .context-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("AGENTS.md");
  await expect(rows.first()).toContainText("Instructions");
  await expect(rows.nth(1)).toContainText("/home/tester/project/AGENTS.md");
});

test("a row shows the text the prompt actually carries", async ({ page }) => {
  await page.click("#contextToggle");
  const project = page.locator('#contextList .context-row[data-scope="project"]');
  await expect(project.locator(".context-text")).toHaveCount(0);

  await project.locator(".context-entry-head").click();
  await expect(project.locator(".context-text")).toContainText("Run make test before every commit.");
  await expect(project.locator(".context-path")).toHaveText("/home/tester/project/AGENTS.md");

  // The open/closed choice survives a reload.
  await page.reload();
  await expect(page.locator("#contextBox")).toHaveAttribute("data-open", "1");
});

test("a folder with nothing loaded says so instead of hiding", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eContext = {
      cwd: "/home/tester/empty",
      sections: [],
      bytes: 0,
      files: 0,
      skills: 0,
      mcp: 0,
    };
    localStorage.setItem("paladin.contextBox.open", "1");
  });
  await page.reload();
  await expect(page.locator("#contextSummary")).toContainText("No context loaded");
  await expect(page.locator("#contextList .context-empty")).toContainText("AGENTS.md");
});
