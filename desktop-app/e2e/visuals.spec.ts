// visuals.spec.ts covers the visual tool's card: drawn live and from
// history, sandboxed with no network, sized to its content, themed, and
// able to fill (never send) the composer.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const WIDGET = `<style>.box{height:300px;border:1px solid var(--v-border);color:var(--v-fg)}</style>
<div class="box" id="box">Revenue 42</div>
<button id="ask" onclick="paladin.sendPrompt('Break revenue down by region')">Ask</button>
<img id="leak" src="http://127.0.0.1:9/pixel.png">
<script>
  document.addEventListener("securitypolicyviolation", (e) => { document.body.dataset.blocked = e.blockedURI; });
  fetch("http://127.0.0.1:9/x").catch(() => { document.body.dataset.fetch = "refused"; });
  document.body.dataset.accent = getComputedStyle(document.documentElement).getPropertyValue("--v-accent").trim();
</script>`;

async function liveTurn(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRunTurn = (_p: any, handlers: any) =>
      new Promise((resolve) => {
        w.__h = handlers;
        w.__done = () => resolve({ sessionId: "", reply: "", stopped: false });
      });
  });
  await page.goto("/");
  // The startup splash keeps the app inert for a moment; clicks inside the frame are dropped until it lifts.
  await expect(page.locator("#startup")).toHaveCount(0);
  await page.fill("#promptInput", "chart it");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => Boolean((window as any).__h));
}

function emit(page: Page, status: string, code = WIDGET): Promise<void> {
  return page.evaluate(
    ([status, code]) =>
      (window as any).__h.onTool({
        id: "v1",
        name: "visual",
        arguments: JSON.stringify({ title: "Revenue", code, caption: "Last quarter" }),
        result: status === "ok" ? JSON.stringify({ title: "Revenue", format: "html", bytes: code.length }) : "",
        status,
      }),
    [status, code],
  );
}

test("a visual draws inline in a sandbox with no network", async ({ page }) => {
  await liveTurn(page);
  await emit(page, "running");
  const card = page.locator('#messages .visual-card[data-visual="v1"]');
  await expect(card.locator(".visual-wait")).toHaveText("Drawing…");
  await emit(page, "ok");
  await expect(card.locator(".visual-title")).toHaveText("Revenue");
  await expect(card.locator(".visual-format")).toHaveText("HTML");
  await expect(card.locator(".visual-caption")).toHaveText("Last quarter");
  await expect(page.locator('#messages .tool-row[data-tool="visual"]')).toHaveCount(0);

  const frameEl = card.locator("iframe.visual-frame");
  await expect(frameEl).toHaveAttribute("sandbox", "allow-scripts");
  // The frame is its own page with its own policy, not a srcdoc that
  // would inherit the app's.
  await expect(frameEl).toHaveAttribute("src", /visual-frame\.html$/);
  expect(await frameEl.getAttribute("srcdoc")).toBeNull();
  const frame = page.frameLocator('.visual-card[data-visual="v1"] iframe');
  await expect(frame.locator("#box")).toHaveText("Revenue 42");
  await expect(frame.locator('meta[http-equiv="Content-Security-Policy"]')).toHaveAttribute(
    "content",
    /default-src 'none'/,
  );
  // Theme variables reach the frame; the network does not.
  await expect(frame.locator("body")).toHaveAttribute("data-accent", /#[0-9a-f]{6}/i);
  await expect(frame.locator("body")).toHaveAttribute("data-fetch", "refused");
  await expect(frame.locator("body")).toHaveAttribute("data-blocked", /127\.0\.0\.1/);
  // The frame has no way into the app.
  const reach = await page
    .frames()
    .find((f) => f !== page.mainFrame())!
    .evaluate(() => {
      try {
        return String((window.parent as any).document.title);
      } catch {
        return "blocked";
      }
    });
  expect(reach).toBe("blocked");
  // The card fits the content (300px box) instead of a fixed size.
  await expect.poll(async () => (await frameEl.boundingBox())?.height ?? 0).toBeGreaterThan(300);
});

test("sendPrompt fills the composer for the user to send", async ({ page }) => {
  await liveTurn(page);
  await emit(page, "ok");
  const frame = page.frameLocator('.visual-card[data-visual="v1"] iframe');
  // The card is taller than the visible chat; wait for the frame to
  // reach its content height, then scroll it clear of the composer, or
  // the click lands on the footer instead of the button.
  const frameEl = page.locator('.visual-card[data-visual="v1"] iframe');
  await expect.poll(async () => (await frameEl.boundingBox())?.height ?? 0).toBeGreaterThan(300);
  await page.locator('.visual-card[data-visual="v1"]').evaluate((el) => el.scrollIntoView({ block: "end" }));
  await frame.locator("#ask").click();
  await expect(page.locator("#promptInput")).toHaveValue("Break revenue down by region");
  // Nothing was sent: only the first turn reached the core.
  expect(await page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBeLessThanOrEqual(1);
});

// A visual may suggest a message only when the user clicks inside it, and
// never replaces a draft without asking.
const PUSHY = `<button id="ask" onclick="paladin.sendPrompt('From the button')">Ask</button>
<script>
  paladin.sendPrompt("On load");
  let n = 0;
  const t = setInterval(() => { paladin.sendPrompt("Timer " + n); if (++n > 6) clearInterval(t); }, 150);
