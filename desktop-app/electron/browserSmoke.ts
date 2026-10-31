// Real Electron + Playwright smoke, using only an ephemeral loopback server.
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { BrowserWindow } from "electron";
import type { BrowserManager, BrowserRequest } from "./browser";

export async function runBrowserSmoke(browsers: BrowserManager): Promise<void> {
  const server = createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end(
      `<html><body><h1>Paladin browser smoke</h1><label>Name <input aria-label="Name"></label><button onclick="document.querySelector('output').textContent='Hello '+document.querySelector('input').value">Greet</button><output></output><a id="route-link" href="${_req.url === "/" ? "/other" : "/"}">Switch page</a><button id="center-target" style="position:fixed;left:50%;top:50%;transform:translate(-50%,-50%)" onclick="document.querySelector('output').textContent='Picker leaked a click'">Select me</button></body></html>`,
    );
  });
  await new Promise<void>((resolve, reject) =>
    server.listen(0, "127.0.0.1", (error?: Error) => (error ? reject(error) : resolve())),
  );
  const port = (server.address() as AddressInfo).port;
  const call = async (sessionId: string, action: string, extra: Partial<BrowserRequest> = {}) => {
    const reply = await browsers.execute({
      event: "browser/request",
      id: `${sessionId}-${action}`,
      sessionId,
      action,
      ...extra,
    });
    if (!reply.ok) throw new Error(reply.error ?? `browser ${action} failed`);
    return reply;
  };
  try {
    await Promise.all([
      call("smoke-a", "open", { url: `http://127.0.0.1:${port}` }),
      call("smoke-b", "open", { url: `http://127.0.0.1:${port}` }),
    ]);
    await call("smoke-a", "fill", { role: "textbox", name: "Name", text: "Ada" });
    await call("smoke-b", "fill", { role: "textbox", name: "Name", text: "Grace" });
    await Promise.all([
      call("smoke-a", "click", { role: "button", name: "Greet" }),
      call("smoke-b", "click", { role: "button", name: "Greet" }),
    ]);
    const a = await call("smoke-a", "snapshot");
    const b = await call("smoke-b", "snapshot");
    if (!JSON.stringify(a.result).includes("Hello Ada") || !JSON.stringify(b.result).includes("Hello Grace")) {
      throw new Error("parallel session pages did not stay isolated");
    }
    const shot = await call("smoke-a", "screenshot");
    if (!shot.imageBase64 || shot.imageBase64.length < 100) throw new Error("screenshot was empty");
    browsers.show("smoke-a");
    const selection = browsers.pick("smoke-a");
    await call("smoke-a", "click", { selector: "[data-paladin-picker]" });
    const element = await selection;
    if (
      element?.selector !== "#center-target" ||
      element.tag !== "button" ||
      element.url !== `http://127.0.0.1:${port}/`
    ) {
      throw new Error("element picker did not return a page locator and URL");
    }
    if (JSON.stringify((await call("smoke-a", "snapshot")).result).includes("Picker leaked a click")) {
      throw new Error("element picker activated the page control");
    }
    const cancelled = browsers.pick("smoke-a");
    await call("smoke-a", "wait", { selector: "[data-paladin-picker]" });
    await call("smoke-a", "click", { selector: "[data-paladin-annotation-root] .badge" });
    await call("smoke-a", "fill", {
      selector: "[data-paladin-annotation-root] textarea",
      text: "Make the button larger",
    });
    if (browsers.getNotes("smoke-a").length) {
      throw new Error("unsaved editor text was exposed as a comment");
    }
    await call("smoke-a", "click", { selector: "[data-paladin-annotation-root] .save" });
    for (let attempt = 0; attempt < 20 && browsers.getNotes("smoke-a").length === 0; attempt++) {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    if (browsers.getNotes("smoke-a")[0]?.comment !== "Make the button larger" || browsers.getNotes("smoke-b").length) {
      throw new Error("floating editor did not save a session-scoped comment");
    }
    await browsers.cancelPick("smoke-a");
    if ((await cancelled) !== null) throw new Error("cancelled element picker returned a selection");
    await call("smoke-a", "click", { selector: "#route-link" });
    await call("smoke-a", "click", { selector: "#route-link" });
    await call("smoke-a", "wait", { selector: "[data-paladin-annotation-root] .badge" });
    await call("smoke-a", "reload");
    await call("smoke-a", "wait", { selector: "[data-paladin-annotation-root] .badge" });
    await browsers.clearNotes("smoke-a");
    if (browsers.getNotes("smoke-a").length) throw new Error("passed comments were not cleared");
    browsers.show(null);
    const windowsBeforeClose = BrowserWindow.getAllWindows().length;
    browsers.windowClosed();
    if (BrowserWindow.getAllWindows().length !== windowsBeforeClose - 2) {
      throw new Error("closing the app window left browser parking windows alive");
    }
  } finally {
    await browsers.stop();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
}
