// toolbody.spec.ts covers the expanded body of a tool call: each tool
// gets a purpose-built view (terminal, file lines, checklist, Q&A, key
// values) instead of raw "Input"/"Output" JSON dumps.
import { test, expect, type Page } from "@playwright/test";

type Tool = { id: string; name: string; arguments: string; result: string; status: string };

const tool = (id: string, name: string, args: unknown, status = "ok", result: unknown = {}): Tool => ({
  id,
  name,
  arguments: JSON.stringify(args),
  result: typeof result === "string" ? result : JSON.stringify(result),
  status,
});

async function setup(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__h = handlers;
        w.__done = () => resolve({ sessionId: "", reply: "", stopped: false, needsContinue: false });
      });
  });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__h));
}

// open emits one call and expands its row, returning the body.
async function open(page: Page, t: Tool) {
  await page.evaluate((t) => (window as any).__h.onTool(t), t);
  const row = page.locator(`#messages .tool-row[data-tool="${t.name}"]`).last();
  await row.locator(".tool-row-head").click();
  const body = row.locator(".tool-body");
  await expect(body).toBeVisible();
  return body;
}

async function noRawJson(body: ReturnType<Page["locator"]>): Promise<void> {
  const text = await body.innerText();
  expect(text).not.toMatch(/^\s*(Input|Output)\s*$/m);
  expect(text).not.toContain('{"');
  expect(text).not.toContain('":');
}

test("shell shows a terminal: prompt, output and exit status", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "shell", { cmd: "ls -la src" }, "ok", { status: "done", exit_code: 0, output: "main.go\nutil.go\n" }));
  const term = body.locator('[data-body="shell"]');
  await expect(term.locator(".term-cmd")).toHaveText("ls -la src");
  await expect(term.locator(".term-out")).toHaveText("main.go\nutil.go");
  await expect(term.locator(".term-status")).toHaveText("exit 0");
  await expect(term.locator(".term-status")).toHaveClass(/is-ok/);
  await noRawJson(body);
});

test("a failing command flags its exit code and truncation", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { cmd: "go test ./..." }, "ok", { status: "failed", exit_code: 2, output: "FAIL pkg", truncated: true }),
  );
  await expect(body.locator(".term-status")).toHaveText("exit 2");
  await expect(body.locator(".term-status")).toHaveClass(/is-bad/);
  await expect(body.locator(".term-note")).toContainText("truncated");
});

test("a background job shows its id and log instead of output", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { cmd: "npm run dev", background: true }, "ok", { job_id: "job-7", status: "running", log: "/tmp/job-7.log" }),
  );
  await expect(body.locator(".term-status")).toHaveText("running in background");
  await expect(body).toContainText("job-7");
  await expect(body).toContainText("/tmp/job-7.log");
  await noRawJson(body);
});

test("a shell status check reads as a job, not a command", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "shell", { action: "status", job_id: "job-7" }, "ok", { job_id: "job-7", status: "done", exit_code: 0, output: "ready" }));
  await expect(body.locator(".term-cmd")).toHaveText("status job-7");
  await expect(body.locator(".term-out")).toHaveText("ready");
});

test("read shows the lines with their real numbers", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "read", { path: "src/a.go", offset: 10 }, "ok", {
      requested_path: "src/a.go",
      resolved_path: "/home/tester/project/src/a.go",
      scope: "inside",
      content: "package a\n\nfunc A() {}\n",
      start_line: 10,
      end_line: 12,
      has_more: true,
      stop_reason: "line_limit",
    }),
  );
  const view = body.locator('[data-body="read"]');
  await expect(view.locator(".ln")).toHaveText(["10", "11", "12"]);
  await expect(view.locator(".file-head")).toContainText("src/a.go");
  await expect(view.locator(".file-head")).toContainText("lines 10–12");
  await expect(view.locator(".file-more")).toBeVisible();
  await noRawJson(body);
});

test("todo renders a checklist", async ({ page }) => {
  await setup(page);
  const items = [
    { content: "Write the spec", status: "finished" },
    { content: "Make it pass", status: "in_progress" },
    { content: "Ship", status: "not_started" },
  ];
  const body = await open(page, tool("1", "todo", { todos: items }, "ok", { todos: items }));
  const list = body.locator('[data-body="todo"] li');
  await expect(list).toHaveText(["Write the spec", "Make it pass", "Ship"]);
  await expect(list.nth(0)).toHaveAttribute("data-state", "finished");
  await expect(list.nth(1)).toHaveAttribute("data-state", "in_progress");
  await noRawJson(body);
});

test("ask renders questions with their answers", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "ask", { questions: [{ question: "Which DB?", options: ["pg", "sqlite"] }] }, "ok", {
      answers: [{ question: "Which DB?", answer: "pg" }],
    }),
  );
  const qa = body.locator('[data-body="ask"]');
  await expect(qa.locator(".qa-q")).toHaveText("Which DB?");
  await expect(qa.locator(".qa-a")).toHaveText("pg");
  await noRawJson(body);
});

test("a failed call shows its error message plainly", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "read", { path: "nope.txt" }, "failed", { error: 'read: path "nope.txt" does not exist' }));
  await expect(body.locator(".tool-err")).toHaveText('read: path "nope.txt" does not exist');
  await noRawJson(body);
});

test("other tools get a key/value view of arguments and result", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "web_fetch", { url: "https://example.com", max: 3, deep: false }, "ok", { title: "Example", links: ["a", "b"] }),
  );
  const args = body.locator('[data-body="args"]');
  await expect(args.locator(".kv-key")).toHaveText(["url", "max", "deep"]);
  await expect(args.locator(".kv-val").nth(0)).toHaveText("https://example.com");
  await expect(args.locator(".kv-val").nth(2)).toHaveText("false");
  const out = body.locator('[data-body="result"]');
  await expect(out.locator(".kv-key")).toHaveText(["title", "links"]);
  await expect(out.locator(".kv-val").nth(1)).toContainText("a");
  await noRawJson(body);
});

test("plain-text results show as text", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "mystery", { q: "x" }, "ok", "just some text"));
  await expect(body.locator('[data-body="result"]')).toHaveText("just some text");
});

test("an open body follows the call from running to done", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "shell", { cmd: "make" }, "running", ""));
  await expect(body.locator(".term-status")).toHaveText("running…");
  await page.evaluate(
    (t) => (window as any).__h.onTool(t),
    tool("1", "shell", { cmd: "make" }, "ok", { status: "done", exit_code: 0, output: "built" }),
  );
  await expect(body.locator(".term-out")).toHaveText("built");
  await expect(body.locator(".term-status")).toHaveText("exit 0");
});
