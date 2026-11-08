import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
});

test("a session opens a local browser beside its conversation", async ({ page }) => {
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: (window as any).__e2eSubmits[0].sessionId,
      url: "http://localhost:8080",
    }),
  );
  const pane = page.locator("#sidePane");
  await expect(pane).toBeVisible();
  await expect(pane.locator(".pane-browser")).toBeVisible();
  await expect(page.locator("#paneTabs .pane-tab[data-browser]")).toContainText("Agent browser");
  await expect(pane.locator(".browser-status")).toContainText("separate from your personal browser");
  await pane.locator(".browser-url").fill("http://127.0.0.1:8080");
  await pane.locator(".browser-open").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserOpens?.length ?? 0)).toBe(1);
  expect(await page.evaluate(() => (window as any).__e2eBrowserOpens[0].url)).toBe("http://127.0.0.1:8080");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserVisible)).toBeTruthy();
});

test("agent browser event opens the pane and keeps the URL", async ({ page }) => {
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: (window as any).__e2eSubmits[0].sessionId,
      url: "http://localhost:8080",
    }),
  );
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#sidePane .browser-url")).toHaveValue("http://localhost:8080");
});

test("a failed picker reset reports the error and leaves the button usable", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    w.paladin.browser.cancelPick = () => Promise.reject(new Error("picker unavailable"));
    w.__e2eHub.emit({
      event: "browser/opened",
      sessionId: w.__e2eSubmits[0].sessionId,
      url: "http://localhost:8080",
    });
  });
  await page.getByRole("button", { name: "Comment on elements" }).click();
  await expect(page.locator(".browser-status")).toContainText("picker unavailable");
  await expect(page.getByRole("button", { name: "Comment on elements" })).toHaveAttribute("aria-pressed", "false");
});

test("page annotations hand off saved comments without a separate comment panel", async ({ page }) => {
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: (window as any).__e2eSubmits[0].sessionId,
      url: "http://localhost:8080",
    }),
  );
  await page.locator(".browser-url").fill("http://127.0.0.1:8080");
  await page.locator(".browser-open").click();
  await page.locator("#promptInput").fill("Please polish this screen.");

  await page.getByRole("button", { name: "Comment on elements" }).click();
  await expect(page.locator(".browser-review")).toHaveCount(0);
  await page.evaluate(() => {
    const sessionId = (window as any).__e2eSubmits[0].sessionId;
    (window as any).__e2eBrowserNotes = [
      {
        id: "first",
        url: "http://127.0.0.1:8080",
        selector: "main > button:nth-of-type(2)",
        tag: "button",
        label: "Save",
        text: "Save",
        comment: "Make this action more prominent",
        color: 0,
      },
    ];
    (window as any).__e2eHub.emit({ event: "browser/annotations", sessionId, count: 1 });
  });
  await page.locator(".browser-url").fill("http://127.0.0.1:8080/settings");
  await page.locator(".browser-open").click();
  await page.evaluate(() => {
    const sessionId = (window as any).__e2eSubmits[0].sessionId;
    (window as any).__e2eBrowserNotes.push({
      id: "second",
      url: "http://127.0.0.1:8080/settings",
      selector: "#theme-toggle",
      tag: "input",
      label: "Dark mode",
      text: "",
      comment: "Move this switch into the header",
      color: 1,
    });
    (window as any).__e2eHub.emit({ event: "browser/annotations", sessionId, count: 2 });
  });
  await page.locator("#paneLayoutBtn").click();
  await expect(page.getByRole("button", { name: "Pass to agent" })).toBeEnabled();
  await page.getByRole("button", { name: "Pass to agent" }).click();

  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator("#promptInput")).toHaveValue(
    /Please polish this screen\.[\s\S]*main > button:nth-of-type\(2\)[\s\S]*Make this action more prominent[\s\S]*127\.0\.0\.1:8080\/settings[\s\S]*#theme-toggle[\s\S]*Move this switch into the header/,
  );
  expect(await page.evaluate(() => (window as any).__e2eSubmits.length)).toBe(1);
});

test("agent browser activity reopens its tab when the pane is closed", async ({ page }) => {
  const sessionId = await page.evaluate(() => (window as any).__e2eSubmits[0].sessionId);
  await page.evaluate(
    (id) => (window as any).__e2eHub.emit({ event: "browser/opened", sessionId: id, url: "http://localhost:8080" }),
    sessionId,
  );
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await page.locator("#paneCloseBtn").click();
  await expect(page.locator("#sidePane")).toBeHidden();
  await page.evaluate(
    (id) => (window as any).__e2eHub.emit({ event: "browser/activity", sessionId: id, url: "http://localhost:8080" }),
    sessionId,
  );
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#paneTabs [data-browser]")).toBeVisible();
});

test("background agent browsing is indicated in its chat without switching chats", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "background-chat",
        cwd: "/home/tester/project",
        title: "Background chat",
        createdAt: 1,
        messageCount: 1,
      },
    ];
  });
  await page.reload();
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: "background-chat",
      url: "http://localhost:8080",
    }),
  );
  await page.locator("#agentTab").click();
  const row = page.locator(".session-row").filter({ has: page.locator('[data-session="background-chat"]') });
  await expect(row.locator('[aria-label="Agent browser open"]')).toBeVisible();
  await expect(page.locator("#sidePane .pane-browser")).toHaveCount(0);
  await row.locator('[data-session="background-chat"]').click();
  await expect(page.locator('#paneTabs [data-browser="background-chat"]')).toBeVisible();
});
