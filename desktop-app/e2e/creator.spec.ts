import { expect, test } from "@playwright/test";
import { showSidebar } from "./drawer";

test("Creator mode opens its workspace and restores the previous one", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  const button = page.locator("#creatorModeBtn");
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await button.click();
  await expect(button).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#workspaceLabel")).toHaveText("creator");
  await expect(page.locator("#ideModeBtn")).toHaveAttribute("aria-pressed", "false");
  expect(await page.evaluate(() => localStorage.getItem("e2e-workspace"))).toBe("/home/creator");
  await showSidebar(page);
  await page.locator('button.session-pick[data-session="sess-notify-1"]').click();
  await expect(page.getByText("Exit Creator mode to switch workspaces")).toBeVisible();
  await expect(page.locator("#workspaceLabel")).toHaveText("creator");
  expect(await page.evaluate(() => localStorage.getItem("e2e-workspace"))).toBe("/home/creator");
  await button.click();
  await expect(button).toHaveAttribute("aria-pressed", "false");
  expect(await page.evaluate(() => localStorage.getItem("e2e-workspace"))).toBe("/home/tester/project");
});

test("the skill picker follows Creator mode and workspace changes", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const original = (window as any).paladin.invoke;
    (window as any).paladin.invoke = (method: string, args: { cwd?: string }) => {
      if (method === "listSkills") {
        const skills = [{ name: "debugging", description: "Debug", source: "builtin", path: "builtin:debugging" }];
        if ((window as any).__e2eCreatorMode && args.cwd === "/home/creator") {
          skills.push({
            name: "writing-an-extension",
            description: "Create tools",
            source: "builtin",
            path: "builtin:writing-an-extension",
          });
        }
        return Promise.resolve(skills);
      }
      return original(method, args);
    };
  });
  await page.goto("/");
  const input = page.locator("#promptInput");
  await input.fill("/skill ");
  await expect(page.locator("#skillPopup")).toContainText("debugging");
  await expect(page.locator("#skillPopup")).not.toContainText("writing-an-extension");
  await input.fill("");
  await page.locator("#creatorModeBtn").click();
  await expect(page.locator("#creatorModeBtn")).toHaveAttribute("aria-pressed", "true");
  await page.waitForTimeout(150); // The picker's blur handler closes after 120 ms.
  await input.fill("/skill ");
  await expect(page.locator("#skillPopup")).toContainText("writing-an-extension");
  await input.fill("");
  await page.locator("#creatorModeBtn").click();
  await expect(page.locator("#creatorModeBtn")).toHaveAttribute("aria-pressed", "false");
  await page.waitForTimeout(150);
  await input.fill("/skill ");
  await expect(page.locator("#skillPopup")).toContainText("debugging");
  await expect(page.locator("#skillPopup")).not.toContainText("writing-an-extension");
});

test("a custom chat view can send a message without losing the existing draft", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.locator("#promptInput").fill("keep this draft");
  await page.evaluate(() =>
    (window as any).__e2ePluginPrompt({
      text: "Message from custom UI",
      sessionId: "",
      workspace: "/home/tester/project",
    }),
  );
  await expect(page.locator("#messages")).toContainText("Message from custom UI");
  await expect(page.locator("#promptInput")).toHaveValue("keep this draft");
});

test("a failed plugin message preserves the existing draft", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const original = (window as any).paladin.invoke;
    (window as any).paladin.invoke = (method: string, args: unknown) =>
      method === "submitChat" ? Promise.reject(new Error("test failure")) : original(method, args);
  });
  await page.goto("/");
  await page.locator("#promptInput").fill("keep this draft");
  await page.evaluate(() =>
    (window as any).__e2ePluginPrompt({
      text: "Message from custom UI",
      sessionId: "",
      workspace: "/home/tester/project",
    }),
  );
  await expect(page.locator("#messages")).toContainText("test failure");
  await expect(page.locator("#promptInput")).toHaveValue("keep this draft");
});
