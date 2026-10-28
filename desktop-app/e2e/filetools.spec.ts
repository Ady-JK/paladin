// filetools.spec.ts covers the write/edit rich tool views.
// It stubs a demo session and checks file view, diff, and matches.
import { test, expect } from "@playwright/test";

const SESSION = "sess-filetools-1";

// Covers every filetools test with a stubbed file-tools session.
// It opens the demo session before each test runs.
test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const at = Math.floor(Date.now() / 1000);
    const history = [
      { role: "user", content: "Create an app config and switch it to the new port.", createdAt: at },
      { role: "assistant", content: "Creating the config file.", createdAt: at },
      {
        role: "tool",
        content:
          '{"requested_path":"/home/tester/project/app.conf","resolved_path":"/home/tester/project/app.conf","scope":"inside","created":true,"overwritten":false,"bytes":41,"lines":3}',
        createdAt: at,
        name: "write",
        arguments:
          '{"path":"/home/tester/project/app.conf","content":"[server]\\nhost = \\"localhost\\"\\nport = 8080\\n"}',
        status: "ok",
      },
      { role: "assistant", content: "Bumping the port.", createdAt: at },
      {
        role: "tool",
        content:
          '{"requested_path":"/home/tester/project/app.conf","resolved_path":"/home/tester/project/app.conf","scope":"inside","replacements":1,"bytes":41,"lines":3,"match_start_line":3,"match_end_line":3,"matches":[{"start_line":3,"end_line":3}]}',
        createdAt: at,
        name: "edit",
        arguments:
          '{"path":"/home/tester/project/app.conf","old_string":"port = 8080","new_string":"port = 9090"}',
        status: "ok",
      },
      {
        role: "tool",
        content: JSON.stringify({
          error: "edit: old text occurs 4 times; retry with more context around the one you want, or replace all",
          requested_path: "/home/tester/project/app.conf",
          resolved_path: "/home/tester/project/app.conf",
          scope: "inside",
          count: 4,
          occurrences: [
            { occurrence: 1, start_line: 2, end_line: 2, before: { line: 1, text: "[server]" }, after: { line: 3, text: "port = 8080" } },
            { occurrence: 2, start_line: 2, end_line: 2, before: { line: 1, text: "[server]" }, after: { line: 3, text: "port = 8080" } },
            { occurrence: 3, start_line: 2, end_line: 2, before: { line: 1, text: "[server]" }, after: { line: 3, text: "port = 8080" } },
            { occurrence: 4, start_line: 3, end_line: 3, before: { line: 2, text: 'host = "localhost"' }, after: null },
          ],
          hint: "copy more surrounding lines into old_string so it matches exactly once, or pass replace_all true",
        }),
        createdAt: at,
        name: "edit",
        arguments: '{"path":"/home/tester/project/app.conf","old_string":"o","new_string":"0"}',
        status: "failed",
      },
      { role: "assistant", content: "Port bumped; the broad retry needs more context.", createdAt: at },
      {
        role: "tool",
        content: JSON.stringify({
          error: 'write: path "/home/tester/project/app.conf" already exists; pass overwrite to replace it',
          requested_path: "/home/tester/project/app.conf",
          resolved_path: "/home/tester/project/app.conf",
          scope: "inside",
          exists: true,
        }),
        createdAt: at,
        name: "write",
        arguments: '{"path":"/home/tester/project/app.conf","content":"stale\\n"}',
        status: "failed",
      },
      {
        role: "tool",
        content:
          '{"requested_path":"/home/tester/project/app.conf","resolved_path":"/home/tester/project/app.conf","scope":"inside","created":false,"overwritten":true,"bytes":6,"lines":1}',
        createdAt: at,
        name: "write",
        arguments: '{"path":"/home/tester/project/app.conf","content":"fresh\\n","overwrite":true}',
        status: "ok",
      },
      {
        role: "tool",
        content:
          '{"requested_path":"/home/tester/project/app.conf","resolved_path":"/home/tester/project/app.conf","scope":"inside","replacements":1,"bytes":44,"lines":3,"match_start_line":1,"match_end_line":2,"matches":[{"start_line":1,"end_line":2}]}',
        createdAt: at,
        name: "edit",
        arguments: JSON.stringify({
          path: "/home/tester/project/app.conf",
          old_string: "[server]\nhost = \"localhost\"",
          new_string: "[server]\nhost = \"0.0.0.0\"",
          replace_all: true,
        }),
        status: "ok",
      },
      { role: "assistant", content: "Config rewritten from the retry.", createdAt: at },
    ];
    const paladin = (window as any).paladin;
    const prev = paladin.invoke.bind(paladin);
    paladin.invoke = async (cmd: string, args: any) => {
      if (cmd === "list_sessions") {
        return [
          {
            sessionId: "sess-filetools-1",
            cwd: "/home/tester/project",
            createdAt: at,
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: history.length,
            title: "file tools demo",
          },
        ];
      }
      if (cmd === "open_session") {
        return { sessionId: "sess-filetools-1", cwd: "/home/tester/project", provider: "Opencode Go", model: "openai/gpt-4o" };
      }
      if (cmd === "session_history") {
        return history;
      }
      return prev(cmd, args);
    };
  });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
  await page.click(`[data-session="${SESSION}"]`);
  await expect(page.locator("#messages")).toContainText("Port bumped");
});

