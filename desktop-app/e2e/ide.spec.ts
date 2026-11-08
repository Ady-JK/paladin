import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("IDE opens a workspace file, edits and saves while chat stays usable", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("Hello from file");
  await page.locator("#ideEditor .cm-content").fill("Edited\n");
  await expect(page.locator("#ideTabs")).toContainText("●");
  await page.locator("#ideSaveBtn").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eIDESaves?.length ?? 0)).toBe(1);
  expect(await page.evaluate(() => (window as any).__e2eIDESaves[0].content)).toBe("Edited\n");
  await expect(page.locator("#ideStatus")).toContainText("Saved");
  await page.locator("#promptInput").fill("explain this file");
  await page.locator("#promptInput").press("Enter");
  await expect(page.locator("#messages")).toContainText("explain this file");
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("Edited");
});

test("IDE changes panel shows the current session diff next to the editor", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChanges = { available: true, turns: [], files: [{ path: "README.md", status: "modified" }] };
    (window as any).__e2ePatch = "@@ -1 +1 @@\n-before\n+after";
  });
  await page.locator("#ideModeBtn").click();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.getByRole("button", { name: "Show changes" }).click();
  const panel = page.locator("#ideChangesPanel");
  await expect(panel).toBeVisible();
  await panel.getByRole("button", { name: /README.md/ }).click();
  await expect(panel.locator(".change-diff")).toContainText("+after");
  await expect(page.locator("#ideEditor .cm-content")).toBeVisible();
  expect((await panel.boundingBox())!.x).toBeGreaterThan((await page.locator("#ideEditor").boundingBox())!.x);
  await page.getByRole("button", { name: "Hide changes" }).click();
  await expect(panel).toBeHidden();
});

test("IDE Problems shows diagnostics for unsaved text and jumps to the error", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eIDEContents = { "/home/tester/project/README.md": "first\nerror here\nthird\n" };
    (window as any).__e2eIDEDiagnostics = {
      state: "ready",
      server: "fake",
      errors: 1,
      diagnostics: [{ line: 2, column: 1, message: "Example error" }],
    };
  });
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.getByRole("button", { name: "Show problems" }).click();
  const problems = page.locator("#ideProblemsPanel");
  await expect(problems).toContainText("Example error");
  await problems.getByRole("button", { name: /Example error/ }).click();
  await expect
    .poll(() => page.evaluate(() => window.getSelection()?.anchorNode?.parentElement?.textContent))
    .toContain("error here");
  await page.getByRole("button", { name: "Hide problems" }).click();
  await expect(problems).toBeHidden();
});

test("IDE palette opens workspace files and runs commands from the keyboard", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.keyboard.press(process.platform === "darwin" ? "Meta+p" : "Control+p");
  const palette = page.getByRole("dialog", { name: "Quick open" });
  await expect(palette).toBeVisible();
  await palette.getByRole("textbox").fill("main.go");
  await expect(palette.getByRole("button", { name: /src\/main.go/ })).toBeVisible();
  await palette.getByRole("textbox").press("Enter");
  await expect(palette).toBeHidden();
  await expect(page.locator("#idePath")).toContainText("src/main.go");
  await page.keyboard.press(process.platform === "darwin" ? "Meta+Shift+p" : "Control+Shift+p");
  await palette.getByRole("textbox").fill(">terminal");
  await palette.getByRole("textbox").press("Enter");
  await expect(page.locator("#ideTerminalDock")).toBeVisible();
});

test("IDE go to line opens from the keyboard and moves the caret", async ({ page }) => {
  await page.evaluate(
    () => ((window as any).__e2eIDEContents = { "/home/tester/project/README.md": "first\nsecond\nthird" }),
  );
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.locator("#ideEditor .cm-content").focus();
  await page.keyboard.press("Control+g");
  const palette = page.getByRole("dialog", { name: "Quick open" });
  await expect(palette).toBeVisible();
  await palette.getByRole("textbox").fill(":2");
  await palette.getByRole("textbox").press("Enter");
  await expect(palette).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => window.getSelection()?.anchorNode?.parentElement?.textContent))
    .toContain("second");
});

