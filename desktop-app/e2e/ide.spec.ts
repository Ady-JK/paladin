import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("IDE panel controls live in the workspace rail", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  const rail = page.locator("#rail");
  for (const id of ["Explorer", "Problems", "Terminal", "Activity"]) {
    await expect(rail.locator(`#ide${id}Toggle`)).toBeVisible();
  }
  await expect(rail.locator("#agentTab")).toBeVisible();
  await expect(rail.locator("#sessionChangesBtn")).toBeVisible();
  await expect(page.locator(".ide-panel-rail")).toHaveCount(0);
  await expect(page.locator(".ide-editor-bar #ideTerminalToggle")).toHaveCount(0);
  await rail.locator("#ideExplorerToggle").click();
  await expect(page.locator(".ide-explorer")).toBeHidden();
  await rail.locator("#ideExplorerToggle").click();
  await expect(page.locator(".ide-explorer")).toBeVisible();
});

test("Explorer floats beside the rail without resizing the editor", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  const explorer = page.locator(".ide-explorer");
  const sidebar = page.locator("#sidebar");
  const editor = page.locator(".ide-editor-area");
  const drawerBounds = (await explorer.boundingBox())!;
  const sidebarStyle = await sidebar.evaluate((el) => {
    const style = getComputedStyle(el);
    return { left: style.left, top: style.top, borderRadius: style.borderRadius };
  });
  const explorerStyle = await explorer.evaluate((el) => {
    const style = getComputedStyle(el);
    return { left: style.left, top: style.top, borderRadius: style.borderRadius, position: style.position };
  });
  expect(explorerStyle).toEqual({ ...sidebarStyle, position: "absolute" });
  const before = (await editor.boundingBox())!;
  expect(drawerBounds.x).toBeLessThan(before.x + before.width);
  await page.locator("#ideExplorerToggle").click();
  const after = (await editor.boundingBox())!;
  expect(after.x).toBe(before.x);
  expect(after.width).toBe(before.width);
  await page.locator("#ideExplorerToggle").click();
  await expect(explorer).toBeVisible();
  await page.locator("#ideEmpty").click();
  await expect(explorer).toBeHidden();
  await page.locator("#ideExplorerToggle").click();
  await page.locator("#ideChooseFolder").focus();
  await page.keyboard.press("Escape");
  await expect(explorer).toBeHidden();
  await expect(page.locator("#ideExplorerToggle")).toBeFocused();
});

test("Explorer uses the Sessions drawer transition and remains safe to toggle quickly", async ({ page }) => {
  await page.locator("#ideExplorerToggle").click();
  const explorer = page.locator(".ide-explorer");
  await expect(explorer).toBeVisible();
  const transitions = await page.evaluate(() => {
    const sidebar = document.querySelector("#sidebar")!;
    const wasClosed = sidebar.classList.contains("is-closed");
    sidebar.classList.remove("is-closed");
    const get = (selector: string) => {
      const style = getComputedStyle(document.querySelector(selector)!);
      return { property: style.transitionProperty, duration: style.transitionDuration };
    };
    const values = [get("#sidebar"), get(".ide-explorer")];
    sidebar.classList.toggle("is-closed", wasClosed);
    return values;
  });
  expect(transitions[1]).toEqual(transitions[0]);
  await page.locator("#ideExplorerToggle").click();
  await expect(explorer).toHaveClass(/is-closed/);
  await expect(explorer).toHaveAttribute("inert", "");
  await page.locator("#ideExplorerToggle").click();
  await expect(explorer).toBeVisible();
  await expect(explorer).not.toHaveAttribute("inert");
  await page.evaluate(() => document.documentElement.setAttribute("data-motion", "off"));
  await page.locator("#ideExplorerToggle").click();
  await expect(explorer).toBeHidden();
  await expect(explorer).toHaveCSS("transition-duration", "0s");
});

test("workspace tree heading collapses and restores its contents", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  const heading = page.locator("button.ide-root");
  await expect(heading).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: "README.md", exact: true })).toBeVisible();
  await heading.click();
  await expect(heading).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("button", { name: "README.md", exact: true })).toBeHidden();
  await heading.focus();
  await page.keyboard.press("Enter");
  await expect(heading).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: "README.md", exact: true })).toBeVisible();
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