// Verifies write and edit rows render with path summaries.
// It checks one write row and two edit rows exist.
test("write and edit rows render with summaries", async ({ page }) => {
  await expect(page.locator('[data-tool="write"]')).toHaveCount(3);
  await expect(page.locator('[data-tool="edit"]')).toHaveCount(3);
  await expect(page.locator('[data-tool="write"]').first()).toContainText("/home/tester/project/app.conf");
});

// Verifies an expanded write row shows the created file.
// It checks content, chips, and that raw input/output are hidden.
test("write row expands to a rendered file view", async ({ page }) => {
  await page.locator('[data-tool="write"] button').first().click();
  const view = page.locator('[data-fileview="write"]');
  await expect(view).toBeVisible();
  await expect(view).toContainText("/home/tester/project/app.conf");
  await expect(view).toContainText("created");
  await expect(view).toContainText("[server]");
  await expect(view).toContainText("port = 8080");
  const expanded = page.locator('[data-tool="write"]').first();
  await expect(expanded).not.toContainText("INPUT");
  await expect(expanded).not.toContainText("OUTPUT");
});

// Verifies an expanded edit row shows only the diff view.
// It checks the diff renders and raw sections stay hidden.
test("edit row shows only the diff, no raw input/output", async ({ page }) => {
  await page.locator('[data-tool="edit"] button').first().click();
  const expanded = page.locator('[data-tool="edit"]').first();
  await expect(expanded.locator('[data-filediff="edit"]')).toBeVisible();
  await expect(expanded).not.toContainText("INPUT");
  await expect(expanded).not.toContainText("OUTPUT");
});

// Verifies the edit diff splits removals left and additions right.
// It checks each pane holds only its side with status colors.
test("edit row expands to a side-by-side diff", async ({ page }) => {
  await page.locator('[data-tool="edit"] button').first().click();
  const diff = page.locator('[data-filediff="edit"]').first();
  await expect(diff).toBeVisible();
  await expect(diff).toContainText("1 replacement");
  const left = diff.locator('[data-diffside="old"][data-diffline="del"]');
  const right = diff.locator('[data-diffside="new"][data-diffline="add"]');
  await expect(left).toContainText("port = 8080");
  await expect(right).toContainText("port = 9090");
  await expect(left).toHaveClass(/text-red-700/);
  await expect(right).toHaveClass(/text-emerald-700/);
  await expect(right).not.toContainText("port = 8080");
  await expect(left).not.toContainText("port = 9090");
  await expect(left.locator("span").first()).toHaveText("3");
  await expect(right.locator("span").first()).toHaveText("3");
});

