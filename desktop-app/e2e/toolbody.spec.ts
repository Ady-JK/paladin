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
        w.__done = () => resolve({ sessionId: "", reply: "", stopped: false });
      });
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__h));
}

// open emits one call and expands its row, returning the body.
async function open(page: Page, t: Tool) {
  await page.evaluate((t) => (window as any).__h.onTool(t), t);
  const row = page.locator(`#messages .tool-row[data-tool="${t.name}"]`).last();
  const group = row.locator("xpath=ancestor::*[@data-tool-run]").locator(".tool-run-head");
  if (await group.count()) await group.click();
  await row.locator(".tool-row-head, .term-head").click();
  const body = row.locator(".tool-body");
  await expect(body).toBeVisible();
  return body;
}

// card_ is the terminal card that owns an opened shell body.
function card_(body: ReturnType<Page["locator"]>) {
  return body.locator("xpath=ancestor::*[contains(@class,'tool-row')][1]");
}

async function noRawJson(body: ReturnType<Page["locator"]>): Promise<void> {
  const text = await body.innerText();
  expect(text).not.toMatch(/^\s*(Input|Output)\s*$/m);
  expect(text).not.toContain('{"');
  expect(text).not.toContain('":');
}

test("shell shows a terminal: prompt, output and exit status", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { command: "ls -la src" }, "ok", { status: "done", exit_code: 0, output: "main.go\nutil.go\n" }),
  );
  const term = body.locator('[data-body="shell"]');
  const card = card_(body);
  await expect(card.locator(".term-cmd")).toHaveText("ls -la src");
  await expect(term.locator(".term-out")).toHaveText("main.go\nutil.go");
  await expect(card.locator(".term-status")).toHaveText("exit 0");
  await expect(card.locator(".term-status")).toHaveClass(/is-ok/);
  await noRawJson(body);
});

test("a failing command flags its exit code and truncation", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { cmd: "go test ./..." }, "ok", {
      status: "failed",
      exit_code: 2,
      output: "FAIL pkg",
      truncated: true,
    }),
  );
  await expect(card_(body).locator(".term-status")).toHaveText("exit 2");
  await expect(card_(body).locator(".term-status")).toHaveClass(/is-bad/);
  await expect(body.locator(".term-note")).toContainText("truncated");
});

test("a background job shows its id and log instead of output", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { cmd: "npm run dev", background: true }, "ok", {
      job_id: "job-7",
      status: "running",
      log: "/tmp/job-7.log",
    }),
  );
  await expect(card_(body).locator(".term-status")).toHaveText("running in background");
  await expect(body).toContainText("job-7");
  await expect(body).toContainText("/tmp/job-7.log");
  await noRawJson(body);
});

test("a shell status check reads as a job, not a command", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { action: "status", job_id: "job-7" }, "ok", {
      job_id: "job-7",
      status: "done",
      exit_code: 0,
      output: "ready",
    }),
  );
  await expect(card_(body).locator(".term-cmd")).toHaveText("status job-7");
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
      next_offset: 13,
      stop_reason: "line_limit",
    }),
  );
  const view = body.locator('[data-body="read"]');
  await expect(view.locator(".ln")).toHaveText(["10", "11", "12"]);
  await expect(view.locator(".file-head")).toContainText("src/a.go");
  await expect(view.locator(".file-head")).toContainText("lines 10–12");
  await expect(view.locator(".file-more")).toBeVisible();
  await expect(view.locator(".file-more")).toHaveAttribute("title", /line limit.*line 13/);
  await noRawJson(body);
});

test("read past the end says so instead of 'empty file'", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "read", { path: "src/a.go", offset: 99 }, "ok", {
      requested_path: "src/a.go",
      resolved_path: "/home/tester/project/src/a.go",
      scope: "inside",
      content: "",
      start_line: 0,
      end_line: 0,
      has_more: false,
      stop_reason: "past_end",
    }),
  );
  await expect(body.locator(".file-empty")).toHaveText("past the end of the file");
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
  const body = await open(
    page,
    tool("1", "read", { path: "nope.txt" }, "failed", { error: 'read: path "nope.txt" does not exist' }),
  );
  await expect(body.locator(".tool-err")).toHaveText('read: path "nope.txt" does not exist');
  await noRawJson(body);
});

test("other tools get a key/value view of arguments and result", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "lookup_docs", { url: "https://example.com", max: 3, deep: false }, "ok", {
      title: "Example",
      links: ["a", "b"],
    }),
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

