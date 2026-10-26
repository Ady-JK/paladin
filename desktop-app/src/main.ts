// Bootstrap: wire everything, load workspace + providers + sessions.
import { api } from "./api";
import { send, scrollToBottom } from "./chat";
import { state } from "./state";
import { icon, paintIcons } from "./icons";
import { initModels, loadProviders } from "./models";
import { initSessions, refreshSessions } from "./sessions";
import { initSettings } from "./settings";
import { initSites, refreshExternalVisibility, trackBounds } from "./sites";
import { initWorkspace } from "./workspace";
import { toast } from "./ui";

async function boot(): Promise<void> {
  paintIcons();
  document.querySelector("#headerTitle")!.innerHTML = `${icon("robot", "ic")} paladin`;
  initSessions();
  initModels();
  initSettings();
  initSites();
  initWorkspace();

  document.querySelector("#sendBtn")!.addEventListener("click", () => void send(false));
  document.querySelector("#promptInput")!.addEventListener("keydown", (e) => {
    if ((e as KeyboardEvent).key === "Enter" && !(e as KeyboardEvent).shiftKey) {
      e.preventDefault();
      void send(false);
    }
  });
  document.querySelector("#pauseBtn")!.addEventListener("click", () => {
    void api.stopChat(state.sessionId, state.cwd);
  });
  document.querySelector("#sidebarToggle")!.addEventListener("click", () => {
    document.querySelector("#sidebar")!.classList.add("hidden-bar");
    (document.querySelector("#sidebarShow") as HTMLElement).style.display = "";
    trackBounds();
  });
  document.querySelector("#sidebarShow")!.addEventListener("click", () => {
    document.querySelector("#sidebar")!.classList.remove("hidden-bar");
    (document.querySelector("#sidebarShow") as HTMLElement).style.display = "none";
    trackBounds();
  });

  try {
    state.cwd = await api.getWorkspace();
    if (!state.cwd) state.cwd = await api.resolveWorkspace(null);
    (document.querySelector("#workspaceLabel") as HTMLElement).textContent = state.cwd || "no workspace";
  } catch (e) {
    toast(String(e), "error");
  }
  await loadProviders();
  await refreshSessions();
  await refreshExternalVisibility();
  scrollToBottom();
  // Keep the overlay aligned after fonts settle.
  setTimeout(trackBounds, 300);
}

document.addEventListener("DOMContentLoaded", () => {
  void boot();
});