test("workspace search opens a matching file at its line", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eIDEContents = { "/home/tester/project/README.md": "first\nneedle\nthird\n" };
    (window as any).__e2eIDESearch = {
      hits: [{ path: "/home/tester/project/README.md", line: 2, text: "needle" }],
      truncated: false,
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.keyboard.press(process.platform === "darwin" ? "Meta+Shift+f" : "Control+Shift+f");
  const search = page.getByRole("dialog", { name: "Search workspace" });
  await search.getByRole("textbox").fill("needle");
  await search.getByRole("button", { name: "Search", exact: true }).click();
  await search.getByRole("button", { name: /README.md:2/ }).click();
  await expect(search).toBeHidden();
  await expect(page.locator("#ideEditor .cm-content")).toContainText("needle");
  await expect
    .poll(() => page.evaluate(() => window.getSelection()?.anchorNode?.parentElement?.textContent))
    .toContain("needle");
});

test("IDE activity opens a session waiting for approval", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.evaluate(() => {
    const hub = (window as any).__e2eHub;
    hub.emit({
      event: "turn/start",
      sessionId: "sess-notify-1",
      turnId: "activity-turn",
      turn: { turnId: "activity-turn", sessionId: "sess-notify-1", state: "running" },
    });
    hub.emit({
      event: "chat/approval",
      sessionId: "sess-notify-1",
      turnId: "activity-turn",
      approval: {
        id: "activity-approval",
        tool: "shell",
        kind: "shell",
        command: "npm test",
        choices: ["once", "deny"],
        state: "pending",
      },
    });
  });
  await page.getByRole("button", { name: /Activity/ }).click();
  const activity = page.locator("#ideActivityList");
  await expect(activity).toContainText("Waiting for approval");
  await activity.getByRole("button", { name: /notify demo/ }).click();
  await expect(page.locator("#ideSessionList")).toBeHidden();
  await expect(page.locator("#messages")).toContainText("run the slow job");
});

test("IDE restores unsaved workspace drafts without overwriting disk", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eIDELayouts = {
      "/home/tester/project": {
        files: [
          {
            path: "/home/tester/project/README.md",
            draft: { content: "recovered draft", saved: "Hello from file\n", version: "v1", lineEnding: "\n" },
          },
        ],
        activePath: "/home/tester/project/README.md",
        expanded: [],
        explorerVisible: true,
        agentVisible: true,
        terminalHeight: 250,
        sessionId: "",
      },
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("recovered draft");
  await expect(page.locator("#ideTabs")).toContainText("●");
  expect(await page.evaluate(() => (window as any).__e2eIDESaves?.length ?? 0)).toBe(0);
});

test("a malformed saved draft is not silently overwritten", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eIDELayouts = {
      "/home/tester/project": { files: [{ path: "/home/tester/project/README.md", draft: { content: "keep me" } }] },
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideStatus")).toContainText("layout saving is paused");
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.locator("#ideEditor .cm-content").fill("another draft");
  await page.waitForTimeout(700);
  expect(
    await page.evaluate(() => (window as any).__e2eIDELayouts["/home/tester/project"].files[0].draft.content),
  ).toBe("keep me");
});

test("IDE restores a previously selected session for its workspace", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eIDELayouts = {
      "/home/tester/project": {
        files: [],
        activePath: "",
        expanded: [],
        explorerVisible: true,
        agentVisible: true,
        terminalHeight: 0,
        sessionId: "sess-notify-1",
      },
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
});

test("Explorer filters can be changed and are remembered per workspace", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.getByRole("button", { name: ".hidden-dir" })).toBeVisible();
  await page.getByLabel("Hide dot folders").check();
  await expect(page.getByRole("button", { name: ".hidden-dir" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: ".env", exact: true })).toBeVisible();
  await page.getByLabel("Hide Git-ignored items").check();
  await expect(page.getByRole("button", { name: "README.md", exact: true })).toHaveCount(0);
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionList button[data-fb-select$="/project"]').first().click();
  await page.locator("#sessionStartBtn").click();
  await expect(page.getByLabel("Hide dot folders")).not.toBeChecked();
  await expect(page.getByLabel("Hide Git-ignored items")).not.toBeChecked();
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionCrumbs button[data-fb-crumb="/home/tester/project"]').click();
  await expect(page.locator("#newSessionPath")).toHaveText("/home/tester/project");
  await page.locator("#sessionStartBtn").click();
  await expect(page.getByLabel("Hide dot folders")).toBeChecked();
  await expect(page.getByLabel("Hide Git-ignored items")).toBeChecked();
  await page.getByLabel("Hide Git-ignored items").uncheck();
  await expect(page.getByRole("button", { name: "README.md", exact: true })).toBeVisible();
});

