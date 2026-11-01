// Real Electron + Playwright smoke, using only an ephemeral loopback server
// (web-site names are mapped to it, see main.ts).
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { BrowserWindow } from "electron";
import type { Page } from "@playwright/test";
import { BrowserManager, type BrowserReply, type BrowserRequest, type EgressSetup } from "./browser";

// FlakyRoutes fails Playwright's first route installation, as a broken
// CDP session would.
class FlakyRoutes extends BrowserManager {
  private failures = 1;
  protected override installRoute(page: Page, handler: Parameters<Page["route"]>[1]): Promise<unknown> {
    if (this.failures-- > 0) return Promise.reject(new Error("injected route failure"));
    return super.installRoute(page, handler);
  }
}

// guardFailureCheck: when Playwright's guard cannot be installed, open
// fails without loading anything, and a retry guards the page again.
async function guardFailureCheck(window: () => BrowserWindow | null, egress: EgressSetup, port: number): Promise<void> {
  const flaky = new FlakyRoutes(window, () => undefined, egress);
  const call = (id: string, action: string, extra: Partial<BrowserRequest>): Promise<BrowserReply> =>
    flaky.execute({ event: "browser/request", id, sessionId: "smoke-flaky", action, ...extra });
  try {
    const failed = await call("flaky-1", "open", { url: `http://site-a.test:${port}/` });
    if (failed.ok || !failed.error?.includes("injected route failure")) {
      throw new Error(`open went ahead without the navigation guard: ${JSON.stringify(failed)}`);
    }
    const retry = await call("flaky-2", "open", { url: `http://site-a.test:${port}/` });
    if (!retry.ok) throw new Error(`open after a guard failure: ${retry.error}`);
    const away = await call("flaky-3", "click", { selector: "#away" });
    const result = away.result as { url?: string; blocked_navigation?: string } | undefined;
    if (!away.ok || !result?.blocked_navigation?.includes("site-b.test") || !result.url?.includes("site-a.test")) {
      throw new Error(`a link left the site after a guard failure: ${JSON.stringify(away)}`);
    }
  } finally {
    await flaky.stop();
  }
}

