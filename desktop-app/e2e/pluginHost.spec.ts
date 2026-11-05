// pluginHost.spec.ts covers the main-process half of plugin UI: which
// files a `plugin://` frame may load and where the data directory is.
import { expect, test } from "@playwright/test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { PluginHost, paladinDataDir } from "../electron/pluginHost";

function pluginDir(files: Record<string, string>, name = "board"): { data: string; dir: string } {
  const data = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-plugin-"));
  const dir = path.join(data, "config", "extensions", name);
  fs.mkdirSync(dir, { recursive: true });
  for (const [rel, content] of Object.entries(files)) {
    const target = path.join(dir, rel);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, content);
  }
  return { data, dir };
}

function enable(data: string, ...names: string[]): void {
  fs.mkdirSync(path.join(data, "config"), { recursive: true });
  fs.writeFileSync(path.join(data, "config", "extensions.json"), JSON.stringify({ enabled: names }));
}

const MANIFEST = JSON.stringify({
  views: [
    {
      id: "board",
      title: "Ticket Board",
      entry: "ui/board.html",
      mount: "side-pane",
      permissions: ["invokeTool", "events"],
    },
  ],
  cards: [{ tool: "lookup", entry: "ui/card.js", inline: true }],
});

test("paladinDataDir follows the core's data directory", () => {
  expect(paladinDataDir({ PALADIN_DATA_DIR: "/tmp/pal" }, "linux", "/home/me")).toBe("/tmp/pal");
  expect(paladinDataDir({}, "linux", "/home/me")).toBe("/home/me/.config/paladin");
  expect(paladinDataDir({ XDG_CONFIG_HOME: "/xdg" }, "linux", "/home/me")).toBe("/xdg/paladin");
  expect(paladinDataDir({}, "darwin", "/Users/me")).toBe("/Users/me/Library/Application Support/paladin");
  expect(paladinDataDir({ APPDATA: "C:\\Users\\me\\AppData\\Roaming" }, "win32", "C:\\Users\\me")).toBe(
    path.join("C:\\Users\\me\\AppData\\Roaming", "paladin"),
  );
});

test("a frame gets its HTML and relative assets with a strict CSP", async () => {
  const { data } = pluginDir({
    "ui/board.html": '<!doctype html><link rel="stylesheet" href="./board.css"><p>board</p>',
    "ui/board.css": "p { color: red }",
    "ui/card.js": "window.__paladin_card = { render: () => 'ok' };",
    "paladin-plugin.json": MANIFEST,
  });
  enable(data, "board");
  const host = new PluginHost(data);
  expect(host.listErrors()).toEqual([]);
  expect(host.view("board", "board")?.title).toBe("Ticket Board");

  const page = await host.handle(new Request("plugin://board/board/ui/board.html"));
  expect(page.status).toBe(200);
  expect(page.headers.get("content-type")).toContain("text/html");
  expect(page.headers.get("content-security-policy")).toContain("connect-src 'none'");
  expect(page.headers.get("x-content-type-options")).toBe("nosniff");
  expect(await page.text()).toContain("board");

  const css = await host.handle(new Request("plugin://board/board/ui/board.css"));
  expect(css.status).toBe(200);
  expect(css.headers.get("content-type")).toContain("text/css");

  // An empty path serves the manifest entry.
  const entry = await host.handle(new Request("plugin://board/board"));
  expect(entry.status).toBe(200);
});

test("requests cannot escape ui/, the plugin or the scheme", async () => {
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-outside-"));
  fs.writeFileSync(path.join(outside, "secret.html"), "secret");
  const { data, dir } = pluginDir({
    "ui/board.html": "<p>board</p>",
    "ui/.hidden.html": "hidden",
    "ui/asset.bin": "binary",
    "paladin-plugin.json": MANIFEST,
    "package.json": "{}",
    "node_modules/x/index.js": "module",
  });
  fs.symlinkSync(path.join(outside, "secret.html"), path.join(dir, "ui", "link.html"));
  enable(data, "board");
  const host = new PluginHost(data);
  const cases = [
    "plugin://board/board/ui/../secret.html",
    "plugin://board/board/ui/%2e%2e/secret.html",
    "plugin://board/board/ui/%2e%2e%2fsecret.html",
    "plugin://board/board/../../secret.html",
    "plugin://board/board/ui/asset.bin",
    "plugin://board/board/ui/.hidden.html",
    "plugin://board/board/paladin-plugin.json",
    "plugin://board/board/node_modules/x/index.js",
    "plugin://board/board/ui/link.html",
    "plugin://board/other/ui/board.html",
    "plugin://other/board/ui/board.html",
    "https://board/board/ui/board.html",
  ];
  for (const url of cases) {
    const response = await host.handle(new Request(url));
    expect(response.status, url).toBe(404);
  }
});