test("IDE retains edits on save conflict and when switching views", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.locator("#ideEditor .cm-content").fill("unsaved");
  await page.evaluate(() => ((window as any).__e2eIDEConflict = true));
  await page.locator("#ideSaveBtn").click();
  await expect(page.locator("#ideStatus")).toContainText("changed on disk");
  await page.locator("#ideCloseBtn").click();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("unsaved");
});

test("an externally changed background file is marked and shows a notice when opened", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.getByRole("button", { name: ".env", exact: true }).click();
  await page.evaluate(() => {
    const path = "/home/tester/project/README.md";
    (window as any).__e2eIDEContents = { [path]: "changed outside" };
    (window as any).__e2eIDEVersions = { [path]: "v2" };
    (window as any).__e2eIDEFileStatuses = { [path]: "changed" };
    window.dispatchEvent(new Event("focus"));
  });
  const tab = page.locator("#ideTabs .ide-tab-select").filter({ hasText: "README.md" });
  await expect(tab.locator('[aria-label="Changed on disk"]')).toBeVisible();
  await tab.click();
  await expect(page.getByText("This file changed on disk.")).toBeVisible();
  await page.getByRole("button", { name: "Compare changes" }).click();
  await expect(page.locator("#ideDiskCompare")).toContainText("changed outside");
});

test("file tabs preserve separate unsaved edits and undo history", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  const editor = page.locator("#ideEditor .cm-content");
  await editor.fill("first draft");
  await page.getByRole("button", { name: ".env", exact: true }).click();
  await editor.fill("second draft");
  await page.locator("#ideTabs .ide-tab-select").filter({ hasText: "README.md" }).click();
  await expect(editor).toHaveText("first draft");
  await editor.press(process.platform === "darwin" ? "Meta+z" : "Control+z");
  await expect(editor).toHaveText("Hello from file");
  await page.locator("#ideTabs .ide-tab-select").filter({ hasText: ".env" }).click();
  await expect(editor).toHaveText("second draft");
});

test("editor preserves Windows line endings and asks before discarding edits", async ({ page }) => {
  await page.evaluate(
    () => ((window as any).__e2eIDEContents = { "/home/tester/project/README.md": "first\r\nsecond\r\n" }),
  );
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  const editor = page.locator("#ideEditor .cm-content");
  await editor.fill("changed\nnext");
  await page.locator("#ideSaveBtn").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eIDESaves?.length ?? 0)).toBe(1);
  expect(await page.evaluate(() => (window as any).__e2eIDESaves[0].content)).toBe("changed\r\nnext");
  await editor.fill("unsaved");
  await page.locator("#ideReloadBtn").click();
  await expect(editor).toHaveText("unsaved");
  await expect(page.locator("#ideReloadBtn")).toHaveText("Confirm discard");
  await page.locator("#ideReloadBtn").click();
  await expect(editor).toContainText("first");
});

test("the folder picker and session switcher stay in the IDE", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.locator("#ideChooseFolder").click();
  await expect(page.locator("#sessionModal")).toBeVisible();
  await expect(page.locator("#sessionDialogTitle")).toHaveText("Open folder");
  await page.locator("#sessionCancelBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await expect(page.locator("#ideWorkspace")).toBeVisible();
});

test("IDE keeps the editor and chat visible in tab layout when a terminal opens", async ({ page }) => {
  await page.locator("#paneLayoutBtn").click();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator(".pane-terminal")).toBeVisible();
});

