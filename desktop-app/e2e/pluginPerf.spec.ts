// pluginPerf.spec.ts gates the protocol handler's per-request cost. The
// Electron-only budgets (panel open, bridge round-trip, memory) are
// covered by the manual smoke run; the handler is the hot path every
// panel asset takes.
import { expect, test } from "@playwright/test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { PluginHost } from "../electron/pluginHost";

function fixture(): { data: string; dir: string } {
  const data = fs.mkdtempSync(path.join(os.tmpdir(), "paladin-perf-"));
  const dir = path.join(data, "config", "extensions", "board");
  fs.mkdirSync(path.join(dir, "ui"), { recursive: true });
  fs.writeFileSync(
    path.join(dir, "paladin-plugin.json"),
    JSON.stringify({
      views: [{ id: "board", title: "Board", entry: "ui/board.html", mount: "side-pane", permissions: ["invokeTool"] }],
      cards: [{ tool: "lookup", entry: "ui/card.js", inline: true }],
    }),
  );
  fs.writeFileSync(path.join(dir, "ui", "board.html"), "<!doctype html><p>board</p>");
  fs.writeFileSync(path.join(dir, "ui", "card.js"), "window.__paladin_card = { render: () => 'ok' };");
  fs.mkdirSync(path.join(data, "config"), { recursive: true });
  fs.writeFileSync(path.join(data, "config", "extensions.json"), JSON.stringify({ enabled: ["board"] }));
  return { data, dir };
}

function percentile(values: number[], p: number): number {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))] ?? 0;
}

test("a panel asset stays inside the interactive budget", async () => {
  const { data } = fixture();
  const host = new PluginHost(data);
  const times: number[] = [];
  for (let i = 0; i < 40; i++) {
    const start = performance.now();
    const res = await host.handle(new Request("plugin://board/board/ui/board.html"));
    times.push(performance.now() - start);
    expect(res.status).toBe(200);
  }
  expect(percentile(times, 0.5)).toBeLessThan(50);
  expect(percentile(times, 0.99)).toBeLessThan(150);
});

test("a card document and script stay inside the interactive budget", async () => {
  const { data } = fixture();
  const host = new PluginHost(data);
  const times: number[] = [];
  for (let i = 0; i < 20; i++) {
    const start = performance.now();
    expect((await host.handle(new Request("plugin://board/__card__/lookup/index.html"))).status).toBe(200);
    expect((await host.handle(new Request("plugin://board/__card__/lookup/card.js"))).status).toBe(200);
    times.push(performance.now() - start);
  }
  expect(percentile(times, 0.5)).toBeLessThan(50);
  expect(percentile(times, 0.99)).toBeLessThan(150);
});
