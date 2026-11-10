import { _electron as electron, expect, test } from "@playwright/test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// Exercises the actual Electron scheme, sandboxed subframe and Go host.
// `make test` builds both the sidecar and the Electron preload first.
test("real plugin panels and overlays use the restricted bridge", async ({ browserName }, testInfo) => {
  void browserName;
  test.skip(
    process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY,
    "Electron needs a display",
  );
  test.setTimeout(60_000);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-plugin-electron-"));
  const data = path.join(root, "data");
  const workspace = path.join(root, "workspace");
  const plugin = path.join(data, "config", "extensions", "probe");
  fs.mkdirSync(path.join(plugin, "ui"), { recursive: true });
  fs.mkdirSync(workspace, { recursive: true });
  fs.writeFileSync(path.join(workspace, "sample.txt"), "local sample");
  fs.writeFileSync(path.join(root, "outside"), "private");
  fs.writeFileSync(path.join(data, "config", "extensions.json"), JSON.stringify({ enabled: ["probe"] }));
  fs.writeFileSync(path.join(plugin, "package.json"), JSON.stringify({ type: "module" }));
  fs.writeFileSync(
    path.join(plugin, "paladin-plugin.json"),
    JSON.stringify({
      entry: "index.js",
      views: [
        {
          id: "panel",
          title: "Probe panel",
          entry: "ui/index.html",
          mount: "side-pane",
          permissions: ["readFile", "invokeTool"],
        },
        {
          id: "canvas",
          title: "Probe canvas",
          entry: "ui/index.html",
          mount: "overlay",
          permissions: ["readFile", "invokeTool"],
        },
      ],
    }),
  );
  fs.writeFileSync(
    path.join(plugin, "index.js"),
    `export default {tools:[{name:'echo',description:'Echo input',parameters:{type:'object'},execute(a){return {value:a.value}}}]};`,
  );
  fs.writeFileSync(
    path.join(plugin, "ui", "index.html"),
    `<!doctype html><button id="read">Read</button><button id="run">Run</button><output id="status">Loading</output><script type="module" src="./index.js"></script>`,
  );
  fs.writeFileSync(
    path.join(plugin, "ui", "index.js"),
    `const p=window.paladin.plugins;const status=document.querySelector('#status');await p.getContext();status.textContent='Ready';document.querySelector('#read').onclick=async()=>{try{const r=await p.readFile('sample.txt');status.textContent=r.ok?r.text:r.error}catch(error){status.textContent=String(error)}};document.querySelector('#run').onclick=async()=>{try{const r=await p.invokeTool('extension__probe__echo',{value:'worked'});status.textContent=r.ok?r.result:r.error}catch(error){status.textContent=String(error)}};`,
  );
  let app: Awaited<ReturnType<typeof electron.launch>> | undefined;
  try {
    app = await electron.launch({
      args: [".", `--user-data-dir=${path.join(root, "profile")}`, "--disable-gpu"],
      cwd: path.resolve(__dirname, ".."),
      env: {
        ...process.env,
        PALADIN_DATA_DIR: data,
        PALADIN_CREATOR_DIR: path.join(root, "creator"),
        PALADIN_KEYSTORE: "memory",
        LITELLM_MASTER_KEY: "",
        ELECTRON_START_URL: "http://localhost:5173",
      },
    });
    const page = await app.firstWindow();
    await page.waitForSelector("#promptInput");
    await page.waitForTimeout(750);
    const sessionId = await page.evaluate(async (cwd) => {
      const api = (window as any).paladin;
      await api.invoke("addProvider", {
        name: "localtest",
        apiUrl: "http://localhost:1/v1",
        apiType: "openai",
        apiKey: "local-test-key",
      });
      await api.invoke("setDefaultProvider", { name: "localtest" });
      await api.invoke("setWorkspace", { path: cwd });
      const session = await api.invoke("openSession", { cwd });
      return session.sessionId as string;
    }, workspace);
    await page.reload();
    await expect(page.locator("#promptInput")).toBeVisible();
    const creatorSkills = async (cwd: string): Promise<string[]> =>
      page.evaluate(async (folder) => {
        const list = await (window as any).paladin.invoke("listSkills", { cwd: folder });
        return list.map((skill: { name: string }) => skill.name);
      }, cwd);
    expect(await creatorSkills(workspace)).not.toContain("writing-an-extension");
    await page.waitForTimeout(400);
    if (await page.locator("#sidebar.is-closed").count()) await page.click("#agentTab");
    await page.locator(`button.session-pick[data-session="${sessionId}"]`).first().click();
    await page.click("#panePluginsBtn");
    await page.locator('.plugin-row[data-plugin="probe"][data-view="panel"]').click();
    const panel = page.frameLocator("iframe.plugin-frame");
    await expect(panel.locator("#status")).toHaveText("Ready");
    expect(await panel.locator("body").evaluate(() => typeof (window as any).paladin.invoke)).toBe("undefined");
    await panel.locator("#read").dispatchEvent("click");
    await expect(panel.locator("#status")).toHaveText("local sample");
    const denied = await panel
      .locator("body")
      .evaluate(async () => (window as any).paladin.plugins.readFile("../outside"));
    expect(denied.ok).toBe(false);
    expect(denied.error).toContain("outside the workspace");
    const cross = await panel
      .locator("body")
      .evaluate(async () => (window as any).paladin.plugins.invokeTool("extension__other__echo", {}));
    expect(cross.ok).toBe(false);
    const store = await panel.locator("body").evaluate(async () => {
      const api = (window as any).paladin.plugins;
      await api.writeStore("selection", { value: 3 });
      return api.readStore("selection");
    });
    expect(store).toEqual({ value: 3 });
    await panel.locator("#run").dispatchEvent("click");
    await page.locator('.plugin-approval-layer [data-approve="once"]').click();
    await expect(panel.locator("#status")).toContainText('"value":"worked"');
    await page.click("#panePluginsBtn");
    await page.locator('.plugin-row[data-plugin="probe"][data-view="canvas"]').click();
    const overlay = page.locator(".plugin-overlay");
    const canvas = overlay.frameLocator("iframe");
    await expect(canvas.locator("#status")).toHaveText("Ready");
    await canvas.locator("#read").dispatchEvent("click");
    await expect(canvas.locator("#status")).toHaveText("local sample");
    const initial = await overlay.boundingBox();
    expect(initial).not.toBeNull();
    const header = overlay.locator(".plugin-overlay-header");
    const handle = await header.boundingBox();
    expect(handle).not.toBeNull();
    await page.mouse.move(handle!.x + 50, handle!.y + 10);
    await page.mouse.down();
    await page.mouse.move(handle!.x - 50, handle!.y + 60, { steps: 5 });
    await page.mouse.up();
    expect((await overlay.boundingBox())!.x).toBeLessThan(initial!.x);
    await canvas.locator("#run").dispatchEvent("click");
    const approval = page.locator('.plugin-approval-layer [data-approve="once"]');
    await expect(approval).toBeVisible();
    await expect(overlay).toBeHidden();
    await approval.click();
    await expect(overlay).toBeVisible();
    await expect(canvas.locator("#status")).toContainText('"value":"worked"');
    await overlay.getByRole("button", { name: "Close Probe canvas" }).click();
    await expect(overlay).toHaveCount(0);
    await page.click("#panePluginsBtn");
    await page.locator('.plugin-row[data-plugin="probe"][data-view="canvas"]').click();
    await expect(overlay).toBeVisible();
    await page.waitForTimeout(300);
    if (await page.locator("#sidebar.is-closed").count()) await page.click("#agentTab");
    await page.click("#newSessionBtn");
    await page.click("#sessionStartBtn");
    await expect(overlay).toHaveCount(0);

    await page.locator("#creatorModeBtn").click();
    await expect(page.locator("#creatorModeBtn")).toHaveAttribute("aria-pressed", "true");
    await expect(page.locator("#workspaceLabel")).toHaveText("creator");
    const creatorRoot = path.join(root, "creator");
    expect(await creatorSkills(creatorRoot)).toEqual(
      expect.arrayContaining(["writing-an-extension", "writing-a-plugin-ui", "creating-paladin-ui"]),
    );
    expect(await creatorSkills(workspace)).not.toContain("writing-an-extension");
    const creatorPlugin = path.join(root, "creator", "ui", "live");
    const creatorCore = path.join(root, "creator", "core", "live");
    fs.mkdirSync(path.join(creatorPlugin, "ui"), { recursive: true });
    fs.mkdirSync(creatorCore, { recursive: true });
    fs.writeFileSync(path.join(creatorCore, "package.json"), JSON.stringify({ type: "module" }));
    fs.writeFileSync(
      path.join(creatorCore, "index.js"),
      `export default {tools:[{name:'echo',description:'Echo',parameters:{type:'object'},execute(){return {value:'one'}}}]};`,
    );
    fs.writeFileSync(
      path.join(creatorCore, "paladin-plugin.json"),
      JSON.stringify({
        entry: "index.js",
        tools: [{ name: "echo", description: "Echo", parameters: { type: "object" } }],
      }),
    );
    const moduleHtml = (status: string): string =>
      `<!doctype html><style>body{margin:0;padding:18px;background:#16161b;color:#dddde9;font:13px system-ui}h1{margin:0 0 8px;font-size:15px}p{color:#9c9cae;font-size:12px}button{margin-top:12px;padding:8px 12px;border:0;border-radius:7px;background:#a6baff;color:#161625;font-weight:600}output{display:block;margin-top:14px;color:#c8d1ff;font-family:monospace}</style><h1>Creator demo</h1><p>Live extension demo</p><button id="run">Run tool</button><output id="status">${status}</output><script src="./index.js" type="module"></script>`;
    fs.writeFileSync(path.join(creatorPlugin, "ui", "index.html"), moduleHtml("Ready"));
    fs.writeFileSync(
      path.join(creatorPlugin, "ui", "index.js"),
      `document.querySelector('#run').onclick=async()=>{const result=await window.paladin.plugins.invokeTool('extension__creator_core_live__echo',{});document.querySelector('#status').textContent=result.ok?result.result:result.error};`,
    );
    fs.writeFileSync(
      path.join(creatorPlugin, "paladin-plugin.json"),
      JSON.stringify({
        tools: [],
        views: [
          {
            id: "panel",
            title: "Live panel",
            entry: "ui/index.html",
            mount: "side-pane",
            permissions: ["invokeToolDestructive", "sendPrompt"],
          },
          {
            id: "canvas",
            title: "Live canvas",
            entry: "ui/index.html",
            mount: "overlay",
            permissions: ["invokeToolDestructive", "sendPrompt"],
          },
        ],
      }),
    );
    await page.waitForTimeout(800);
    await page.click("#panePluginsBtn");
    await expect(page.locator('.plugin-row[data-plugin="creator_ui_live"][data-view="panel"]')).toBeVisible({
      timeout: 15000,
    });
    await page.locator('.plugin-row[data-plugin="creator_ui_live"][data-view="panel"]').click();
    const creatorPanel = page.frameLocator("iframe.plugin-frame");
    await expect(creatorPanel.locator("#status")).toHaveText("Ready", { timeout: 15000 });
    await creatorPanel.locator("#run").dispatchEvent("click");
    const pendingApproval = page.locator('.plugin-approval-layer [data-approve="once"]');
    await expect(pendingApproval).toBeVisible();
    fs.writeFileSync(path.join(creatorPlugin, "ui", "index.html"), moduleHtml("Updated after call"));
    await page.waitForTimeout(800);
    await pendingApproval.click();
    await expect(creatorPanel.locator("#status")).toContainText('"value":"one"');
    const runner = await page.evaluate(
      async (cwd) => {
        const overview = await (window as any).paladin.invoke("toolsList", { cwd, sessionId: "" });
        return overview.runners.find((item: { plugin: string }) => item.plugin === "creator_core_live");
      },
      path.join(root, "creator"),
    );
    expect(runner?.electron).toBe(true);
    await expect(page.locator(".plugin-approval-layer")).toHaveCount(0);
    await expect(creatorPanel.locator("#status")).toHaveText("Updated after call", { timeout: 15000 });
    const previousFrameURL = await page.locator("iframe.plugin-frame").getAttribute("src");
    fs.writeFileSync(
      path.join(creatorCore, "index.js"),
      `export default {tools:[{name:'echo',description:'Echo',parameters:{type:'object'},execute(){return {value:'two'}}}]};`,
    );
    await expect
      .poll(() => page.locator("iframe.plugin-frame").getAttribute("src"), { timeout: 15000 })
      .not.toBe(previousFrameURL);
    await expect(creatorPanel.locator("#status")).toHaveText("Updated after call");
    await creatorPanel.locator("#run").dispatchEvent("click");
    await page.locator('.plugin-approval-layer [data-approve="once"]').click();
    await expect(creatorPanel.locator("#status")).toContainText('"value":"two"');
    await expect(page.locator(".plugin-approval-layer")).toHaveCount(0);
    fs.writeFileSync(path.join(creatorPlugin, "ui", "index.html"), moduleHtml("UI updated"));
    await expect(creatorPanel.locator("#status")).toHaveText("UI updated", { timeout: 15000 });
    await page.locator("#promptInput").fill("saved draft");
    await page.click("#panePluginsBtn");
    await page.locator('.plugin-row[data-plugin="creator_ui_live"][data-view="canvas"]').click();
    const creatorCanvas = page.locator(".plugin-overlay");
    const canvasFrame = creatorCanvas.frameLocator("iframe");
    await expect(canvasFrame.locator("#status")).toHaveText("UI updated");
    await creatorCanvas.getByRole("button", { name: "Expand Live canvas canvas" }).click();
    await expect(creatorCanvas).toHaveClass(/is-maximized/);
    await page.screenshot({ path: testInfo.outputPath("creator-mode.png") });
    const sent = await canvasFrame
      .locator("body")
      .evaluate(async () => (window as any).paladin.plugins.sendPrompt("Message from Creator UI"));
    expect(sent.ok).toBe(true);
    await expect(page.locator("#messages")).toContainText("Message from Creator UI");
    await expect(page.locator("#promptInput")).toHaveValue("saved draft");
    await expect(creatorCanvas).toHaveCount(0);
    await page.locator("#creatorModeBtn").click();
    await expect(page.locator("#creatorModeBtn")).toHaveAttribute("aria-pressed", "false");
    expect(await creatorSkills(creatorRoot)).not.toContain("writing-an-extension");
  } finally {
    await app?.close();
    fs.rmSync(root, { recursive: true, force: true });
  }
});