test("choosing a folder updates the explorer and preserves the old draft", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.locator("#ideEditor .cm-content").fill("old workspace draft");
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionList button[data-fb-select$="/project"]').first().click();
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideFolderName")).toHaveAttribute("title", "/home/tester/project/project");
  await expect(page.locator("#ideEmpty")).toBeVisible();
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionCrumbs button[data-fb-crumb="/home/tester/project"]').click();
  await expect(page.locator("#newSessionPath")).toHaveText("/home/tester/project");
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("old workspace draft");
});

test("IDE panes can be resized with the keyboard", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  const explorer = page.locator(".ide-explorer");
  const before = (await explorer.boundingBox())!.width;
  await page.locator("#ideExplorerResize").focus();
  await page.keyboard.press("ArrowRight");
  await expect.poll(async () => (await explorer.boundingBox())!.width).toBeGreaterThan(before);
  const chat = page.locator("#agentMain");
  const chatBefore = (await chat.boundingBox())!.width;
  await page.locator("#ideChatResize").focus();
  await page.keyboard.press("ArrowLeft");
  await expect.poll(async () => (await chat.boundingBox())!.width).toBeGreaterThan(chatBefore);
});

test("IDE explorer and agent panes collapse independently and reopen", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "Hide explorer" }).click();
  await expect(page.locator(".ide-explorer")).toBeHidden();
  await expect(page.locator("#ideExplorerResize")).toBeHidden();
  await page.getByRole("button", { name: "Hide agent" }).click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#ideChatResize")).toBeHidden();
  await page.locator("#ideCloseBtn").click();
  await expect(page.locator("#agentMain")).toBeVisible();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await page.getByRole("button", { name: "Show explorer" }).click();
  await page.getByRole("button", { name: "Show agent" }).click();
  await expect(page.locator(".ide-explorer")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock")).toBeVisible();
  await page.getByRole("button", { name: "Hide terminal panel" }).click();
  await expect(page.locator("#ideTerminalDock")).toBeHidden();
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock")).toBeVisible();
});

test("IDE starts with workspace sessions and opens a selected chat", async ({ page }) => {
  await expect(page.locator("#titleBar #ideModeBtn")).toBeVisible();
  await expect(page.locator("#sidebar #ideModeBtn")).toHaveCount(0);
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList")).toBeVisible();
  await expect(page.locator("#ideSessionList")).toContainText("notify demo");
  await expect(page.locator("#ideSessionList")).not.toContainText("different workspace");
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await expect(page.locator("#ideSessionList")).toBeHidden();
});

test("terminal opens below editor in IDE and survives hide and mode changes", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-terminal]").click();
  await expect(page.locator("#ideTerminalDock .pane-terminal .xterm")).toBeVisible();
  await expect(page.locator("#ideSessionList")).toBeVisible();
  const editor = await page.locator(".ide-editor-area").boundingBox();
  const dock = await page.locator("#ideTerminalDock").boundingBox();
  expect(dock!.y).toBeGreaterThan(editor!.y);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator("#ideTerminalHide").click();
  await expect(page.locator("#ideTerminalDock")).toBeHidden();
  expect(await page.evaluate(() => (window as any).__e2eTerminalCloses?.length ?? 0)).toBe(0);
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .pane-terminal .xterm")).toBeVisible();
  expect(await page.evaluate(() => (window as any).__e2eTerminalStarts.length)).toBe(1);
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeHidden();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideTerminalDock .pane-terminal .xterm")).toBeVisible();
  expect(await page.evaluate(() => (window as any).__e2eTerminalStarts.length)).toBe(1);
});

test("an IDE terminal keeps running as chats in the same workspace change", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      { sessionId: "second-chat", cwd: "/home/tester/project", title: "Second chat", createdAt: 3, messageCount: 1 },
      { sessionId: "other-chat", cwd: "/other", title: "Other workspace", createdAt: 4, messageCount: 1 },
    ];
  });
  await page.reload();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList")).not.toContainText("Other workspace");
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator("#ideShowSessions").click();
  await page.locator('#ideSessionList [data-ide-session="second-chat"]').click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await page.locator("#ideTerminalDock .xterm-helper-textarea").focus();
  await page.keyboard.type("pwd");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBeGreaterThan(0);
  expect(await page.evaluate(() => (window as any).__e2eTerminalStarts.length)).toBe(1);
});

