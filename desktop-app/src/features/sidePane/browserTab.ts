import { bridge, hasBridge } from "../../core/bridge";
import { inputEl } from "../../core/dom";
import { state } from "../../core/state";
import { icon } from "../../icons";
import { errorText } from "./utils";
import type { BrowserTab } from "./types";

export function newBrowserTab(sessionId: string, key: string, onNavigate: () => void, onPass: () => void): BrowserTab {
  const body = document.createElement("div");
  body.className = "pane-browser";
  body.innerHTML = `<form class="browser-toolbar"><input class="browser-url" type="url" inputmode="url" spellcheck="false" placeholder="http://localhost:8080 or https://…" aria-label="Browser URL" /><button class="browser-open" type="submit">Open</button><button class="browser-annotate" type="button" aria-label="Comment on elements" aria-pressed="false" title="Comment on elements">${icon("comments", "h-4 w-4")}<span>Comment</span></button><button class="browser-pass" type="button" disabled>Pass to agent</button></form><p class="browser-status" role="status">Open a local app or website in the agent browser, separate from your personal browser.</p><div class="browser-surface" aria-label="Browser page"></div>`;
  const tab: BrowserTab = {
    kind: "browser",
    key,
    sessionId,
    title: "Agent browser",
    body,
    url: "",
    picking: false,
    pickToken: 0,
    noteCount: 0,
    passing: false,
  };
  body.querySelector<HTMLFormElement>("form")!.addEventListener("submit", (event) => {
    event.preventDefault();
    const url = body.querySelector<HTMLInputElement>(".browser-url")!.value.trim();
    navigateBrowser(tab, url, onNavigate);
  });
  body.querySelector<HTMLButtonElement>(".browser-annotate")!.addEventListener("click", () => {
    if (tab.picking) stopBrowserPick(tab);
    else void startBrowserPick(tab);
  });
  body
    .querySelector<HTMLButtonElement>(".browser-pass")!
    .addEventListener("click", () => void passBrowserComments(tab, onPass));
  return tab;
}

export function stopBrowserPick(tab: BrowserTab): void {
  if (!tab.picking) return;
  tab.picking = false;
  tab.pickToken++;
  void bridge()
    .browser?.cancelPick(tab.sessionId)
    .catch((error: unknown) => {
      tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = errorText(error);
    });
  updateBrowserToolbar(tab);
}

async function startBrowserPick(tab: BrowserTab): Promise<void> {
  if (!hasBridge() || !bridge().browser?.pick) {
    tab.body.querySelector<HTMLElement>(".browser-status")!.textContent =
      "Element selection needs the desktop browser.";
    return;
  }
  tab.picking = true;
  const token = ++tab.pickToken;
  updateBrowserToolbar(tab);
  try {
    await bridge().browser!.cancelPick(tab.sessionId);
  } catch (error) {
    if (token === tab.pickToken) {
      tab.picking = false;
      tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = errorText(error);
      updateBrowserToolbar(tab);
    }
    return;
  }
  if (!tab.picking || token !== tab.pickToken) return;
  while (tab.picking && token === tab.pickToken) {
    try {
      const element = await bridge().browser!.pick(tab.sessionId);
      if (!tab.picking || token !== tab.pickToken || !element) break;
    } catch (error) {
      if (token === tab.pickToken)
        tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = errorText(error);
      break;
    }
  }
  if (token === tab.pickToken) {
    tab.picking = false;
    updateBrowserToolbar(tab);
  }
}

export function updateBrowserToolbar(tab: BrowserTab): void {
  const toggle = tab.body.querySelector<HTMLButtonElement>(".browser-annotate")!;
  toggle.setAttribute("aria-pressed", String(tab.picking));
  toggle.title = tab.picking ? "Stop selecting elements" : "Comment on elements";
  const pass = tab.body.querySelector<HTMLButtonElement>(".browser-pass")!;
  pass.disabled = tab.passing || tab.noteCount === 0;
  pass.textContent = tab.noteCount ? `Pass to agent (${tab.noteCount})` : "Pass to agent";
}

async function passBrowserComments(tab: BrowserTab, onPass: () => void): Promise<void> {
  if (tab.passing || !hasBridge() || !bridge().browser?.getNotes) return;
  tab.passing = true;
  updateBrowserToolbar(tab);
  stopBrowserPick(tab);
  try {
    const notes = await bridge().browser!.getNotes(tab.sessionId);
    if (!notes.length || state.sessionId !== tab.sessionId) return;
    const lines = notes.map(
      (note, index) =>
        `${index + 1}. URL: ${note.url}\n   Element: <${note.tag}>${note.label || note.text ? ` ${JSON.stringify(note.label || note.text)}` : ""}\n   CSS selector: ${note.selector}\n   Change: ${note.comment}`,
    );
    const feedback = `Browser UI feedback:\n${lines.join("\n")}`;
    inputEl.value = `${inputEl.value.trimEnd()}${inputEl.value.trim() ? "\n\n" : ""}${feedback}`;
    inputEl.dispatchEvent(new Event("input", { bubbles: true }));
    await bridge().browser!.clearNotes(tab.sessionId);
    onPass();
    inputEl.focus();
  } catch (error) {
    tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = errorText(error);
  } finally {
    tab.passing = false;
    updateBrowserToolbar(tab);
  }
}

export function navigateBrowser(tab: BrowserTab, url: string, onNavigate: () => void): void {
  stopBrowserPick(tab);
  const status = tab.body.querySelector<HTMLElement>(".browser-status")!;
  if (!url) {
    status.textContent = "Enter a URL.";
    return;
  }
  tab.body.querySelector<HTMLInputElement>(".browser-url")!.value = url;
  if (!hasBridge() || !bridge().browser) {
    status.textContent = "The desktop browser is unavailable.";
    return;
  }
  status.textContent = "Opening…";
  void bridge()
    .browser!.open(tab.sessionId, url)
    .then(
      () => {
        tab.url = url;
        status.textContent = url;
        onNavigate();
      },
      (error: unknown) => {
        status.textContent = errorText(error);
      },
    );
}
