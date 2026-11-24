import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("Codex discovery starts automatically without holding up the composer or selecting a session", async ({
  page,
}) => {
  await page.addInitScript(() => {
    (window as any).__e2eDiscoveryPending = true;
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eDiscoveryStarts)).toBe(1);
  await page.fill("#promptInput", "keep my draft");
  await page.evaluate(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "auto-codex",
        cwd: "/home/tester/codex",
        title: "Automatically discovered",
        provider: "codex",
        harness: "codex",
        messageCount: 0,
      },
    ];
    (window as any).__e2eHub.emit({ event: "sessions/changed" });
  });
  await showSidebar(page);
  const row = page.locator('#sessionList .session-row:has([data-session="auto-codex"])');
  await expect(row).toContainText("Automatically discovered");
  await expect(row.locator(".session-harness")).toHaveText("Codex");
  await expect(page.locator("#codexThreadsBtn, #codexThreadsModal")).toHaveCount(0);
  await expect(page.locator("#promptInput")).toHaveValue("keep my draft");
  await expect(page.locator("body")).not.toHaveClass(/harness-codex/);
});

test("Codex discovery restarts automatically when the agent host restarts", async ({ page }) => {
  await expect.poll(() => page.evaluate(() => (window as any).__e2eDiscoveryStarts)).toBe(1);
  await page.evaluate(() => {
    (window as any).__e2eHub.emit({ event: "host/restarted" });
  });
  await expect.poll(() => page.evaluate(() => (window as any).__e2eDiscoveryStarts)).toBe(2);
  await expect(page.locator("#promptInput")).toBeEnabled();
});

test("New Session is the one start button, and its dialog offers Codex when the CLI is installed", async ({ page }) => {
  await showSidebar(page);
  await expect(page.locator("#newCodexSessionBtn")).toHaveCount(0);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionDialogTitle")).toHaveText("New Session");
  await expect(page.locator("#sessionStartBtn")).toHaveText("Paladin");
  await expect(page.locator("#sessionStartCodexBtn")).toBeEnabled();
  // Looking for the CLI starts nothing: only a shallow status check ran.
  const calls = await page.evaluate(() => (window as any).__e2eCodexCalls);
  expect(calls.every((call: { deep: boolean }) => call.deep === false)).toBe(true);
});

test("the Codex choice says why it is unavailable when the CLI is missing", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.addInitScript(() => {
    (window as any).__e2eCodex = { installed: false, loggedIn: false, models: [] };
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await showSidebar(page);
  await expect(page.locator("#codexThreadsBtn")).toHaveCount(0);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionStartCodexBtn")).toBeDisabled();
  await expect(page.locator("#sessionStartCodexBtn")).toHaveAttribute("title", /not found|install/i);
  // Paladin still starts.
  await expect(page.locator("#sessionStartBtn")).toBeEnabled();
  await page.click("#sessionStartBtn");
  await page.fill("#promptInput", "hello without Codex");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello without Codex");
  expect(errors).toEqual([]);
});

test("Codex installed after launch is picked up the next time the dialog opens", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eCodex = { installed: false, loggedIn: false, models: [] };
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionStartCodexBtn")).toBeDisabled();
  await page.keyboard.press("Escape");
  await page.evaluate(() => {
    (window as any).__e2eCodex = {
      installed: true,
      loggedIn: true,
      models: [{ id: "gpt-5.6-luna", displayName: "GPT-5.6-Luna", efforts: ["low"], isDefault: true }],
    };
  });
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionStartCodexBtn")).toBeEnabled();
  await expect(page.locator("#codexThreadsBtn")).toHaveCount(0);
});

test("a Codex session picks Codex models and opens with the harness", async ({ page }) => {
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await page.click("#sessionStartCodexBtn");

  // Codex's models replace the provider catalog in the picker.
  await expect(page.locator("#modelSelectProvider")).toHaveText("codex");
  await expect(page.locator("#modelSelectLabel")).toContainText("sol");
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelTree [data-mpick]")).toHaveCount(2);
  await page.locator('#modelTree [data-mpick$="gpt-5.6-luna"]').click();
  await expect(page.locator("#modelSelectLabel")).toContainText("luna");
  await expect(page.locator("body")).toHaveClass(/harness-codex/);

  await page.fill("#promptInput", "hello codex");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello codex");

  const opens = await page.evaluate(() => (window as any).__e2eOpens);
  const created = opens.find((open: { harness?: string }) => open.harness);
  expect(created).toMatchObject({ harness: "codex", model: "gpt-5.6-luna" });
  const submits = await page.evaluate(() => (window as any).__e2eSubmits);
  expect(submits[0]).toMatchObject({ provider: "codex", model: "gpt-5.6-luna" });
});

