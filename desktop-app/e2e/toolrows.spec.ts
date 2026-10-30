// toolrows.spec.ts covers the compact tool-call rows: a verb + detail
// line per call ("Ran `pytest -q`", "Read src/conf.py"), consecutive
// calls folded into one summarized group ("Ran 2 commands"), live
// running/failed states, and the same layout for persisted history.
import { test, expect, type Page } from "@playwright/test";

type Tool = { id: string; name: string; arguments: string; result: string; status: string };

const tool = (id: string, name: string, args: unknown, status = "ok", result: unknown = { ok: true }): Tool => ({
  id,
  name,
  arguments: JSON.stringify(args),
  result: JSON.stringify(result),
  status,
});

// Every turn is held open; the spec drives its events through __h.
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
  await expect(page.locator("#sessionList")).toBeVisible();
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__h));
}

async function emit(page: Page, steps: Array<string | Tool>): Promise<void> {
  await page.evaluate((list) => {
    const h = (window as any).__h;
    for (const s of list) {
      if (typeof s === "string") h.onDelta(s);
      else h.onTool(s);
    }
  }, steps);
}

const runs = (page: Page) => page.locator("#messages [data-tool-run]");

test("consecutive tool calls fold into one summarized group", async ({ page }) => {
  await setup(page);
  await emit(page, [
    "Looking around:",
    tool("1", "shell", { cmd: "ls src" }),
    tool("2", "shell", { cmd: "sed -n 1,40p src/types.ts" }),
    "Now the config.",
    tool("3", "read", { path: "src/conf.py" }),
    tool("4", "edit", { path: "src/conf.py", old_string: "a", new_string: "b" }),
  ]);
  await expect(runs(page)).toHaveCount(2);
  const first = runs(page).nth(0);
  await expect(first.locator(".tool-run-head")).toHaveText(/Ran 2 commands/);
  await expect(first.locator(".tool-row").first()).toBeHidden();
  await first.locator(".tool-run-head").click();
  await expect(first.locator(".tool-row")).toHaveCount(2);
  await expect(first.locator(".tool-row").nth(0)).toBeVisible();
  await expect(first.locator(".tool-row").nth(0).locator(".tool-chip")).toHaveText("ls src");
  await expect(runs(page).nth(1).locator(".tool-run-head")).toHaveText(/Read 1 file, edited 1 file/);
});

test("text streamed right before a tool call is kept", async ({ page }) => {
  await setup(page);
  await emit(page, ["Looking around:", tool("1", "shell", { cmd: "ls" }), "Now the config.", tool("2", "read", { path: "a.txt" })]);
  await expect(page.locator("#messages")).toContainText("Looking around:");
  await expect(page.locator("#messages")).toContainText("Now the config.");
  await expect(page.locator("#messages")).not.toContainText("thinking");
});

test("a lone call is one row: verb, detail and a command chip", async ({ page }) => {
  await setup(page);
  await emit(page, [tool("1", "shell", { cmd: "pytest -q" }), "Tests pass.", tool("2", "read", { path: "src/conf.py" })]);
  await expect(runs(page)).toHaveCount(2);
  await expect(page.locator(".tool-run-head")).toHaveCount(0);
  const ran = runs(page).nth(0).locator(".tool-row");
  await expect(ran.locator(".tool-verb")).toHaveText("Ran");
  await expect(ran.locator(".tool-chip")).toHaveText("pytest -q");
  const read = runs(page).nth(1).locator(".tool-row");
  await expect(read.locator(".tool-verb")).toHaveText("Read");
  await expect(read.locator(".tool-detail")).toHaveText("src/conf.py");
});

test("running calls read in the present tense and settle in place", async ({ page }) => {
  await setup(page);
  await emit(page, [tool("1", "shell", { cmd: "make" }, "running", null)]);
  const row = page.locator('.tool-row[data-tool="shell"]');
  await expect(row).toHaveAttribute("data-status", "running");
  await expect(row.locator(".tool-verb")).toHaveText("Running");
  await emit(page, [tool("2", "shell", { cmd: "make test" }, "running", null)]);
  const head = runs(page).locator(".tool-run-head");
  await expect(head).toHaveText(/Running 2 commands/);
  await expect(head).toContainText("make test");
  await emit(page, [tool("1", "shell", { cmd: "make" }), tool("2", "shell", { cmd: "make test" })]);
  await expect(head).toHaveText(/^Ran 2 commands$/);
  await expect(page.locator('.tool-row[data-status="running"]')).toHaveCount(0);
});

