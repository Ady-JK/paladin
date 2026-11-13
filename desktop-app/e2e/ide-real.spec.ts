// Opt-in cross-process check: real agent-host file and terminal RPCs with the renderer.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";

test("IDE saves files and runs a terminal through the real sidecar", async ({ page }) => {
  const binary = process.env.PALADIN_IDE_REAL_HOST;
  test.skip(!binary, "Set PALADIN_IDE_REAL_HOST to a locally built agent-host binary");
  const root = await mkdtemp(path.join(tmpdir(), "paladin-ide-e2e-"));
  const file = path.join(root, "example.go");
  await writeFile(file, "package main\n");
  const problemFile = path.join(root, "problems.fake");
  await writeFile(problemFile, "clean disk\n");
  const fakeServer = path.join(root, "fake-lsp.cjs");
  await writeFile(
    fakeServer,
    `
let buf = Buffer.alloc(0);
function send(message) { const body = Buffer.from(JSON.stringify(message)); process.stdout.write('Content-Length: ' + body.length + '\\r\\n\\r\\n'); process.stdout.write(body); }
process.stdin.on('data', chunk => {
  buf = Buffer.concat([buf, chunk]);
  while (true) {
    const end = buf.indexOf('\\r\\n\\r\\n'); if (end < 0) return;
    const match = /Content-Length:\\s*(\\d+)/i.exec(buf.subarray(0, end).toString()); if (!match) process.exit(1);
    const size = Number(match[1]); if (buf.length < end + 4 + size) return;
    const msg = JSON.parse(buf.subarray(end + 4, end + 4 + size)); buf = buf.subarray(end + 4 + size);
    if (msg.method === 'initialize') send({jsonrpc:'2.0',id:msg.id,result:{capabilities:{}}});
    if (msg.method === 'shutdown') send({jsonrpc:'2.0',id:msg.id,result:null});
    if (msg.method === 'exit') process.exit(0);
    if (msg.method === 'textDocument/didOpen' || msg.method === 'textDocument/didChange') {
      const doc = msg.params.textDocument; const text = doc.text ?? msg.params.contentChanges[0].text;
      const lines = text.split('\\n'); const line = lines.findIndex(value => value.includes('ERROR'));
      const diagnostics = line < 0 ? [] : [{severity:1,message:'Draft error',range:{start:{line,character:0},end:{line,character:1}}}];
      send({jsonrpc:'2.0',method:'textDocument/publishDiagnostics',params:{uri:doc.uri,version:doc.version,diagnostics}});
    }
  }
});
`,
  );
  await mkdir(path.join(root, "data/config"), { recursive: true });
  await writeFile(
    path.join(root, "data/config/lsp.json"),
    JSON.stringify({
      timeoutMs: 2000,
      servers: { fake: { command: [process.execPath, fakeServer], extensions: [".fake"], languageId: "fake" } },
    }),
  );
  await mkdir(path.join(root, ".hidden"));
  await mkdir(path.join(root, "ignored"));
  await writeFile(path.join(root, ".gitignore"), "ignored/\n*.log\n");
  await writeFile(path.join(root, "debug.log"), "debug\n");
  const child = spawn(binary!, [], {
    env: { ...process.env, SHELL: "/bin/sh", PALADIN_DATA_DIR: path.join(root, "data"), PALADIN_KEYSTORE: "memory" },
    stdio: ["pipe", "pipe", "ignore"],
  });
  const lines = createInterface({ input: child.stdout });
  let nextId = 0;
  const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
  lines.on("line", (line) => {
    const message = JSON.parse(line) as { id?: string; event?: string; ok?: boolean; result?: unknown; error?: string };
    if (message.event?.startsWith("terminal/")) {
      void page.evaluate((event) => (window as any).__e2eHub.emit(event), message).catch(() => undefined);
      return;
    }
    if (!message.id) return;
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    if (message.ok) request.resolve(message.result);
    else request.reject(new Error(message.error ?? "Sidecar request failed"));
  });
  await page.exposeFunction("__ideReal", (method: string, params: unknown) => {
    const id = String(++nextId);
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
    });
  });
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((workspace) => {
    window.addEventListener("DOMContentLoaded", () => {
      const w = window as any;
      const fallback = w.__paladinBaseInvoke;
      w.__paladinBaseInvoke = (method: string, params: unknown) => {
        if (method === "getWorkspace") return Promise.resolve(workspace);
        if (
          [
            "fsList",
            "ideList",
            "ideSearch",
            "ideDiagnose",
            "ideLayoutGet",
            "ideLayoutSet",
            "ideExplorerSettingsGet",
            "ideExplorerSettingsSet",
            "listSessions",
            "ideReadFile",
            "ideFileStatuses",
            "ideSaveFile",
            "terminalRun",
            "terminalStart",
            "terminalWrite",
            "terminalResize",
            "terminalClose",
          ].includes(method)
        )
          return w.__ideReal(method, params);
        return fallback(method, params);
      };
    });
  }, root);
  try {
    await page.goto("/");
    await expect(page.locator("#workspaceLabel")).not.toHaveText("workspace");
    await page.locator("#ideExplorerToggle").click();
    await expect(page.locator("#ideModeBtn")).toHaveAttribute("aria-pressed", "false");
    await expect(page.getByRole("button", { name: "example.go", exact: true })).toBeVisible();
    await page.locator("#ideModeBtn").click();
    await expect(page.locator("#ideSessionList")).toContainText("No sessions in this workspace");
    await expect(page.getByRole("button", { name: ".hidden", exact: true })).toBeVisible();
    await page.getByLabel("Hide dot folders").check();
    await expect(page.getByRole("button", { name: ".hidden", exact: true })).toHaveCount(0);
    await page.getByLabel("Hide Git-ignored items").check();
    await expect(page.getByRole("button", { name: "ignored", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "debug.log", exact: true })).toHaveCount(0);
    await page.getByRole("button", { name: "problems.fake", exact: true }).click();
    await page.locator("#ideEditor .cm-content").fill("first\nERROR draft\n");
    await page.getByRole("button", { name: "Show problems" }).click();
    await expect(page.locator("#ideProblemsPanel")).toContainText("Draft error");
    expect(await readFile(problemFile, "utf8")).toBe("clean disk\n");
    await page.getByRole("button", { name: "Hide problems" }).click();
    await page.locator("#ideReloadBtn").click();
    await page.locator("#ideReloadBtn").click();
    await expect(page.locator("#ideEditor .cm-content")).toHaveText("clean disk");
    expect(JSON.parse(await readFile(path.join(root, ".config/paladin/explorer.json"), "utf8"))).toEqual({
      hideIgnored: true,
      hideDotFolders: true,
    });
    await page.reload();
    await page.locator("#ideModeBtn").click();
    await expect(page.getByLabel("Hide dot folders")).toBeChecked();
    await expect(page.getByLabel("Hide Git-ignored items")).toBeChecked();
    await expect(page.getByRole("button", { name: "debug.log", exact: true })).toHaveCount(0);
    await page.keyboard.press(process.platform === "darwin" ? "Meta+Shift+f" : "Control+Shift+f");
    const workspaceSearch = page.getByRole("dialog", { name: "Search workspace" });
    await workspaceSearch.getByRole("textbox").fill("package main");
    await workspaceSearch.getByRole("button", { name: "Search", exact: true }).click();
    await workspaceSearch.getByRole("button", { name: /example.go:1/ }).click();
    await expect(page.locator("#ideEditor .cm-content")).toContainText("package main");
    await page.locator("#ideExplorerToggle").click();
    await page.locator("#ideTree").getByRole("button", { name: "problems.fake", exact: true }).click();
    await page.locator("#ideFileTabs .ide-tab-select").filter({ hasText: "example.go" }).click();
    await writeFile(problemFile, "external background edit\n");
    const backgroundTab = page.locator("#ideFileTabs .ide-tab-select").filter({ hasText: "problems.fake" });
    await expect(backgroundTab.locator('[aria-label="Changed on disk"]')).toBeVisible();
    await backgroundTab.click();
    await expect(page.getByText("This file changed on disk.")).toBeVisible();
    await page.locator("#ideFileTabs .ide-tab-select").filter({ hasText: "example.go" }).click();
    const editor = page.locator("#ideEditor .cm-content");
    await expect(editor).toHaveText("package main");
    await editor.fill("package changed\n");
    await page.locator("#ideSaveBtn").click();
    await expect(page.locator("#ideStatus")).toContainText("Saved");
    expect(await readFile(file, "utf8")).toBe("package changed\n");
    await editor.fill("my draft\n");
    await writeFile(file, "external change\n");
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await expect(page.getByText("This file changed on disk.")).toBeVisible();
    await page.getByRole("button", { name: "Compare changes" }).click();
    await expect(page.locator("#ideDiskCompare")).toContainText("external change");
    await expect(page.locator("#ideDiskCompare")).toContainText("my draft");
    await page.getByRole("button", { name: "Close comparison" }).click();
    await page.locator("#ideSaveBtn").click();
    await expect(page.locator("#ideStatus")).toContainText("changed on disk");
    await expect(editor).toHaveText("my draft");
    expect(await readFile(file, "utf8")).toBe("external change\n");
    await page.locator("#ideTerminalToggle").click();
    await expect(page.locator("#ideTerminalDock .xterm")).toBeVisible();
    await page.locator("#ideTerminalDock .xterm-helper-textarea").focus();
    await page.keyboard.type("printf 'real-%s-ok\\n' shell");
    await page.keyboard.press("Enter");
    await expect(page.locator("#ideTerminalDock .xterm-rows")).toContainText("real-shell-ok");
    await page.locator("#promptInput").fill("!! printf standalone-command-ok");
    await page.locator("#promptInput").press("Enter");
    await expect(page.locator("#ideTerminalDock .xterm-rows")).toContainText("standalone-command-ok");
    await expect(page.locator("#ideSessionList")).toBeVisible();
    expect(await page.evaluate(() => (window as any).__e2eOpens?.length ?? 0)).toBe(0);
    await expect
      .poll(async () =>
        page.evaluate(async (cwd) => {
          const layout = await (window as any).__ideReal("ideLayoutGet", { cwd });
          return layout?.files?.find((item: any) => item.path.endsWith("example.go"))?.draft?.content;
        }, root),
      )
      .toBe("my draft\n");
    page.once("dialog", (dialog) => void dialog.accept());
    await page.reload();
    await page.locator("#ideModeBtn").click();
    await expect(page.locator("#ideEditor .cm-content")).toHaveText("my draft");
    expect(await readFile(file, "utf8")).toBe("external change\n");
  } finally {
    child.stdin.end();
    await new Promise<void>((resolve) => {
      if (child.exitCode !== null) resolve();
      else child.once("exit", () => resolve());
    });
    lines.close();
    await rm(root, { recursive: true, force: true });
  }
});
