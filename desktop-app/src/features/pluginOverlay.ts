import { bridge, hasBridge } from "../core/bridge";
import * as api from "../core/api";
import { state } from "../core/state";

// A plugin owns the content of its frame, while Paladin owns the window,
// its title bar, position, size, and close control.
export function openPluginOverlay(
  opened: { id: string; url: string; title: string },
  sessionId: string,
  plugin: string,
  view: string,
  workspace: string,
): void {
  let panelId = opened.id;
  let closed = false;
  let reloading = false;
  let reloadAgain = false;
  const windowEl = document.createElement("section");
  windowEl.className = "plugin-overlay";
  windowEl.setAttribute("aria-label", `${opened.title} plugin`);
  const header = document.createElement("div");
  header.className = "plugin-overlay-header";
  header.tabIndex = 0;
  header.setAttribute("aria-label", "Move with arrow keys; hold Shift to resize");
  const title = document.createElement("span");
  title.textContent = opened.title;
  const close = document.createElement("button");
  close.type = "button";
  close.textContent = "×";
  close.setAttribute("aria-label", `Close ${opened.title}`);
  const actions = document.createElement("div");
  const maximize = document.createElement("button");
  maximize.type = "button";
  maximize.textContent = "□";
  maximize.setAttribute("aria-label", `Expand ${opened.title} canvas`);
  maximize.setAttribute("aria-pressed", "false");
  maximize.addEventListener("click", () => {
    const expanded = windowEl.classList.toggle("is-maximized");
    maximize.setAttribute("aria-pressed", String(expanded));
    maximize.setAttribute("aria-label", `${expanded ? "Restore" : "Expand"} ${opened.title} canvas`);
  });
  actions.append(maximize, close);
  header.append(title, actions);
  const frame = document.createElement("iframe");
  frame.className = "plugin-overlay-frame";
  frame.title = opened.title;
  frame.setAttribute("sandbox", "allow-scripts");
  frame.setAttribute("referrerpolicy", "no-referrer");
  frame.src = opened.url;
  windowEl.append(header, frame);
  document.body.append(windowEl);
  window.dispatchEvent(new Event("paladin:plugin-overlay-changed"));

  const reload = (): void => {
    if (closed || !hasBridge()) return;
    if (reloading) {
      reloadAgain = true;
      return;
    }
    const plugins = bridge().plugins;
    if (!plugins) return;
    reloading = true;
    void plugins
      .prepareReload(panelId)
      .then((ready) => {
        if (!ready || closed) throw new Error("panel closed before reload");
        return plugins.close(panelId);
      })
      .then(() => plugins.open(plugin, view, sessionId, workspace))
      .then((next) => {
        if (closed) {
          void plugins.close(next.id);
          return;
        }
        panelId = next.id;
        frame.src = next.url;
      })
      .catch((error: unknown) => console.error("Could not reload plugin overlay", error))
      .finally(() => {
        reloading = false;
        if (reloadAgain) {
          reloadAgain = false;
          reload();
        }
      });
  };
  const unsubscribe = api.onAgentEvent((event) => {
    if (event.event === "plugin/updated" && plugin.startsWith("creator_")) reload();
  });

  const dispose = (): void => {
    closed = true;
    unsubscribe();
    window.removeEventListener("paladin:session-selected", syncSession);
    window.removeEventListener("paladin:workspace-changed", syncSession);
    windowEl.remove();
    window.dispatchEvent(new Event("paladin:plugin-overlay-changed"));
    if (hasBridge()) void bridge().plugins?.close(panelId);
  };
  const syncSession = (): void => {
    if (state.sessionId !== sessionId || state.cwd !== workspace) dispose();
  };
  window.addEventListener("paladin:session-selected", syncSession);
  window.addEventListener("paladin:workspace-changed", syncSession);
  close.addEventListener("click", dispose);
  header.addEventListener("keydown", (event) => {
    if (windowEl.classList.contains("is-maximized")) return;
    const direction: Record<string, [number, number]> = {
      ArrowLeft: [-1, 0],
      ArrowRight: [1, 0],
      ArrowUp: [0, -1],
      ArrowDown: [0, 1],
    };
    const vector = direction[event.key];
    if (!vector || event.target !== header) return;
    event.preventDefault();
    const rect = windowEl.getBoundingClientRect();
    const [dx, dy] = vector.map((value) => value * 20);
    if (event.shiftKey) {
      windowEl.style.width = `${Math.max(240, Math.min(innerWidth, rect.width + dx))}px`;
      windowEl.style.height = `${Math.max(160, Math.min(innerHeight * 0.9, rect.height + dy))}px`;
    } else {
      windowEl.style.left = `${Math.max(0, Math.min(innerWidth - rect.width, rect.left + dx))}px`;
      windowEl.style.top = `${Math.max(0, Math.min(innerHeight - rect.height, rect.top + dy))}px`;
      windowEl.style.right = "auto";
    }
  });
  header.addEventListener("pointerdown", (event) => {
    if (windowEl.classList.contains("is-maximized")) return;
    if (event.button !== 0 || (event.target as HTMLElement).closest("button")) return;
    const rect = windowEl.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    header.setPointerCapture(event.pointerId);
    const move = (next: PointerEvent): void => {
      const maxX = Math.max(0, innerWidth - windowEl.offsetWidth);
      const maxY = Math.max(0, innerHeight - windowEl.offsetHeight);
      windowEl.style.left = `${Math.max(0, Math.min(maxX, next.clientX - x))}px`;
      windowEl.style.top = `${Math.max(0, Math.min(maxY, next.clientY - y))}px`;
      windowEl.style.right = "auto";
    };
    const end = (): void => {
      header.removeEventListener("pointermove", move);
      header.removeEventListener("pointerup", end);
      header.removeEventListener("pointercancel", end);
    };
    header.addEventListener("pointermove", move);
    header.addEventListener("pointerup", end);
    header.addEventListener("pointercancel", end);
  });
}