test("failed calls are flagged on the row and counted on the group", async ({ page }) => {
  await setup(page);
  await emit(page, [
    tool("1", "write", { path: "a.txt", content: "x" }, "failed", { error: "exists" }),
    tool("2", "edit", { path: "b.txt", old_string: "a", new_string: "b" }),
  ]);
  const head = runs(page).locator(".tool-run-head");
  await expect(head).toHaveText(/Wrote 1 file, edited 1 file/);
  await expect(head.locator(".tool-fail")).toHaveText("1 failed");
  await head.click();
  const failed = page.locator('.tool-row[data-tool="write"]');
  await expect(failed).toHaveAttribute("data-status", "failed");
  await expect(failed.locator(".tool-fail")).toHaveText("failed");
});

test("a row expands to its input and output", async ({ page }) => {
  await setup(page);
  await emit(page, [tool("1", "shell", { cmd: "echo hi" }, "ok", { output: "hi-from-shell" })]);
  const row = page.locator('.tool-row[data-tool="shell"]');
  await expect(row).not.toContainText("hi-from-shell");
  await row.locator(".tool-row-head").click();
  await expect(row.locator(".tool-row-head")).toHaveAttribute("aria-expanded", "true");
  await expect(row).toContainText("hi-from-shell");
});

test("history transcripts group calls the same way", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const at = Math.floor(Date.now() / 1000);
    const t = (name: string, args: unknown) => ({
      role: "tool",
      content: "{}",
      createdAt: at,
      name,
      arguments: JSON.stringify(args),
      status: "ok",
    });
    const history = [
      { role: "user", content: "look", createdAt: at },
      { role: "assistant", content: "Checking.", createdAt: at },
      t("read", { path: "a.go" }),
      t("read", { path: "b.go" }),
      t("read", { path: "c.go" }),
      { role: "assistant", content: "Running it.", createdAt: at },
      t("shell", { cmd: "go test ./..." }),
      { role: "assistant", content: "All good.", createdAt: at },
    ];
    const p = (window as any).paladin;
    const prev = p.invoke.bind(p);
    p.invoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        return [{ sessionId: "sess-hist", cwd: "/home/tester/project", createdAt: at, provider: "p", model: "m", messageCount: 8, title: "hist" }];
      }
      if (cmd === "sessionHistory") return history;
      return prev(cmd, args);
    };
  });
  await page.goto("/");
  await page.click('[data-session="sess-hist"]');
  await expect(page.locator("#messages")).toContainText("All good.");
  await expect(runs(page)).toHaveCount(2);
  await expect(runs(page).nth(0).locator(".tool-run-head")).toHaveText(/Read 3 files/);
  await expect(runs(page).nth(1).locator(".tool-chip")).toHaveText("go test ./...");
});

test("parallel tool updates keep nodes stable and the group animates shut", async ({ page }) => {
  await setup(page);
  await emit(page, [
    tool("p1", "shell", { cmd: "make a" }, "running", ""),
    tool("p2", "shell", { cmd: "make b" }, "running", ""),
  ]);
  const run = runs(page).first();
  await expect(run.locator(".tool-run-head")).toHaveText(/Running|Ran/);
  // Tag the live nodes, push more updates, and check nothing was rebuilt
  // (a rebuilt spinner restarts its rotation and the head flickers).
  await page.evaluate(() => {
    const run = document.querySelector("#messages [data-tool-run]")!;
    (run.querySelector(".tool-run-head .tool-spin") as any).__tag = "head";
    (run.querySelector(".tool-run-head .tool-verb") as any).__tag = "verb";
    (run.querySelector('.tool-row[data-tool="shell"] .tool-spin') as any).__tag = "row";
  });
  await emit(page, [
    tool("p3", "read", { path: "src/a.ts" }, "running", ""),
    tool("p2", "shell", { cmd: "make b" }, "ok", "ok"),
  ]);
  await expect(run.locator(".tool-run-head")).toContainText(/reading 1 file/);
  const tags = await page.evaluate(() => {
    const run = document.querySelector("#messages [data-tool-run]")!;
    return {
      head: (run.querySelector(".tool-run-head .tool-spin") as any)?.__tag,
      verb: (run.querySelector(".tool-run-head .tool-verb") as any)?.__tag,
      row: (run.querySelector('.tool-row[data-tool="shell"] .tool-spin') as any)?.__tag,
    };
  });
  expect(tags).toEqual({ head: "head", verb: "verb", row: "row" });
  // The folded list collapses with a transition rather than display:none.
  await run.locator(".tool-run-head").click();
  await expect(run.locator(".tool-row").first()).toBeVisible();
  await run.locator(".tool-run-head").click();
  await expect(run.locator(".tool-row").first()).toBeHidden();
  const css = await run.locator(".tool-run-list").evaluate((el) => getComputedStyle(el).display);
  expect(css).toBe("grid");
});
