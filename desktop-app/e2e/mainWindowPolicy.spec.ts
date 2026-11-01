import { expect, test } from "@playwright/test";
import { appFrameNavigationAllowed, appNavigationAllowed, isMainWindowFrame } from "../electron/mainWindowPolicy";

test("main app window stays on its trusted page", () => {
  expect(appNavigationAllowed("http://localhost:5173/", "http://localhost:5173/settings#providers")).toBe(true);
  expect(appNavigationAllowed("http://localhost:5173/", "https://attacker.test/")).toBe(false);
  expect(appNavigationAllowed("http://localhost:5173/", "http://localhost.evil.test/")).toBe(false);
  expect(appNavigationAllowed("file:///app/dist/index.html", "file:///app/dist/index.html#chat")).toBe(true);
  expect(appNavigationAllowed("file:///app/dist/index.html", "file:///app/dist/other.html")).toBe(false);
  expect(appNavigationAllowed("file:///app/dist/index.html", "https://attacker.test/")).toBe(false);
});

test("only the app main frame may invoke privileged IPC", () => {
  const contents = {};
  const mainFrame = {};
  expect(isMainWindowFrame(contents, mainFrame, contents, mainFrame)).toBe(true);
  expect(isMainWindowFrame({}, mainFrame, contents, mainFrame)).toBe(false);
  expect(isMainWindowFrame(contents, {}, contents, mainFrame)).toBe(false);
  expect(isMainWindowFrame(contents, mainFrame, null, mainFrame)).toBe(false);
});

test("frames in the app window may only load the visual frame page", () => {
  for (const app of ["http://localhost:5173", "http://localhost:5173/", "file:///app/dist/index.html"]) {
    const frame = new URL("visual-frame.html", app).href;
    expect(appFrameNavigationAllowed(app, frame)).toBe(true);
    expect(appFrameNavigationAllowed(app, "about:blank")).toBe(true);
    for (const url of [
      `${frame}?x=1`,
      new URL("other.html", app).href,
      "about:srcdoc",
      "https://attacker.test/visual-frame.html",
      "data:text/html,hi",
      "javascript:alert(1)",
    ]) {
      expect(appFrameNavigationAllowed(app, url)).toBe(false);
    }
  }
});

test("the app page has a strict content security policy", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  const csp = await page.locator('meta[http-equiv="Content-Security-Policy"]').getAttribute("content");
  expect(csp).toContain("script-src 'self'");
  expect(csp).not.toContain("unsafe-eval");
  expect(csp).toMatch(/object-src 'none'/);
  // An injected handler does not run, even if markup gets through.
  await page.evaluate(() => {
    const div = document.createElement("div");
    div.innerHTML = `<img src="data:," onerror="window.__inline = 1"><a id="js" href="javascript:window.__href=1">x</a>`;
    document.body.append(div);
    (document.getElementById("js") as HTMLAnchorElement).click();
  });
  await page.waitForTimeout(300);
  expect(await page.evaluate(() => [(window as any).__inline, (window as any).__href])).toEqual([undefined, undefined]);
});