test("a plain New Session leaves Codex mode and restores the model", async ({ page }) => {
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await page.click("#sessionStartCodexBtn");
  await expect(page.locator("body")).toHaveClass(/harness-codex/);

  await showSidebar(page);
  await page.click("#newSessionBtn");
  await page.click("#sessionStartBtn");
  await expect(page.locator("body")).not.toHaveClass(/harness-codex/);
  await expect(page.locator("#modelSelectProvider")).not.toHaveText("codex");
});

test("a Codex session in the list carries a badge and reopens in Codex mode", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "sess-codex-9",
        cwd: "/home/tester/project",
        createdAt: Math.floor(Date.now() / 1000),
        provider: "codex",
        model: "gpt-5.6-luna",
        messageCount: 2,
        title: "codex chat",
        harness: "codex",
      },
    ];
    (window as any).__e2eOpenExtra = {
      "sess-codex-9": { provider: "codex", model: "gpt-5.6-luna", harness: "codex" },
    };
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await showSidebar(page);
  const row = page.locator("#sessionList .session-row", { hasText: "codex chat" });
  await expect(row.locator(".session-harness")).toHaveText("Codex");
  await row.locator(".session-pick").click();
  await expect(page.locator("body")).toHaveClass(/harness-codex/);
  await expect(page.locator("#modelSelectLabel")).toContainText("luna");
});

test("a visible linked Codex conversation refreshes on focus without clearing the draft", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "sess-sync",
        cwd: "/home/tester/project",
        title: "Linked thread",
        harness: "codex",
        messageCount: 2,
      },
    ];
    (window as any).__e2eOpenExtra = { "sess-sync": { provider: "codex", model: "gpt-5.6-luna", harness: "codex" } };
    (window as any).__e2eHistories = {
      "sess-sync": [
        { role: "user", content: "first prompt", createdAt: 1, index: 0 },
        { role: "assistant", content: "first answer", createdAt: 1 },
      ],
    };
  });
  await page.reload();
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-sync"]').click();
  await expect(page.locator("#messages")).toContainText("first answer");
  await page.fill("#promptInput", "keep this draft");
  await page.evaluate(() => {
    (window as any).__e2eSyncHistory = [
      ...(window as any).__e2eHistories["sess-sync"],
      { role: "user", content: "continued in Codex", createdAt: 2, index: 2 },
      { role: "assistant", content: "new answer from Codex", createdAt: 2 },
    ];
    window.dispatchEvent(new Event("focus"));
  });
  await expect(page.locator("#messages")).toContainText("new answer from Codex");
  await expect(page.locator("#promptInput")).toHaveValue("keep this draft");
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page.getByText("new answer from Codex", { exact: true })).toHaveCount(1);
});

test("a linked Codex conversation also refreshes while the window stays open", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "sess-sync",
        cwd: "/home/tester/project",
        title: "Linked thread",
        harness: "codex",
        messageCount: 0,
      },
    ];
    (window as any).__e2eOpenExtra = { "sess-sync": { provider: "codex", model: "gpt-5.6-luna", harness: "codex" } };
    (window as any).__e2eHistories = { "sess-sync": [] };
  });
  await page.reload();
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-sync"]').click();
  await expect(page.locator("body")).toHaveClass(/harness-codex/);
  await page.evaluate(() => {
    (window as any).__e2eSyncHistory = [{ role: "assistant", content: "periodically synced answer", createdAt: 1 }];
  });
  await expect(page.locator("#messages")).toContainText("periodically synced answer", { timeout: 8000 });
});