test("a disabled plugin and an unknown asset are never served", async () => {
  const { data } = pluginDir({
    "ui/board.html": "<p>board</p>",
    "ui/app.js": "console.log('ok')",
    "paladin-plugin.json": MANIFEST,
  });
  enable(data);
  const host = new PluginHost(data);
  expect((await host.handle(new Request("plugin://board/board/ui/board.html"))).status).toBe(404);
});

test("an invalid view is reported and the valid one still serves", async () => {
  const { data } = pluginDir({
    "ui/board.html": "<p>board</p>",
    "ui/escape.html": "<p>escape</p>",
    "paladin-plugin.json": JSON.stringify({
      views: [
        { id: "board", title: "Board", entry: "ui/board.html", mount: "side-pane" },
        { id: "bad", title: "Bad", entry: "../escape.html" },
        { id: "other", title: "Other", entry: "ui/escape.html", permissions: ["network"] },
        { id: "gone", title: "Gone", entry: "ui/gone.html" },
      ],
    }),
  });
  enable(data, "board");
  const host = new PluginHost(data);
  expect(host.listViews().map((v) => v.id)).toEqual(["board"]);
  expect(host.listErrors().join("\n")).toContain("entry must stay under ui/");
  expect(host.listErrors().join("\n")).toContain("unknown permission");
  expect(host.listErrors().join("\n")).toContain("missing or too large");
});

test("cards are served as a document and its script", async () => {
  const { data } = pluginDir({
    "ui/board.html": "<p>board</p>",
    "ui/card.js": "window.__paladin_card = { render: () => 'ok' };",
    "ui/other.js": "window.__paladin_card = { render: () => 'no' };",
    "paladin-plugin.json": MANIFEST,
  });
  enable(data, "board");
  const host = new PluginHost(data);
  const doc = await host.handle(new Request("plugin://board/__card__/lookup/index.html"));
  expect(doc.status).toBe(200);
  expect(doc.headers.get("content-type")).toContain("text/html");
  expect(doc.headers.get("content-security-policy")).toContain("script-src 'self' plugin:");
  const html = await doc.text();
  expect(html).toContain('src="card.js"');
  expect(html).toContain("__paladin_card");
  const script = await host.handle(new Request("plugin://board/__card__/lookup/card.js"));
  expect(script.status).toBe(200);
  expect(script.headers.get("content-type")).toContain("text/javascript");
  expect(await script.text()).toContain("__paladin_card");
  expect((await host.handle(new Request("plugin://board/__card__/other/index.html"))).status).toBe(404);
  expect((await host.handle(new Request("plugin://other/__card__/lookup/index.html"))).status).toBe(404);
  expect((await host.handle(new Request("plugin://board/__card__/lookup/../card.js"))).status).toBe(404);
});

test("a symlinked plugin folder is refused", () => {
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-outside-"));
  fs.mkdirSync(path.join(outside, "ui"), { recursive: true });
  fs.writeFileSync(path.join(outside, "ui", "board.html"), "<p>board</p>");
  fs.writeFileSync(path.join(outside, "paladin-plugin.json"), MANIFEST);
  const data = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-data-"));
  fs.mkdirSync(path.join(data, "config", "extensions"), { recursive: true });
  fs.symlinkSync(outside, path.join(data, "config", "extensions", "board"));
  enable(data, "board");
  const host = new PluginHost(data);
  expect(host.listViews()).toEqual([]);
  expect(host.listErrors().join("\n")).toContain("outside the extensions folder");
});