test("IDE terminal panel supports multiple shells, resizing, and closing", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await page.locator("#ideTerminalNew").click();
  await expect(page.locator("#ideTerminalDock [role=tab]")).toHaveCount(2);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(2);
  await expect(page.getByRole("tab", { name: "Terminal 2" })).toHaveAttribute("aria-selected", "true");
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.getByRole("tab", { name: "Terminal 2" })).toHaveAttribute("aria-selected", "true");
  const dock = page.locator("#ideTerminalDock");
  const before = (await dock.boundingBox())!.height;
  await page.getByRole("separator", { name: "Resize terminal" }).focus();
  await page.keyboard.press("ArrowUp");
  await expect.poll(async () => (await dock.boundingBox())!.height).toBeGreaterThan(before);
  await page.locator("#ideTerminalClose").click();
  await expect(page.locator("#ideTerminalDock [role=tab]")).toHaveCount(1);
  await page.locator("#ideTerminalClose").click();
  await expect(dock).toBeHidden();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalCloses?.length ?? 0)).toBe(2);
});

test("opening IDE with an existing chat keeps that conversation visible", async ({ page }) => {
  await page.locator("#agentTab").click();
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList")).toBeHidden();
  await expect(page.locator("#messages")).toBeVisible();
  await expect(page.locator("#messages")).toContainText("run the slow job");
});

test("workspace session list can load older chats beyond the initial page", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__e2eWorkspaceQueries = [];
    w.__paladinBaseInvoke = (method: string, args: any) => {
      if (method === "listSessions" && args.cwd) {
        w.__e2eWorkspaceQueries.push(args.limit);
        const count = args.limit === 0 ? 101 : 100;
        return Promise.resolve(
          Array.from({ length: count }, (_, i) => ({
            sessionId: `chat-${i}`,
            cwd: args.cwd,
            title: `Chat ${i}`,
            createdAt: i + 1,
            messageCount: 1,
          })),
        );
      }
      return base(method, args);
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList [data-ide-session]")).toHaveCount(100);
  await page.getByRole("button", { name: "Show all sessions" }).click();
  await expect(page.locator("#ideSessionList [data-ide-session]")).toHaveCount(101);
  expect(await page.evaluate(() => (window as any).__e2eWorkspaceQueries)).toContain(0);
});

test("IDE terminals stay with their workspace when choosing another folder", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionList button[data-fb-select$="/project"]').first().click();
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideTerminalDock")).toBeHidden();
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionCrumbs button[data-fb-crumb="/home/tester/project"]').click();
  await expect(page.locator("#newSessionPath")).toHaveText("/home/tester/project");
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideFolderName")).toHaveAttribute("title", "/home/tester/project");
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  expect(await page.evaluate(() => (window as any).__e2eTerminalStarts)).toHaveLength(1);
});

test("terminal-only commands open the IDE dock without creating a chat", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList")).toBeVisible();
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect(page.locator("#ideTerminalDock .xterm-rows")).toContainText("/home/tester/project");
  await expect(page.locator("#ideSessionList")).toBeVisible();
  expect(await page.evaluate(() => (window as any).__e2eOpens?.length ?? 0)).toBe(0);
});

test("terminal-only output follows the workspace if a chat opens while the command runs", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = (method: string, args: unknown) => {
      if (method === "terminalRun")
        return new Promise((resolve) => {
          w.__finishTerminalRun = resolve;
        });
      return base(method, args);
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList")).toBeVisible();
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => typeof (window as any).__finishTerminalRun)).toBe("function");
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await page.evaluate(() =>
    (window as any).__finishTerminalRun({ output: "finished-output", exitCode: 0, truncated: false }),
  );
  await expect(page.locator("#ideTerminalDock .xterm-rows")).toContainText("finished-output");
});

