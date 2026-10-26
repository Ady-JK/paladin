// Sites: Agent + Gemini + ChatGPT (+ custom), backed by WebContentsViews.
// Views stay alive when hidden; only the selected one's bounds are set.
import { views } from "./api";
import { state } from "./state";
import type { ChatEntry } from "./types";
import { toast } from "./ui";

const BUILT_IN: ChatEntry[] = [
  { id: "gemini", title: "Gemini", url: "https://gemini.google.com", builtin: true },
  { id: "chatgpt", title: "ChatGPT", url: "https://chatgpt.com", builtin: true },
];

function partitionFor(id: string): string {
  return `persist:paladin-${id}`;
}

export function allPages(): ChatEntry[] {
  return [...BUILT_IN, ...state.customSites.map((s, i) => ({ id: `custom-${i}`, title: s.name, url: s.url, builtin: false }))];
}

export function currentPage(): ChatEntry | null {
  return allPages().find((p) => p.id === state.chatPage) ?? null;
}

function setTab(id: string): void {
  document.querySelectorAll("#tabs .tab").forEach((el) => {
    el.classList.toggle("active", (el as HTMLElement).dataset.tab === id || (id.startsWith("custom-") && (el as HTMLElement).dataset.tab === id));
  });
  const host = document.querySelector("#externalHost") as HTMLElement;
  const chatView = document.querySelector("#chatView") as HTMLElement;
  const composer = document.querySelector("#composer") as HTMLElement;
  const reload = document.querySelector("#pageReloadBtn") as HTMLElement;
  const external = id !== "agent";
  host.classList.toggle("on", external);
  chatView.style.display = external ? "none" : "";
  composer.style.display = external ? "none" : "";
  reload.style.display = external ? "" : "none";
}

export function externalRect(): { x: number; y: number; width: number; height: number } {
  const host = document.querySelector("#externalHost") as HTMLElement;
  const r = host.getBoundingClientRect();
  return { x: r.x, y: r.y, width: r.width, height: r.height };
}

// One settle move per layout change (no 60fps storm; no sidebar animation).
export function trackBounds(): void {
  requestAnimationFrame(() => {
    if (state.sideView !== "chat") return;
    const rect = externalRect();
    const key = `${rect.x},${rect.y},${rect.width},${rect.height}`;
    if (key !== state.lastSentRect) {
      state.lastSentRect = key;
      views.setBounds(rect).catch(() => undefined);
    }
  });
}

export async function refreshExternalVisibility(): Promise<void> {
  const page = currentPage();
  if (state.sideView === "chat" && page) {
    setTab(page.id);
    try {
      await views.ensure(page.id, page.url, partitionFor(page.id));
      await views.show(page.id);
      state.lastSentRect = "";
      trackBounds();
    } catch (e) {
      toast(String(e), "error");
    }
    return;
  }
  setTab("agent");
  try {
    await views.show("agent");
  } catch {
    // Ignore.
  }
}

let dropTimer = 0;
function armDrop(): void {
  window.clearTimeout(dropTimer);
  dropTimer = window.setTimeout(() => {
    if (state.sideView === "chat") return;
    // Reclaim the hidden page's RAM; logins persist in the partition.
    const page = currentPage();
    if (page && !page.builtin) return; // keep custom views; cheap enough
    if (page) views.drop(page.id).catch(() => undefined);
  }, 60_000);
}

export async function selectView(view: "agent" | string): Promise<void> {
  if (view === "agent") {
    state.sideView = "agent";
    armDrop();
  } else {
    state.sideView = "chat";
    window.clearTimeout(dropTimer);
    state.chatPage = view;
  }
  await refreshExternalVisibility();
}

export function initSites(): void {
  document.querySelector("#tabs")!.addEventListener("click", (e) => {
    const t = (e.target as HTMLElement).closest<HTMLElement>("[data-tab]");
    if (!t?.dataset.tab) return;
    void selectView(t.dataset.tab === "agent" ? "agent" : t.dataset.tab);
  });
  document.querySelector("#pageReloadBtn")!.addEventListener("click", () => {
    const page = currentPage();
    if (page && state.sideView === "chat") views.reload(page.id).catch(() => undefined);
  });
  window.addEventListener("resize", trackBounds);
}