</script>`;

test("a visual cannot fill the composer by itself or replace a draft", async ({ page }) => {
  await liveTurn(page);
  await emit(page, "ok", PUSHY);
  const card = page.locator('.visual-card[data-visual="v1"]');
  const frame = page.frameLocator('.visual-card[data-visual="v1"] iframe');
  await expect(frame.locator("#ask")).toBeVisible();
  await page.waitForTimeout(1500);
  await expect(page.locator("#promptInput")).toHaveValue("");

  // With a draft in the composer, a click asks before replacing it.
  await page.fill("#promptInput", "my own draft");
  await frame.locator("#ask").click();
  const bar = card.locator(".visual-suggest");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("From the button");
  await expect(page.locator("#promptInput")).toHaveValue("my own draft");
  await bar.locator("[data-visual-keep]").click();
  await expect(bar).toBeHidden();
  await expect(page.locator("#promptInput")).toHaveValue("my own draft");
  await frame.locator("#ask").click();
  await bar.locator("[data-visual-replace]").click();
  await expect(page.locator("#promptInput")).toHaveValue("From the button");
  await expect(bar).toBeHidden();
});

// heapCount counts the page's native objects whose name matches want (a
// DOM node is named after its tag, "Detached " first once removed) in a
// heap snapshot taken after a GC.
async function heapCount(page: Page, want: RegExp): Promise<number> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("HeapProfiler.enable");
  await cdp.send("HeapProfiler.collectGarbage");
  let raw = "";
  cdp.on("HeapProfiler.addHeapSnapshotChunk", (e) => (raw += e.chunk));
  await cdp.send("HeapProfiler.takeHeapSnapshot", { reportProgress: false });
  await cdp.detach();
  const snap = JSON.parse(raw) as {
    snapshot: { meta: { node_fields: string[]; node_types: [string[]] } };
    nodes: number[];
    strings: string[];
  };
  const { node_fields: fields, node_types: types } = snap.snapshot.meta;
  const typeAt = fields.indexOf("type");
  const nameAt = fields.indexOf("name");
  let count = 0;
  for (let i = 0; i < snap.nodes.length; i += fields.length) {
    if (types[0][snap.nodes[i + typeAt]] === "native" && want.test(snap.strings[snap.nodes[i + nameAt]])) count++;
  }
  return count;
}

const VISUAL_FRAME = /^(Detached )?<iframe class="visual-frame"/;
const OBSERVER = /^MutationObserver$/;

test("removed visuals keep no frames or observers alive", async ({ page }) => {
  await liveTurn(page);
  const observersBefore = await heapCount(page, OBSERVER);
  for (const id of ["v1", "v2", "v3"]) {
    await page.evaluate(
      (id) =>
        (window as any).__h.onTool({
          id,
          name: "visual",
          arguments: JSON.stringify({ title: id, code: `<p id="p">${id}</p>` }),
          result: "{}",
          status: "ok",
        }),
      id,
    );
  }
  // Plain DOM counts: Playwright's own element queries would hold frames.
  const frames = () => page.evaluate(() => document.querySelectorAll(".visual-card iframe").length);
  await expect.poll(frames).toBe(3);
  for (let i = 0; i < 4; i++) {
    await page.evaluate(() => document.documentElement.classList.toggle("light"));
    await page.waitForTimeout(50);
  }
  const card = page.locator('.visual-card[data-visual="v1"]');
  for (let i = 0; i < 2; i++) await card.locator("[data-visual-expand]").click();
  await expect.poll(frames).toBe(3);
  expect(await heapCount(page, VISUAL_FRAME)).toBe(3);
  // A card put back in the page (switching back to a cached chat) reloads
  // its frame, which draws again.
  await page.evaluate(() => {
    const card = document.querySelector('.visual-card[data-visual="v2"]')!;
    const parent = card.parentElement!;
    card.remove();
    parent.append(card);
  });
  await expect(page.frameLocator('.visual-card[data-visual="v2"] iframe').locator("#p")).toHaveText("v2");
  // The turn ends and the chat is replaced. The session's cached view may
  // keep its cards, but never more than one frame each, and no observer
  // per card.
  await page.evaluate(() => {
    const w = window as any;
    w.__done();
    w.__h = null;
    document.querySelector("#messages")!.innerHTML = "";
  });
  expect(await heapCount(page, VISUAL_FRAME)).toBeLessThanOrEqual(3);
  expect(await heapCount(page, OBSERVER)).toBeLessThanOrEqual(observersBefore + 1);
});

test("source, open larger, and a failed visual", async ({ page }) => {
  await liveTurn(page);
  await emit(page, "ok", `<svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4" fill="var(--v-accent)"/></svg>`);
  const card = page.locator('#messages .visual-card[data-visual="v1"]');
  await expect(card.locator(".visual-format")).toHaveText("SVG");
  await card.locator("[data-visual-source]").click();
  await expect(card.locator(".visual-source")).toContainText("<circle");
  await card.locator("[data-visual-expand]").click();
  await expect(card).toHaveClass(/is-expanded/);
  await page.keyboard.press("Escape");
  await expect(card).not.toHaveClass(/is-expanded/);
  await page.evaluate(() =>
    (window as any).__h.onTool({
      id: "v2",
      name: "visual",
      arguments: JSON.stringify({ title: "Bad", code: "<html></html>" }),
      result: JSON.stringify({ error: "visual: send a fragment without <html>" }),
      status: "failed",
    }),
  );
  await expect(page.locator('.visual-card[data-visual="v2"] .visual-err')).toContainText("fragment");
});

test("a visual in history is drawn again", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((code) => {
    const at = Math.floor(Date.now() / 1000);
    (window as any).__e2eHistory = [
      { role: "user", content: "draw", createdAt: at, index: 0 },
      {
        role: "tool",
        name: "visual",
        arguments: JSON.stringify({ title: "Flow", code }),
        content: JSON.stringify({ title: "Flow", format: "svg" }),
        createdAt: at,
      },
      { role: "assistant", content: "Here is the flow.", createdAt: at },
    ];
  }, `<svg viewBox="0 0 20 10"><text x="1" y="8">Flow chart</text></svg>`);
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  const card = page.locator("#messages .visual-card");
  await expect(card.locator(".visual-title")).toHaveText("Flow");
  await expect(page.frameLocator("#messages .visual-card iframe").locator("text")).toHaveText("Flow chart");
});