test("IDE changes use the workspace for the file list and diff, then return to the editor", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChanges = { available: true, turns: [], files: [{ path: "README.md", status: "modified" }] };
    (window as any).__e2ePatch = "@@ -1 +1 @@\n-before\n+after";
  });
  await page.locator("#ideModeBtn").click();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await page.locator("#ideExplorerToggle").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.getByRole("button", { name: "Show changes" }).click();
  const panel = page.locator("#ideChangesPanel");
  await expect(panel).toBeVisible();
  await expect(page.locator("#ideEditorWrap")).toBeHidden();
  await panel.getByRole("button", { name: /README.md/ }).click();
  await expect(panel.locator(".change-diff")).toContainText("+after");
  const files = (await panel.locator(".change-files").boundingBox())!;
  const diff = (await panel.locator(".change-diff").boundingBox())!;
  expect(diff.x).toBeGreaterThan(files.x + files.width - 1);
  await page.locator("#ideFileTabs [data-ide-tab]").click();
  await expect(panel).toBeHidden();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("Hello from file");
});

test("IDE changes show edited files without an empty editor", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChanges = {
      available: true,
      turns: [],
      files: [
        { path: "src/main.go", status: "modified" },
        { path: "src/helper.go", status: "created" },
      ],
    };
    (window as any).__e2ePatch = "@@ -1 +1 @@\n-old\n+new";
  });
  await page.locator("#ideModeBtn").click();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await page.getByRole("button", { name: "Show changes" }).click();
  const panel = page.locator("#ideChangesPanel");
  await expect(page.locator("#ideEmpty")).toBeHidden();
  await expect(panel.locator(".change-files")).toContainText("src/main.go");
  await expect(panel.locator(".change-files")).toContainText("src/helper.go");
  await panel.getByRole("button", { name: /src\/helper.go/ }).click();
  await expect(panel.locator(".change-diff")).toContainText("+new");
  await page.getByRole("button", { name: "Hide changes" }).click();
  await expect(page.locator("#ideEmpty")).toBeVisible();
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
      turn: {
        turnId: "activity-turn",
        sessionId: "sess-notify-1",
        cwd: "/home/tester/project",
        message: "run the slow job",
        state: "running",
      },
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
  await page.locator("#ideModeBtn").click();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("unsaved");
});

test("an externally changed background file is marked and shows a notice when opened", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.locator("#ideExplorerToggle").click();
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
  await page.locator("#ideExplorerToggle").click();
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
  await page.getByRole("button", { name: "Switch workspace" }).click();
  await expect(page.locator("#sessionModal")).toBeVisible();
  await expect(page.locator("#sessionDialogTitle")).toHaveText("Open folder");
  await page.locator("#sessionCancelBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await expect(page.locator("#ideWorkspace")).toBeVisible();
});

test("reopening the current workspace keeps the active conversation and draft", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await page.locator("#promptInput").fill("unfinished message");
  await page.locator("#ideExplorerToggle").click();
  await page.getByRole("button", { name: "Switch workspace" }).click();
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await expect(page.locator("#messages")).toContainText("run the slow job");
  await expect(page.locator("#promptInput")).toHaveValue("unfinished message");
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
  await page.locator("#ideExplorerToggle").click();
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
  await page.evaluate(() => window.dispatchEvent(new Event("paladin:ide-toggle-agent")));
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#ideChatResize")).toBeHidden();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#agentMain")).toBeVisible();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await page.getByRole("button", { name: "Show explorer" }).click();
  await page.evaluate(() => window.dispatchEvent(new Event("paladin:ide-toggle-agent")));
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
  await expect(page.locator("#rail #ideModeBtn")).toBeVisible();
  await expect(page.locator("#titleBar #ideModeBtn")).toHaveCount(0);
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
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewShown)).toBe("web:t1");
  const bounds = await page.evaluate(() => (window as any).__e2eViewBounds);
  const surface = await page.locator("#ideBrowserArea .web-surface").boundingBox();
  expect(Math.abs(bounds.x - surface!.x)).toBeLessThan(2);
  expect(Math.abs(bounds.width - surface!.width)).toBeLessThan(2);
  await page.locator("#ideExplorerToggle").click();
  await expect(page.locator(".ide-explorer")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewShown)).toBe("agent");
  await page.locator("#ideExplorerToggle").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewShown)).toBe("web:t1");
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
  if (await page.locator(".ide-explorer.is-closed").count()) await page.locator("#ideExplorerToggle").click();
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
  if (await page.locator(".ide-explorer.is-closed").count()) await page.locator("#ideExplorerToggle").click();
  await page.locator("#ideChooseFolder").click();
  await page.locator('#newSessionCrumbs button[data-fb-crumb="/home/tester/project"]').click();
  await expect(page.locator("#newSessionPath")).toHaveText("/home/tester/project");
  await page.locator("#sessionStartBtn").click();
  await expect(page.locator("#ideTerminalDock .xterm-rows")).toContainText("old-workspace-output");
});