test("folder picker waits for the selected folder to finish loading", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = (method: string, args: unknown) => {
      if (method === "fsList")
        return new Promise((resolve) => {
          w.__finishFolderList = () => void base(method, args).then(resolve);
        });
      return base(method, args);
    };
  });
  await page.locator("#ideModeBtn").click();
  await page.locator("#ideChooseFolder").click();
  await expect(page.locator("#sessionStartBtn")).toBeDisabled();
  await expect.poll(() => page.evaluate(() => typeof (window as any).__finishFolderList)).toBe("function");
  await page.evaluate(() => (window as any).__finishFolderList());
  await expect(page.locator("#sessionStartBtn")).toBeEnabled();
});

test("terminal-only command output goes to the selected IDE terminal", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await page.locator("#ideTerminalNew").click();
  await expect(page.getByRole("tab", { name: "Terminal 2" })).toHaveAttribute("aria-selected", "true");
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect(page.locator("#ideTerminalDock .pane-terminal:visible .xterm-rows")).toContainText(
    "/home/tester/project",
  );
  await expect(page.getByRole("tab", { name: "Terminal 2" })).toHaveAttribute("aria-selected", "true");
});

test("agent browser remains visible alongside the IDE terminal", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: "sess-notify-1",
      url: "http://localhost:8080",
    }),
  );
  await expect(page.locator("#ideBrowserArea .pane-browser")).toBeVisible();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("IDE browser tabs share the editor area and preserve file drafts", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.locator("#ideEditor .cm-content").fill("unsaved draft");
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: "sess-notify-1",
      url: "http://localhost:8080",
    }),
  );
  await expect(page.locator("#ideBrowserArea .pane-browser")).toBeVisible();
  await expect(page.locator("#ideEditorWrap")).toBeHidden();
  await page.locator("#ideTabs [data-ide-tab]").filter({ hasText: "README.md" }).click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("unsaved draft");
  await expect(page.locator("#ideBrowserArea")).toBeHidden();
  await page.locator("#ideBrowserTabs .ide-tab-select").click();
  await expect(page.locator("#ideBrowserArea .pane-browser")).toBeVisible();
  await page.locator("#ideBrowserTabs .ide-tab-close").click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("unsaved draft");
});

test("personal browser moves into and out of the IDE editor with its native view", async ({ page }) => {
  await page.locator("#paneWebBtn").click();
  await page.locator("[data-web-address]").click();
  await page.locator(".web-url").fill("https://example.com/");
  await page.locator(".web-url").press("Enter");
  await expect(page.locator("#sidePane .pane-web")).toBeVisible();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideBrowserArea .pane-web")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewShown)).toBeTruthy();
  const bounds = await page.evaluate(() => (window as any).__e2eViewBounds);
  const surface = await page.locator("#ideBrowserArea .web-surface").boundingBox();
  expect(Math.abs(bounds.x - surface!.x)).toBeLessThan(2);
  expect(Math.abs(bounds.width - surface!.width)).toBeLessThan(2);
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#sidePane .pane-web")).toBeVisible();
  await expect(page.locator("#sidePane .web-url")).toHaveValue("https://example.com/");
});

test("a command finishing in another workspace does not interrupt the selected terminal", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = (method: string, args: unknown) => {
      if (method === "terminalRun")
        return new Promise((resolve) => {
          w.__finishTerminalRun = resolve;
        });
      return base(method, args);
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideSessionList")).toBeVisible();
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => typeof (window as any).__finishTerminalRun)).toBe("function");
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionList button[data-fb-select$="/project"]').first().click();
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideFolderName")).toHaveAttribute("title", "/home/tester/project/project");
  await page.locator("#ideTerminalToggle").click();
  await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
  await page.locator("#ideTerminalNew").click();
  await expect(page.getByRole("tab", { name: "Terminal 2" })).toHaveAttribute("aria-selected", "true");
  await page.evaluate(() =>
    (window as any).__finishTerminalRun({ output: "old-workspace-output", exitCode: 0, truncated: false }),
  );
  await expect(page.getByRole("tab", { name: "Terminal 2" })).toHaveAttribute("aria-selected", "true");
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionCrumbs button[data-fb-crumb="/home/tester/project"]').click();
  await expect(page.locator("#newSessionPath")).toHaveText("/home/tester/project");
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideTerminalDock .xterm-rows")).toContainText("old-workspace-output");
});
