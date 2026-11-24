// workedFold.spec.ts covers the "Worked for …" fold: once a turn ends, what the
// agent did before its final message folds into one disclosure; the user's
// message and the reply stay in the open. Live turns and history read alike.
import { expect, test, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

async function boot(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
}

test("a live turn folds its working into 'Worked for' when it ends", async ({ page }) => {
  await boot(page);
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRunTurn = async (_params: unknown, handlers: any) => {
      handlers.onDelta("Let me look first. ");
      handlers.onTool({ id: "t1", name: "shell", arguments: '{"command":"ls"}', result: "a.go", status: "ok" });
      await new Promise<void>((resolve) => (w.__release = resolve));
      handlers.onDelta("All done.");
      return { reply: "All done.", stats: { durationMs: 482_000, tokens: 40, genMs: 4000 } };
    };
  });
  await page.goto("/");
  await page.fill("#promptInput", "do it");
  await page.click("#sendBtn");

  // While it runs, the working is shown as it happens: nothing is folded.
  await expect(page.locator('#messages .tool-run, #messages [data-tool="shell"]').first()).toBeVisible();
  await expect(page.locator("details.worked-fold")).toHaveCount(0);

  await page.evaluate(() => (window as any).__release());
  const fold = page.locator("#messages details.worked-fold");
  await expect(fold).toHaveCount(1);
  await expect(fold.locator(":scope > summary")).toHaveText("Worked for 8m 2s");
  await expect(fold).not.toHaveAttribute("open", "");
  // The working is out of sight; the reply and the user's message are not.
  await expect(page.locator("#messages .tool-run").first()).toBeHidden();
  await expect(page.locator("#messages")).toContainText("do it");
  await expect(page.locator("#messages .turn-row > .agent-text").last()).toContainText("All done.");
  await expect(page.locator("#messages .turn-row > .agent-text").last()).toBeVisible();

  await fold.locator(":scope > summary").click();
  await expect(fold).toHaveAttribute("open", "");
  await expect(fold.locator(".tool-run").first()).toBeVisible();
  await expect(fold).toContainText("Let me look first.");
  // The reply is after the fold, never inside it.
  expect(await fold.locator(".agent-text", { hasText: "All done." }).count()).toBe(0);
});

test("a plain reply has nothing to fold", async ({ page }) => {
  await boot(page);
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");
  await expect(page.locator("details.worked-fold")).toHaveCount(0);
});

async function openHistory(page: Page, history: unknown[]): Promise<void> {
  await boot(page);
  await page.addInitScript((rows) => ((window as any).__e2eHistory = rows), history);
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-notify-1"]');
}

test("history folds the same way, and keeps the reply open", async ({ page }) => {
  const at = Math.floor(Date.now() / 1000);
  await openHistory(page, [
    { role: "user", content: "check it", createdAt: at, index: 0 },
    { role: "assistant", kind: "thinking", content: "Weighing it.", createdAt: at },
    { role: "assistant", content: "Looking now.", createdAt: at },
    { role: "tool", name: "read", content: "{}", arguments: '{"path":"a.go"}', status: "ok", createdAt: at },
    { role: "assistant", content: "It is fine.", createdAt: at, stats: { durationMs: 75_000, tokens: 9, genMs: 3000 } },
  ]);
  const fold = page.locator("#messages details.worked-fold");
  await expect(fold).toHaveCount(1);
  await expect(fold.locator(":scope > summary")).toHaveText("Worked for 1m 15s");
  await expect(page.locator("#messages")).toContainText("It is fine.");
  await expect(page.locator("#messages .think-block")).toBeHidden();
  await fold.locator(":scope > summary").click();
  await expect(page.locator("#messages .think-block")).toBeVisible();
  await expect(fold).toContainText("Looking now.");
});

test("a turn that never reached a reply is shown as it happened", async ({ page }) => {
  const at = Math.floor(Date.now() / 1000);
  await openHistory(page, [
    { role: "user", content: "check it", createdAt: at, index: 0 },
    { role: "assistant", content: "Looking now.", createdAt: at },
    { role: "tool", name: "read", content: "{}", arguments: '{"path":"a.go"}', status: "ok", createdAt: at },
  ]);
  await expect(page.locator("#messages")).toContainText("Looking now.");
  await expect(page.locator("details.worked-fold")).toHaveCount(0);
});

for (const outcome of ["stopped", "failed"]) {
  test(`a ${outcome} live turn keeps its partial reply and work visible`, async ({ page }) => {
    await boot(page);
    await page.addInitScript((outcome) => {
      (window as any).__e2eRunTurn = async (_params: unknown, handlers: any) => {
        handlers.onTool({ id: "t1", name: "read", arguments: '{"path":"a.go"}', result: "a", status: "ok" });
        handlers.onDelta("Partial answer.");
        if (outcome === "failed") throw new Error("provider went away");
        return { reply: "Partial answer.", stopped: true };
      };
    }, outcome);
    await page.goto("/");
    await page.fill("#promptInput", "do it");
    await page.click("#sendBtn");
    await expect(page.locator("#pauseBtn")).toBeHidden();
    await expect(page.locator("#messages").getByText("Partial answer.", { exact: true })).toBeVisible();
    await expect(page.locator("#messages .tool-run")).toBeVisible();
    await expect(page.locator("details.worked-fold")).toHaveCount(0);
  });
}