export async function runBrowserSmoke(
  browsers: BrowserManager,
  window: () => BrowserWindow | null,
  egress: EgressSetup,
): Promise<void> {
  let loopbackHits = 0;
  let rebindHits = 0;
  const server = createServer((_req, res) => {
    const host = (_req.headers.host ?? "").split(":")[0];
    if (host === "rebind.test") {
      // Reached only if a public-looking name got through to this machine.
      rebindHits++;
      res.writeHead(200, { "Content-Type": "text/html", "Access-Control-Allow-Origin": "*" });
      res.end("<title>rebind</title>reached");
      return;
    }
    if (_req.url === "/redirect-rebind") {
      res.writeHead(302, { Location: `http://rebind.test:${(server.address() as AddressInfo).port}/probe-redirect` });
      res.end();
      return;
    }
    if (_req.url?.startsWith("/probe")) {
      // Reached only if a web page got through to "this machine".
      if (host === "127.0.0.1") loopbackHits++;
      res.writeHead(200, { "Content-Type": "text/plain", "Access-Control-Allow-Origin": "*" });
      res.end("probe");
      return;
    }
    if (host === "site-a.test" || host === "site-b.test") {
      const port = (server.address() as AddressInfo).port;
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      res.end(
        `<html><head><title>${host}</title></head><body><h1>Welcome to ${host}</h1>` +
          `<a id="same" href="http://sub.${host}:${port}/">sub</a>` +
          `<a id="away" href="http://site-b.test:${port}/">Other site</a>` +
          `<img id="self" src="/probe-self"><img id="redir" src="/redirect-rebind"><output id="loop"></output><output id="rebind"></output>` +
          `<script>fetch("http://127.0.0.1:${port}/probe-loop").then(()=>{document.getElementById("loop").textContent="reached"}).catch(()=>{document.getElementById("loop").textContent="blocked"});` +
          `fetch("http://rebind.test:${port}/probe-rebind").then(()=>{document.getElementById("rebind").textContent="reached"}).catch(()=>{document.getElementById("rebind").textContent="blocked"})</script>` +
          `</body></html>`,
      );
      return;
    }
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
    // The first pages open in panes that are not on screen: Playwright
    // must attach before they load (its navigation guard sees the load),
    // or Chromium holds back a hidden page's timers and it stalls.
    const started = Date.now();
    await Promise.all([
      call("smoke-a", "open", { url: `http://127.0.0.1:${port}` }),
      call("smoke-b", "open", { url: `http://127.0.0.1:${port}` }),
    ]);
    if (Date.now() - started > 15_000)
      throw new Error(`the first hidden pages took ${Date.now() - started} ms to open`);
    for (const id of ["smoke-a", "smoke-b"]) {
      if (browsers.routedLoads(id)[0] !== `http://127.0.0.1:${port}/`) {
        throw new Error(
          `Playwright attached after the first load in ${id}: ${JSON.stringify(browsers.routedLoads(id))}`,
        );
      }
    }
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
    if (browsers.show("smoke-a")) throw new Error("a session browser was shown before the pane gave its bounds");
    browsers.setBounds({ x: 760, y: 56, width: 440, height: 620 });
    if (!browsers.show("smoke-a")) throw new Error("a session browser did not show in the pane");
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
    // A web site: it opens, stays on its own site, and cannot reach this
    // machine. site-a.test and site-b.test are mapped to the loopback
    // server above, so nothing leaves the machine.
    const opened = await call("smoke-web", "open", { url: `http://site-a.test:${port}/` });
    if (!JSON.stringify(opened.result).includes("site-a.test")) throw new Error("web site did not open");
    await call("smoke-web", "wait", { selector: "#loop:has-text('blocked')" });
    const settled = await call("smoke-web", "wait", { selector: "#rebind:has-text('blocked')" });
    if (loopbackHits !== 0) throw new Error("a web page reached the local machine");
    // rebind.test looks public but resolves to this machine: refused for
    // a fetch, after a redirect and as a page of its own.
    if (rebindHits !== 0) throw new Error("a public-looking name reached the local machine");
    if (!JSON.stringify([opened.result, settled.result]).includes("rebind.test: rebind.test resolves to 127.0.0.1")) {
      throw new Error(`refused requests were not reported: ${JSON.stringify([opened.result, settled.result])}`);
    }
    const away = await call("smoke-web", "click", { selector: "#away" });
    const awayResult = away.result as { url?: string; blocked_navigation?: string };
    if (!awayResult.blocked_navigation?.includes("site-b.test") || !awayResult.url?.includes("site-a.test")) {
      throw new Error(`navigation to another site was not blocked and reported: ${JSON.stringify(away.result)}`);
    }
    const back = await call("smoke-web", "snapshot");
    if (JSON.stringify(back.result).includes("blocked_navigation"))
      throw new Error("a blocked link was reported twice");
    await call("smoke-web", "open", { url: `http://site-b.test:${port}/` });
    const nowB = await call("smoke-web", "snapshot");
    if (!JSON.stringify(nowB.result).includes("Welcome to site-b.test")) throw new Error("an opened site did not load");
    const rebind = await browsers.execute({
      event: "browser/request",
      id: "smoke-web-rebind",
      sessionId: "smoke-web",
      action: "open",
      url: `http://rebind.test:${port}/`,
    });
    if (rebind.ok || !rebind.error?.includes("this machine") || rebindHits !== 0) {
      throw new Error(`a page on a name resolving to this machine opened: ${JSON.stringify(rebind)}`);
    }
    const fileTry = await browsers.execute({
      event: "browser/request",
      id: "smoke-web-file",
      sessionId: "smoke-web",
      action: "open",
      url: "file:///etc/hosts",
    });
    if (fileTry.ok) throw new Error("the browser opened a file URL");
    await guardFailureCheck(window, egress, port);
    browsers.show(null);
    const windowsBeforeClose = BrowserWindow.getAllWindows().length;
    browsers.windowClosed();
    if (BrowserWindow.getAllWindows().length !== windowsBeforeClose - 3) {
      throw new Error("closing the app window left browser parking windows alive");
    }
  } finally {
    await browsers.stop();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
}