test("Codex mode drops Paladin's own controls and brings them back in a Paladin session", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eCodex = {
      installed: true,
      loggedIn: true,
      models: [{ id: "gpt-5.6-luna", displayName: "GPT-5.6-Luna", efforts: ["low"], isDefault: true }],
    };
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();

  await showSidebar(page);
  await page.click("#newSessionBtn");
  await page.click("#sessionStartCodexBtn");

  await expect(page.locator("#promptInput")).toHaveAttribute("placeholder", /Ask Codex/);
  for (const own of [".tools-wrap", ".mcp-chip", ".budget-wrap", ".context-control"]) {
    await expect(page.locator(own)).toBeHidden();
  }

  await page.fill("#promptInput", "hi");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hi");

  // Back in a Paladin session every control returns.
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await page.click("#sessionStartBtn");
  await expect(page.locator("#promptInput")).toHaveAttribute("placeholder", /Ask paladin/);
});

async function startCodexSession(page: Page): Promise<void> {
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await page.click("#sessionStartCodexBtn");
  await expect(page.locator("body")).toHaveClass(/harness-codex/);
}

test("Codex compaction without a summary shows a quiet divider live and after reopening", async ({
  page,
}, testInfo) => {
  await startCodexSession(page);
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");
  await page.evaluate(() => {
    (window as any).__e2eRunTurn = async (_params: unknown, handlers: any) => {
      handlers.onCompact({ phase: "start", reason: "manual" });
      await new Promise((resolve) => setTimeout(resolve, 300));
      handlers.onCompact({ phase: "done", reason: "manual" });
      return { reply: "" };
    };
  });
  await page.fill("#promptInput", "/compact");
  await page.keyboard.press("Enter");
  const divider = page.locator('#messages [data-notice="compaction"]');
  await expect(divider.locator(".chat-divider-label")).toHaveText("Context compacted");
  await expect(divider.locator(".chat-divider-summary")).toHaveCount(0);
  await expect(page.locator("#messages .bubble-user").last()).toHaveText("/compact");
  await expect(page.locator("#messages")).not.toContainText("Context compacted.");

  // Reopen the renderer with the persisted projection returned by the core.
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eExtraSessions = [
      {
        sessionId: "sess-codex-1",
        cwd: "/home/tester/project",
        title: "Compaction proof",
        harness: "codex",
        messageCount: 2,
      },
    ];
    w.__e2eOpenExtra = { "sess-codex-1": { provider: "codex", harness: "codex" } };
    w.__e2eHistories = {
      "sess-codex-1": [
        { role: "user", content: "/compact", createdAt: 1, index: 0 },
        {
          role: "system",
          kind: "compaction",
          content: "",
          compaction: { reason: "manual", tokensBefore: 0 },
          createdAt: 1,
        },
      ],
    };
  });
  await page.reload();
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-codex-1"]').first().click();
  await expect(divider).toHaveCount(1);
  await expect(divider.locator(".chat-divider-label")).toHaveText("Context compacted");
  await expect(page.locator("#messages .bubble-user")).toHaveText("/compact");
  // The sessions column stays open after a pick now: it is a column, not a
  // popover that dismisses itself.
  await expect(page.locator("#sidebar")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("codex-compaction.png") });
});

test("the slash palette in Codex mode lists Codex's commands", async ({ page }) => {
  await startCodexSession(page);
  await page.fill("#promptInput", "/");
  const names = await page
    .locator("#slashPopup .slash-row")
    .evaluateAll((rows) => rows.map((r) => (r as HTMLElement).dataset.name));
  expect(names).toEqual(
    expect.arrayContaining(["compact", "goal", "review", "feedback", "mcp", "fast", "new", "fork", "undo"]),
  );
  expect(names).not.toContain("memories");
  expect(names).not.toContain("tools");
  expect(names).not.toContain("skill");
});

test("Codex's own commands reach the core as the message", async ({ page }) => {
  await startCodexSession(page);
  // The first message opens the session; commands need one.
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");
  for (const line of [
    "/compact",
    "/goal all tests pass",
    "/review base main",
    "/feedback it broke",
    "/mcp",
    "/fast on",
    "/init",
  ]) {
    await page.fill("#promptInput", line);
    await page.keyboard.press("Enter");
    await expect(page.locator("#messages")).toContainText(`Stub reply to: ${line}`);
  }
  const submits = await page.evaluate(() => (window as any).__e2eSubmits);
  expect(submits.map((s: { message: string }) => s.message)).toEqual([
    "hello",
    "/compact",
    "/goal all tests pass",
    "/review base main",
    "/feedback it broke",
    "/mcp",
    "/fast on",
    "/init",
  ]);
  // None of them is Paladin's own compaction or prompt command.
  expect(submits.every((s: { compact: boolean; command: unknown }) => !s.compact && !s.command)).toBe(true);
});