test("web_fetch shows the URL, what came back and the page text", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "web_fetch", { url: "https://docs.example.com/guide" }, "ok", {
      url: "https://docs.example.com/guide",
      status: 200,
      title: "Guide",
      format: "markdown",
      content: "# Guide\nStep one.",
      total_chars: 17,
      truncated: true,
      next_index: 17,
      note: "untrusted",
    }),
  );
  const web = body.locator('[data-body="web"]');
  await expect(web.locator(".file-path")).toHaveText("https://docs.example.com/guide");
  await expect(web.locator(".file-chip")).toHaveText(["HTTP 200", "17 chars", "more"]);
  await expect(web.locator(".web-title")).toHaveText("Guide");
  await expect(web.locator(".file-line")).toHaveText(["# Guide", "Step one."]);
  await expect(page.locator('#messages .tool-row[data-tool="web_fetch"] .tool-detail')).toHaveText("docs.example.com");
});

test("plain-text results show as text", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "mystery", { q: "x" }, "ok", "just some text"));
  await expect(body.locator('[data-body="result"]')).toHaveText("just some text");
});

test("an open body follows the call from running to done", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "shell", { cmd: "make" }, "running", ""));
  await expect(card_(body).locator(".term-status")).toHaveText("running…");
  await page.evaluate(
    (t) => (window as any).__h.onTool(t),
    tool("1", "shell", { cmd: "make" }, "ok", { status: "done", exit_code: 0, output: "built" }),
  );
  await expect(body.locator(".term-out")).toHaveText("built");
  await expect(card_(body).locator(".term-status")).toHaveText("exit 0");
});

test("grep, glob and list read as searches with their results", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "grep", { pattern: "func New", glob: "*.go" }, "ok", {
      path: "/home/tester/project",
      scope: "inside",
      engine: "rg",
      matches: 3,
      files: 2,
      truncated: true,
      output: "/home/tester/project/a.go\n  4: func NewA() {}\n",
    }),
  );
  const row = page.locator('#messages .tool-row[data-tool="grep"]').last();
  await expect(row.locator(".tool-row-head")).toContainText("Searched");
  await expect(row.locator(".tool-row-head")).toContainText("func New");
  const view = body.locator('[data-body="search"]');
  await expect(view.locator(".file-path")).toHaveText("func New");
  await expect(view).toContainText("3 matches in 2 files");
  await expect(view).toContainText("more results");
  await expect(view.locator(".file-lines")).toContainText("4: func NewA() {}");
  await noRawJson(body);
});

test("glob names its pattern and list its folder", async ({ page }) => {
  await setup(page);
  const globBody = await open(page, tool("1", "glob", { pattern: "**/*.ts" }, "ok", { files: 1, output: "/p/a.ts\n" }));
  await expect(page.locator('#messages .tool-row[data-tool="glob"] .tool-row-head')).toContainText("Matched**/*.ts");
  await expect(globBody).toContainText("1 file");
  await expect(globBody.locator(".file-lines")).toContainText("/p/a.ts");
});

test("list reads as the workspace when no folder is given", async ({ page }) => {
  await setup(page);
  const body = await open(page, tool("1", "list", {}, "ok", { files: 0, output: "" }));
  await expect(page.locator('#messages .tool-row[data-tool="list"] .tool-row-head')).toContainText(
    "Listedthe workspace",
  );
  await expect(body).toContainText("no results");
});

test("a spilled command names the file holding its full output", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "shell", { cmd: "go test -v ./..." }, "ok", {
      status: "done",
      exit_code: 0,
      output: "=== RUN\n… [truncated] [full output saved to /s/tool-output/c1.txt]\nok",
      truncated: true,
      output_file: "/s/tool-output/c1.txt",
    }),
  );
  await expect(body.locator(".term-note")).toContainText("full output is saved");
  await expect(body.locator(".term-meta")).toContainText("/s/tool-output/c1.txt");
});

test("a read that brought folder instructions says so", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "read", { path: "/p/svc/a.go" }, "ok", {
      resolved_path: "/p/svc/a.go",
      scope: "inside",
      content: "package svc\n",
      start_line: 1,
      end_line: 1,
      nested_instructions: "Instructions for this part of the project\n--- /p/svc/AGENTS.md ---\nrules",
    }),
  );
  await expect(body.locator('[data-body="read"] .file-head')).toContainText("+ folder instructions");
});

test("an edit that left language server errors lists them", async ({ page }) => {
  await setup(page);
  const body = await open(
    page,
    tool("1", "edit", { path: "/home/tester/project/a.go", old_string: "x", new_string: "y" }, "ok", {
      replacements: 1,
      match_strategy: "exact",
      matches: [{ start_line: 3, end_line: 3 }],
      diagnostics: [
        { line: 3, column: 9, message: "undefined: y", source: "compiler" },
        { line: 7, column: 2, message: "missing return" },
      ],
      diagnostics_from: "gopls",
      errors: 2,
    }),
  );
  await expect(body).toContainText("2 errors · gopls");
  const rows = body.locator("[data-diagnostics] .diag-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("3:9");
  await expect(rows.first()).toContainText("undefined: y");
});