test("composer controls fit inside a narrow IDE chat pane", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 650 });
  await page.locator("#ideModeBtn").click();
  await page.locator('#ideSessionList [data-ide-session="sess-notify-1"]').click();
  const composer = page.locator(".composer-float");
  const bounds = await composer.boundingBox();
  expect(bounds).not.toBeNull();
  for (const selector of ["#modelSelectBtn", "#toolsChip", "#budgetChip", "#sendBtn"]) {
    const control = page.locator(selector);
    await expect(control).toBeVisible();
    const box = await control.boundingBox();
    expect(box!.width).toBeGreaterThan(selector === "#modelSelectBtn" ? 55 : 10);
    expect(box!.x).toBeGreaterThanOrEqual(bounds!.x - 1);
    expect(box!.x + box!.width).toBeLessThanOrEqual(bounds!.x + bounds!.width + 1);
  }
});

test("the rail keeps shared controls in place and exposes activity in Agent mode", async ({ page }) => {
  const rail = page.locator("#rail");
  await expect(rail.locator("#ideExplorerToggle")).toBeVisible();
  await expect(rail.locator("#ideProblemsToggle")).toBeHidden();
  await rail.locator("#ideActivityToggle").click();
  await expect(page.locator("#ideActivityList")).toBeVisible();
  await page.keyboard.press("Escape");
  const modePosition = (await rail.locator("#ideModeBtn").boundingBox())!.y;
  await rail.locator("#ideModeBtn").click();
  await expect(rail.locator("#ideProblemsToggle")).toBeVisible();
  expect((await rail.locator("#ideModeBtn").boundingBox())!.y).toBe(modePosition);
  expect((await rail.locator("#ideProblemsToggle").boundingBox())!.y).toBeGreaterThan(modePosition);
  await rail.locator("#ideModeBtn").click();
  await expect(rail.locator("#ideActivityToggle")).toBeVisible();
  await expect(rail.locator("#ideTerminalToggle")).toBeVisible();
  await expect(rail.locator("#sessionChangesBtn")).toBeVisible();
  await rail.locator("#ideTerminalToggle").click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
});

test("Files stays in Agent mode and only the IDE mode toggle stays highlighted", async ({ page }) => {
  const rail = page.locator("#rail");
  await rail.locator("#ideExplorerToggle").click();
  await expect(rail.locator("#ideModeBtn")).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByRole("button", { name: "README.md", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await expect(rail.locator("#ideModeBtn")).toHaveAttribute("aria-pressed", "false");
  await rail.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await expect(page.locator("#ideEditor .cm-content")).toHaveText("Hello from file");
  await rail.locator("#ideActivityToggle").click();
  await page.mouse.move(500, 500);
  const background = (id: string) => rail.locator(id).evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(await background("#ideActivityToggle")).toBe("rgba(0, 0, 0, 0)");
  expect(await background("#ideModeBtn")).not.toBe("rgba(0, 0, 0, 0)");
});

test("shared rail actions do not switch to IDE mode", async ({ page }) => {
  const rail = page.locator("#rail");
  const mode = rail.locator("#ideModeBtn");
  for (const id of ["#agentTab", "#sessionChangesBtn", "#ideTerminalToggle", "#ideActivityToggle"]) {
    await rail.locator(id).click();
    await expect(mode).toHaveAttribute("aria-pressed", "false");
  }
});

test("IDE changes selection does not remain active in Agent mode", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  const changes = page.locator("#sessionChangesBtn");
  await changes.click();
  await expect(changes).toHaveAttribute("aria-pressed", "true");
  await page.locator("#ideModeBtn").click();
  await expect(changes).not.toHaveAttribute("aria-pressed", "true");
  await expect(changes).toHaveAttribute("aria-label", "Session changes");
});

test("IDE palette commands use the shared rail controls", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  const palette = page.getByRole("dialog", { name: "Quick open" });
  const shortcut = process.platform === "darwin" ? "Meta+Shift+p" : "Control+Shift+p";
  await page.keyboard.press(shortcut);
  await palette.getByRole("textbox").fill(">toggle agent");
  await palette.getByRole("textbox").press("Enter");
  await expect(page.locator("#agentMain")).toBeHidden();
  await page.keyboard.press(shortcut);
  await palette.getByRole("textbox").fill(">show or hide changes");
  await palette.getByRole("textbox").press("Enter");
  await expect(page.locator("#ideChangesPanel")).toBeVisible();
});