// Verifies a failed edit lists each match as plain lines.
// It checks ordinals, context, and that above/below labels are gone.
// Verifies a failed write shows the engine error in the file view.
// It checks the exists message and hidden raw sections.
test("failed write row shows the exists error", async ({ page }) => {
  const failed = page.locator('[data-tool="write"]').nth(1);
  await expect(failed).toContainText("failed");
  await failed.locator("button").first().click();
  const view = failed.locator('[data-fileview="write"]');
  await expect(view).toBeVisible();
  await expect(view).toContainText("already exists");
  await expect(failed).not.toContainText("INPUT");
});

// Verifies an overwritten write shows the overwrite chip.
// It checks the chip and the replaced content.
test("overwritten write row reports the overwrite", async ({ page }) => {
  const row = page.locator('[data-tool="write"]').nth(2);
  await row.locator("button").first().click();
  const view = row.locator('[data-fileview="write"]');
  await expect(view).toBeVisible();
  await expect(view).toContainText("overwritten");
  await expect(view).toContainText("fresh");
});

// Verifies a multiline diff shows context, del, and add rows.
// It checks the replace-all chip and each row kind.
test("multiline edit diff keeps context lines", async ({ page }) => {
  const row = page.locator('[data-tool="edit"]').nth(2);
  await row.locator("button").first().click();
  const diff = row.locator('[data-filediff="edit"]');
  await expect(diff).toBeVisible();
  await expect(diff).toContainText("replace all");
  await expect(diff.locator('[data-diffline="context"]')).toContainText("[server]");
  await expect(diff.locator('[data-diffline="del"]')).toContainText('host = "localhost"');
  await expect(diff.locator('[data-diffline="add"]')).toContainText('host = "0.0.0.0"');
});

// Verifies expanding visible file rows keeps the scroll position.
// It compares scrollTop before and after expanding.
test("expanding a file row never scrolls the chat", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(page.locator("#messages")).toContainText("Config rewritten");
  const box = page.locator('[data-tool="write"]').first();
  await box.evaluate((el) => el.scrollIntoView({ block: "center" }));
  await page.waitForTimeout(150);
  const chat = page.locator("#chatView");
  const before = await chat.evaluate((el) => el.scrollTop);
  await box.locator("button").first().click({ force: true });
  await page.locator('[data-tool="edit"]').first().locator("button").first().click({ force: true });
  await page.waitForTimeout(200);
  const after = await chat.evaluate((el) => el.scrollTop);
  expect(Math.abs(after - before)).toBeLessThan(5);
});

// Verifies collapsing hides the rendered file view again.
// It toggles the row twice and checks visibility.
test("collapsing a file row hides its view", async ({ page }) => {
  const toggle = page.locator('[data-tool="write"] button').first();
  await toggle.click();
  await expect(page.locator('[data-fileview="write"]')).toBeVisible();
  await toggle.click();
  await expect(page.locator('[data-fileview="write"]')).toBeHidden();
});

// Verifies a failed edit lists each match as plain lines.
// It checks ordinals, context, and that above/below labels are gone.
test("failed edit row lists ambiguous matches", async ({ page }) => {
  const failed = page.locator('[data-tool="edit"]').nth(1);
  await expect(failed).toContainText("failed");
  await failed.locator("button").first().click();
  const diff = failed.locator('[data-filediff="edit"]');
  await expect(diff).toBeVisible();
  await expect(diff).toContainText("4 matches");
  await expect(diff).toContainText("1st match");
  await expect(diff).toContainText("2nd match");
  await expect(diff).toContainText("[server]");
  await expect(diff).toContainText('host = "localhost"');
  await expect(diff).not.toContainText("above:");
  await expect(diff).not.toContainText("below:");
});

// Captures the expanded file views for visual review.
// It expands all three rows and screenshots the page.
test("expanded file views screenshot", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.locator('[data-tool="write"] button').first().click();
  await page.locator('[data-tool="edit"] button').first().click();
  await page.locator('[data-tool="edit"]').nth(1).locator("button").first().click();
  await page.waitForTimeout(300);
  await page.locator("#chatView").evaluate((el) => el.scrollTo(0, 0));
  await page.waitForTimeout(200);
  await page.screenshot({ path: "/tmp/opencode/filetools-views.png" });
});
