import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
});

test("IDE terminals can be renamed, split and closed independently", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.locator("#ideTerminalToggle").click();
  const dock = page.locator("#ideTerminalDock");
  await expect(dock.locator(".xterm")).toBeVisible();
  await dock.getByRole("button", { name: "Rename terminal" }).click();
  await dock.getByRole("textbox", { name: "Terminal name" }).fill("Server");
  await dock.getByRole("textbox", { name: "Terminal name" }).press("Enter");
  await expect(dock.getByRole("tab", { name: "Server" })).toBeVisible();
  await dock.getByRole("button", { name: "Split terminal" }).click();
  await expect(dock.locator(".pane-terminal:visible")).toHaveCount(2);
  await expect(dock.locator(".xterm:visible")).toHaveCount(2);
  await dock.getByRole("button", { name: "Split terminal" }).click();
  await expect(dock.locator(".pane-terminal:visible")).toHaveCount(1);
  await dock.getByRole("button", { name: "Split terminal" }).click();
  await expect(dock.locator(".pane-terminal:visible")).toHaveCount(2);
  await expect(dock.getByRole("tab")).toHaveCount(2);
  await dock.getByRole("button", { name: "Close terminal" }).click();
  await expect(dock.locator(".pane-terminal:visible")).toHaveCount(1);
  await expect(dock.getByRole("tab", { name: "Server" })).toBeVisible();
});

test("selected IDE terminal output can be added to the agent draft", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  const row = page.locator("#ideTerminalDock .xterm-rows > div").filter({ hasText: "/home/tester/project" }).first();
  await expect(row).toBeVisible();
  // The Explorer overlay covers the left of the terminal; close it so the drag reaches the text.
  await page.locator("#ideExplorerToggle").click();
  await expect(page.locator(".ide-explorer")).toHaveClass(/is-closed/);
  const box = (await row.boundingBox())!;
  await page.mouse.move(box.x + 3, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + 170, box.y + box.height / 2, { steps: 8 });
  await page.mouse.up();
  await page.getByRole("button", { name: "Add selected output to agent" }).click();
  await expect(page.locator("#promptInput")).toHaveValue(/Terminal output:[\s\S]*\/home\/tester\/project/);
  expect(await page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(0);
});

test("bang runs a workspace command then starts a turn with its result", async ({ page }) => {
  await page.locator("#promptInput").fill("! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalRuns?.length ?? 0)).toBe(1);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(1);
  const result = await page.evaluate(() => ({
    run: (window as any).__e2eTerminalRuns[0],
    submit: (window as any).__e2eSubmits[0],
  }));
  expect(result.run.command).toBe("pwd");
  expect(result.run.cwd).toBe("/home/tester/project");
  expect(result.submit.message).toContain("/home/tester/project");
  expect(result.submit.message).toContain("pwd");
});

test("double bang shows output without adding agent history", async ({ page }) => {
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalRuns?.length ?? 0)).toBe(1);
  await expect(page.locator(".pane-terminal .xterm-rows")).toContainText("/home/tester/project");
  expect(await page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(0);
  expect(await page.evaluate(() => (window as any).__e2eOpens?.length ?? 0)).toBe(0);
});

test("terminal button opens a full terminal tab beside chat", async ({ page }) => {
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect(page.locator(".pane-terminal")).toBeVisible();
  await expect(page.locator(".xterm")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("pwd");
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBeGreaterThan(0);
});

test("an interactive terminal works before the first chat message", async ({ page }) => {
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect(page.locator(".pane-terminal .xterm")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  const start = await page.evaluate(() => (window as any).__e2eTerminalStarts[0]);
  expect(start.cwd).toBe("/home/tester/project");
  expect(start.sessionId).toBeTruthy();
  expect(await page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(0);
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("pwd");
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBeGreaterThan(0);
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await expect(page.locator(".side-chat")).toHaveCount(0);
  await expect(page.locator(".pane-terminal")).toBeVisible();
  await page.locator("#paneTabs .pane-tab-close").click();
  await expect(page.locator(".pane-terminal")).toHaveCount(0);
});

test("typing while the terminal is starting is delivered once it is ready", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = (method: string, args: unknown) => {
      if (method === "terminalStart")
        return new Promise((resolve) => {
          w.__finishTerminalStart = () => resolve("terminal-delayed");
        });
      return base(method, args);
    };
  });
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect(page.locator(".xterm")).toBeVisible();
  await expect.poll(() => page.evaluate(() => typeof (window as any).__finishTerminalStart)).toBe("function");
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("first command");
  await page.keyboard.press("Enter");
  expect(await page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBe(0);
  await page.evaluate(() => (window as any).__finishTerminalStart());
  await expect
    .poll(() =>
      page.evaluate(() => ((window as any).__e2eTerminalWrites || []).map((write: any) => write.data).join("")),
    )
    .toBe("first command\r");
});

test("a terminal exiting before its start reply shows its output and exit", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = (method: string, args: any) => {
      if (method === "terminalStart") {
        w.__e2eHub.emit({
          event: "terminal/data",
          sessionId: args.sessionId,
          terminalId: "early-exit",
          data: btoa("goodbye"),
        });
        w.__e2eHub.emit({ event: "terminal/exit", sessionId: args.sessionId, terminalId: "early-exit", exitCode: 1 });
        return Promise.resolve("early-exit");
      }
      return base(method, args);
    };
  });
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect(page.locator(".xterm-rows")).toContainText("goodbye");
  await expect(page.locator(".xterm-rows")).toContainText("terminal exited: 1");
});

test("a terminal opened before the first message remains usable after starting a chat", async ({ page }) => {
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator("#promptInput").fill("hello");
  await page.locator("#promptInput").press("Enter");
  await expect(page.locator("#messages")).toContainText("hello");
  await expect(page.locator(".pane-terminal .xterm")).toBeVisible();
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await page.locator("#paneTabs .pane-tab").filter({ hasText: "Terminal" }).locator(".pane-tab-pick").click();
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("pwd");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBeGreaterThan(0);
  await page.evaluate(() => {
    const owner = (window as any).__e2eTerminalStarts[0].sessionId;
    (window as any).__e2eHub.emit({
      event: "terminal/data",
      sessionId: owner,
      terminalId: "terminal-1",
      data: btoa("standalone output"),
    });
  });
  await expect(page.locator(".pane-terminal .xterm-rows")).toContainText("standalone output");
  expect(await page.evaluate(() => (window as any).__e2eTerminalStarts.length)).toBe(1);
});

test("a side chat requires a main message even after a local command", async ({ page }) => {
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalRuns?.length ?? 0)).toBe(1);
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await expect(page.locator("#toastText")).toContainText("Send a message in the main chat");
  await expect(page.locator(".side-chat")).toHaveCount(0);
});

test("a standalone terminal follows the user when opening an existing chat", async ({ page }) => {
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator("#agentTab").click();
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator(".pane-terminal .xterm")).toBeVisible();
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("pwd");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBeGreaterThan(0);
  expect(await page.evaluate(() => (window as any).__e2eTerminalStarts.length)).toBe(1);
});

test("the shared terminal button reopens the existing terminal in Agent mode", async ({ page }) => {
  const toggle = page.locator("#rail #ideTerminalToggle");
  await toggle.click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await toggle.click();
  await expect(page.locator("#sidePane")).toBeHidden();
  await toggle.click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await expect(page.locator("#sidePane .pane-terminal")).toHaveCount(1);
});
