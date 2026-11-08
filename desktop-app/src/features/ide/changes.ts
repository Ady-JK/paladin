import * as api from "../../core/api";
import { state } from "../../core/state";
import type { SessionChanges } from "../../core/types";
import { newChangesTab, paintChangesTab } from "../sidePane/changesTab";
import type { ChangesTab } from "../sidePane/types";

// Reuse the same diff renderer as the session Changes tab.
export function initIDEChanges(showEditor: () => void): void {
  const button = document.querySelector<HTMLButtonElement>("#ideChangesToggle")!;
  const panel = document.querySelector<HTMLElement>("#ideChangesPanel")!;
  let tab: ChangesTab | undefined;
  let data: SessionChanges | undefined;
  let request = 0;
  let open = false;

  const update = (): void => {
    panel.hidden = !open;
    const label = open ? "Hide changes" : "Show changes";
    button.setAttribute("aria-label", label);
    button.setAttribute("title", label);
    button.setAttribute("aria-pressed", String(open));
  };
  const load = async (): Promise<void> => {
    const id = state.sessionId;
    const sequence = ++request;
    if (!open) return;
    if (!id) {
      tab = undefined;
      panel.textContent = "Open a session to see its changes.";
      return;
    }
    if (!tab || tab.sessionId !== id) {
      data = undefined;
      tab = newChangesTab(id, "ide-changes", () => data);
      panel.replaceChildren(tab.body);
      paintChangesTab(tab, undefined);
    }
    try {
      const result = await api.sessionChanges(id, null);
      if (sequence !== request || !open || state.sessionId !== id || !tab) return;
      data = result;
      paintChangesTab(tab, data);
    } catch {
      if (sequence === request && open) panel.textContent = "Could not load session changes.";
      tab = undefined;
    }
  };
  button.addEventListener("click", () => {
    open = !open;
    if (open) showEditor();
    update();
    void load();
  });
  window.addEventListener("paladin:sessions-changed", () => {
    if (open && tab?.sessionId !== state.sessionId) void load();
  });
  window.addEventListener("paladin:changes-updated", (event) => {
    const detail = (event as CustomEvent<{ sessionId: string; data: SessionChanges }>).detail;
    if (!open || detail.sessionId !== state.sessionId) return;
    if (!tab || tab.sessionId !== detail.sessionId) {
      void load();
      return;
    }
    // Ignore any older request already in flight for the same session.
    request++;
    data = detail.data;
    paintChangesTab(tab, data);
  });
  update();
}