test("/new in Codex mode starts another Codex session", async ({ page }) => {
  await startCodexSession(page);
  await page.fill("#promptInput", "/new");
  await page.keyboard.press("Enter");
  await expect(page.locator("body")).toHaveClass(/harness-codex/);
  await expect(page.locator("#modelSelectProvider")).toHaveText("codex");
});

test("plan mode in Codex mode sends the plan mode with the message", async ({ page }) => {
  await startCodexSession(page);
  await page.fill("#promptInput", "/plan add a hello function");
  await page.keyboard.press("Enter");
  await expect(page.locator("#messages")).toContainText("Stub reply to: add a hello function");
  await expect(page.locator("#modeChip")).toBeVisible();
  const submits = await page.evaluate(() => (window as any).__e2eSubmits);
  expect(submits[0]).toMatchObject({ message: "add a hello function", mode: "plan" });
  await page.fill("#promptInput", "/build");
  await page.keyboard.press("Enter");
  await expect(page.locator("#modeChip")).toBeHidden();
  await page.fill("#promptInput", "now do it");
  await page.keyboard.press("Enter");
  await expect(page.locator("#messages")).toContainText("Stub reply to: now do it");
  const after = await page.evaluate(() => (window as any).__e2eSubmits);
  expect(after[1]).toMatchObject({ message: "now do it", mode: "build" });
});

test.describe("the $ skill menu", () => {
  async function startCodex(page: Page): Promise<void> {
    await showSidebar(page);
    await page.click("#newSessionBtn");
    await page.click("#sessionStartCodexBtn");
    await expect(page.locator("#modelSelectProvider")).toHaveText("codex");
  }

  test("typing $ in a Codex session lists its skills and inserts the pick", async ({ page }) => {
    await startCodex(page);
    await page.fill("#promptInput", "please use $");
    const rows = page.locator("#skillPopup [data-skill]");
    await expect(rows).toHaveCount(2);
    await expect(rows.first()).toContainText("guava");
    await expect(rows.first()).toContainText("Answers with a fixed word.");

    await page.fill("#promptInput", "please use $doc");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("documents:documents");

    await page.fill("#promptInput", "please use $gu");
    await expect(rows).toHaveCount(1);
    await page.keyboard.press("Enter");
    await expect(page.locator("#promptInput")).toHaveValue("please use $guava ");
    await expect(page.locator("#skillPopup")).toBeHidden();
    // Enter picked the skill; it did not send the message.
    expect(await page.evaluate(() => (window as any).__e2eSubmits ?? [])).toHaveLength(0);
  });

  test("a price or a variable is not a skill", async ({ page }) => {
    await startCodex(page);
    await page.fill("#promptInput", "it costs 5$ and a$b");
    await expect(page.locator("#skillPopup")).toBeHidden();
  });

  test("a Paladin session has no $ menu and asks Codex for nothing", async ({ page }) => {
    await page.fill("#promptInput", "use $");
    await expect(page.locator("#skillPopup")).toBeHidden();
    expect(await page.evaluate(() => (window as any).__e2eSkillCalls ?? [])).toHaveLength(0);
  });

  test("Escape closes the menu", async ({ page }) => {
    await startCodex(page);
    await page.fill("#promptInput", "$");
    await expect(page.locator("#skillPopup")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator("#skillPopup")).toBeHidden();
  });
});

test("every session shows which engine runs it, in Projects and in Recents", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "sess-codex-9",
        cwd: "/home/tester/project",
        createdAt: Math.floor(Date.now() / 1000) + 10,
        provider: "codex",
        model: "gpt-5.6-luna",
        messageCount: 2,
        title: "codex chat",
        harness: "codex",
      },
    ];
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await showSidebar(page);
  const projects = page.locator("#sessionList .session-row");
  await expect(projects.filter({ hasText: "codex chat" }).locator(".session-harness")).toHaveText("Codex");
  const plain = projects.filter({ hasNotText: "codex chat" }).first();
  await expect(plain.locator(".session-harness")).toHaveText("Paladin");
  const recents = page.locator("#recentList .recent-row");
  await expect(recents.filter({ hasText: "codex chat" }).locator(".session-harness")).toHaveText("Codex");
  await expect(recents.filter({ hasNotText: "codex chat" }).first().locator(".session-harness")).toHaveText("Paladin");
});