test("sessions lead the rail and both drawers fit narrow windows", async ({ page }) => {
  const buttons = page.locator("#railPanels > div button, #railPanels > button");
  await expect(buttons.nth(0)).toHaveId("agentTab");
  await expect(buttons.nth(0)).toHaveAccessibleName("Sessions");
  await expect(buttons.nth(0).locator('[data-icon="home"]')).toHaveCount(1);
  await expect(buttons.nth(1)).toHaveId("ideExplorerToggle");
  await page.locator("#ideExplorerToggle").click();
  const explorer = page.locator(".ide-explorer");
  await expect(explorer).toBeVisible();
  await expect(explorer.getByText("Files", { exact: true })).toBeVisible();
  expect((await explorer.boundingBox())!.width).toBeGreaterThan(300);
  await page.locator("#agentTab").click();
  const sidebar = page.locator("#sidebar");
  await expect(sidebar).toBeVisible();
  expect((await sidebar.boundingBox())!.width).toBeGreaterThan(300);
  await page.setViewportSize({ width: 375, height: 700 });
  const bounds = (await sidebar.boundingBox())!;
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(375);
});

test("Sessions and Files open the same floating drawers in IDE mode", async ({ page }) => {
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#agentMain")).toBeVisible();
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator(".ide-explorer")).toBeHidden();
  await page.locator("#ideExplorerToggle").click();
  await expect(page.locator(".ide-explorer")).toBeVisible();
  await expect(page.locator("#sidebar")).toBeHidden();
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeVisible();
  await expect(page.locator(".ide-explorer")).toBeHidden();
  await expect(page.locator("#ideModeBtn")).toHaveAttribute("aria-pressed", "true");
});

test("opening Sessions wins over a pending IDE layout restore", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const original = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = (method: string, params: unknown) => {
      if (method === "ideLayoutGet")
        return new Promise((resolve) => {
          w.__releaseIdeLayout = () => resolve({ explorerVisible: true, agentVisible: true, files: [], expanded: [] });
        });
      return original(method, params);
    };
  });
  await page.locator("#ideModeBtn").click();
  await expect.poll(() => page.evaluate(() => typeof (window as any).__releaseIdeLayout)).toBe("function");
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeVisible();
  await page.evaluate(async () => {
    (window as any).__releaseIdeLayout();
    await new Promise(requestAnimationFrame);
    await new Promise(requestAnimationFrame);
  });
  await expect(page.locator(".ide-explorer")).toBeHidden();
  await expect(page.locator("#sidebar")).toBeVisible();
});

test("Sessions stays open when the IDE loads slowly", async ({ page }) => {
  let release!: () => void;
  let requested = false;
  const ready = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(/\/src\/features\/ide\.ts(?:\?|$)/, async (route) => {
    requested = true;
    await ready;
    await route.continue();
  });
  await page.locator("#ideModeBtn").click();
  await expect.poll(() => requested).toBe(true);
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeVisible();
  release();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await expect(page.locator("#sidebar")).toBeVisible();
  await expect(page.locator(".ide-explorer")).toBeHidden();
});