test("history keeps incomplete turns open and separates successive continuations", async ({ page }) => {
  const at = Math.floor(Date.now() / 1000);
  const stats = { durationMs: 2000, tokens: 9, genMs: 1000 };
  const tool = { role: "tool", name: "read", content: "{}", arguments: '{"path":"a.go"}', createdAt: at };
  await openHistory(page, [
    { role: "user", content: "check", createdAt: at, index: 0 },
    tool,
    { role: "assistant", content: "Partial answer.", createdAt: at, stats: { ...stats, incomplete: true } },
    // These have no user messages, but are still separate finished turns.
    tool,
    { role: "assistant", content: "First continuation.", createdAt: at, stats },
    tool,
    { role: "assistant", content: "Second continuation.", createdAt: at, stats },
  ]);
  await expect(page.locator("#messages .worked-fold")).toHaveCount(2);
  for (const text of ["Partial answer.", "First continuation.", "Second continuation."]) {
    await expect(page.locator("#messages").getByText(text, { exact: true })).toBeVisible();
  }
  await expect(page.locator("#messages .worked-body").getByText("First continuation.", { exact: true })).toHaveCount(0);
});

for (const finalReply of [true, false]) {
  test(`deliverables stay visible ${finalReply ? "after the fold" : "without a final reply or fold"}`, async ({
    page,
  }) => {
    const at = Math.floor(Date.now() / 1000);
    const history = [
      { role: "user", content: "draw and review", createdAt: at, index: 0 },
      { role: "assistant", kind: "thinking", content: "Check the details.", createdAt: at },
      { role: "assistant", content: "Making a diagram.", createdAt: at },
      {
        role: "tool",
        name: "visual",
        createdAt: at,
        arguments: JSON.stringify({
          title: "Flow",
          code: '<svg viewBox="0 0 100 30"><text x="5" y="20">Flow</text></svg>',
        }),
        content: JSON.stringify({ title: "Flow", format: "svg" }),
      },
      {
        role: "tool",
        name: "report_findings",
        arguments: "{}",
        content: '{"verdict":"looks_good","summary":"Clean.","findings":[]}',
        createdAt: at,
      },
      {
        role: "tool",
        name: "agent_question",
        toolCallId: "q1",
        createdAt: at,
        arguments: JSON.stringify({ questions: [{ question: "Next step?", options: ["Yes", "No"] }] }),
        content: '{"status":"pending"}',
      },
    ];
    await openHistory(page, [
      ...history,
      ...(finalReply ? [{ role: "assistant", content: "Done.", createdAt: at }] : []),
    ]);
    for (const selector of [".visual-card", ".findings-card", ".agent-q"]) {
      await expect(page.locator(`#messages ${selector}`)).toBeVisible();
      await expect(page.locator(`#messages .worked-body ${selector}`)).toHaveCount(0);
    }
    await expect(page.locator("details.worked-fold")).toHaveCount(finalReply ? 1 : 0);
  });
}

test("expanding long working keeps the disclosure in view instead of jumping to the reply", async ({ page }) => {
  const at = Math.floor(Date.now() / 1000);
  await openHistory(page, [
    { role: "user", content: "check", createdAt: at, index: 0 },
    { role: "assistant", content: "Working detail.\n\n".repeat(80), createdAt: at },
    { role: "assistant", content: "Done.", createdAt: at },
  ]);
  const summary = page.locator(".worked-fold > summary");
  await summary.click();
  await expect(page.locator(".worked-fold")).toHaveAttribute("open", "");
  await expect(summary).toBeInViewport();
  await summary.press("Enter");
  await expect(page.locator(".worked-fold")).not.toHaveAttribute("open", "");
});

for (const paged of [false, true]) {
  test(`incomplete pre-steer work stays open in ${paged ? "paged" : "full"} history`, async ({ page }) => {
    const at = Math.floor(Date.now() / 1000);
    const beforeSteer = [
      { role: "user", content: "check", createdAt: at, index: 0, incomplete: true },
      { role: "assistant", kind: "thinking", content: "Consider it.", createdAt: at, incomplete: true },
      { role: "assistant", content: "Commentary before steer.", createdAt: at, incomplete: true },
    ];
    await openHistory(page, [
      ...beforeSteer,
      ...(paged
        ? []
        : [
            { role: "user", content: "change course", createdAt: at, index: 3 },
            {
              role: "assistant",
              content: "Partial answer.",
              createdAt: at,
              stats: { durationMs: 5000, tokens: 9, genMs: 1000, incomplete: true },
            },
          ]),
    ]);
    await expect(page.locator("#messages").getByText("Commentary before steer.", { exact: true })).toBeVisible();
    await expect(page.locator("#messages .think-block")).toBeVisible();
    await expect(page.locator("details.worked-fold")).toHaveCount(0);
  });
}

test("a nonblocking Stop-hook message does not prevent folding the completed work", async ({ page }) => {
  await boot(page);
  await page.addInitScript(() => {
    (window as any).__e2eRunTurn = async (_params: unknown, handlers: any) => {
      handlers.onTool({ id: "t1", name: "read", arguments: '{"path":"a.go"}', result: "a", status: "ok" });
      handlers.onDelta("Done.");
      handlers.onHook({ event: "Stop", command: "notify", outcome: "message", message: "Saved." });
      return { reply: "Done.", stats: { durationMs: 1000, tokens: 9, genMs: 1000 } };
    };
  });
  await page.goto("/");
  await page.fill("#promptInput", "do it");
  await page.click("#sendBtn");
  await expect(page.locator("details.worked-fold")).toHaveCount(1);
  await expect(page.locator("#messages").getByText("Done.", { exact: true })).toBeVisible();
  await expect(page.locator("#messages .hook-row")).toBeVisible();
});
