import * as api from "../../core/api";
import { bridge } from "../../core/bridge";
import { icon } from "../../icons";
import { errorText } from "./utils";
import type { WebTab } from "./types";

export function webHost(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export function normalizeWebUrl(raw: string): string | null {
  let text = raw.trim();
  if (!text) return null;
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) text = `https://${text}`;
  try {
    const url = new URL(text);
    return url.protocol === "https:" ? url.toString() : null;
  } catch {
    return null;
  }
}

export function setWebStatus(tab: WebTab, text: string): void {
  const status = tab.body.querySelector<HTMLElement>(".web-status");
  if (status) status.textContent = text;
}

export function newWebTab(
  sessionId: string,
  url: string,
  title: string,
  key: string,
  navigate: (tab: WebTab, url: string) => void,
): WebTab {
  const body = document.createElement("div");
  body.className = "pane-web";
  body.innerHTML = `<form class="web-toolbar">
    <button class="web-back" type="button" title="Back" aria-label="Back" disabled>${icon("chevron-left", "h-3.5 w-3.5")}</button>
    <button class="web-forward" type="button" title="Forward" aria-label="Forward" disabled>${icon("chevron-right", "h-3.5 w-3.5")}</button>
    <button class="web-reload" type="button" title="Reload" aria-label="Reload">${icon("repeat", "h-3.5 w-3.5")}</button>
    <input class="web-url" type="text" inputmode="url" spellcheck="false" autocomplete="off" placeholder="https://…" aria-label="Website address" />
    <button class="web-open" type="submit">Open</button>
    <button class="web-external" type="button" title="Open in the system browser" aria-label="Open in the system browser">${icon("external", "h-3.5 w-3.5")}</button>
  </form>
  <p class="web-status" role="status"></p>
  <div class="web-surface" aria-label="Website"></div>`;
  const tab: WebTab = {
    kind: "web",
    key,
    sessionId,
    viewName: "",
    title: title || (url ? webHost(url) : "New tab"),
    url,
    body,
  };
  tab.viewName = `web:${tab.key}`;
  const input = body.querySelector<HTMLInputElement>(".web-url")!;
  input.value = url;
  if (url) setWebStatus(tab, url);
  else setWebStatus(tab, "Type an https address.");
  body.querySelector<HTMLFormElement>(".web-toolbar")!.addEventListener("submit", (event) => {
    event.preventDefault();
    navigate(tab, input.value);
  });
  body.querySelector<HTMLButtonElement>(".web-back")!.addEventListener("click", () => {
    if (!tab.url) return;
    void bridge()
      .views?.back(tab.viewName)
      .catch(() => undefined);
  });
  body.querySelector<HTMLButtonElement>(".web-forward")!.addEventListener("click", () => {
    if (!tab.url) return;
    void bridge()
      .views?.forward(tab.viewName)
      .catch(() => undefined);
  });
  body.querySelector<HTMLButtonElement>(".web-reload")!.addEventListener("click", () => {
    if (!tab.url) {
      input.focus();
      return;
    }
    void api.reloadExternalPage(tab.viewName).catch((error) => setWebStatus(tab, errorText(error)));
  });
  body.querySelector<HTMLButtonElement>(".web-external")!.addEventListener("click", () => {
    if (!tab.url) return;
    void api.openExternalPage(tab.url).catch((error) => setWebStatus(tab, errorText(error)));
  });
  return tab;
}
