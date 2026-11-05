// pluginPanels.ts owns the Plugins menu in the side-pane header. A
// manifest view opens as a tab whose iframe is sandboxed; the tab body
// and the bridge grant live in main, so the renderer only chooses which
// plugin and view to show.

import * as api from "../core/api";
import { bridge, hasBridge } from "../core/bridge";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { PluginView, TurnEvent } from "../core/types";
import { icon } from "../icons";
import { closeMenu, isMenuOpen, toggleMenu } from "../ui/anim";
import { toast } from "../ui/toast";
import {
  closePluginTab,
  markPluginUpdated,
  openPluginTab,
  pluginTabList,
  preparePluginPanel,
  type PluginTab,
} from "./sidePane";

let views: PluginView[] = [];
let errors: string[] = [];
let local = true;
let button: HTMLButtonElement | null = null;
let menu: HTMLElement | null = null;
let listEl: HTMLElement | null = null;

const BADGE: Record<string, string> = {
  invokeTool: "runs tools",
  invokeToolDestructive: "changes files",
  readFile: "reads files",
  events: "watches chat",
  openExternal: "opens links",
};

function badges(view: PluginView): string {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const permission of view.permissions) {
    const label = BADGE[permission];
    if (!label || seen.has(label)) continue;
    seen.add(label);
    const danger = permission === "invokeToolDestructive" ? " is-danger" : "";
    out.push(`<span class="plugin-badge${danger}">${escapeHtml(label)}</span>`);
  }
  return out.join("");
}

function viewsHtml(): string {
  if (!local) {
    return `<p class="pane-menu-note">Plugin UI runs local plugin files. Switch to a local core to use it.</p>`;
  }
  if (!views.length) {
    const why = errors.length
      ? `<p class="pane-menu-note">${errors.map((e) => escapeHtml(e)).join("<br>")}</p>`
      : `<p class="pane-menu-note">No plugin panels. Add a <code>views</code> entry to a plugin manifest.</p>`;
    return why;
  }
  const rows = views
    .map(
      (view) =>
        `<button type="button" class="plugin-row" data-plugin="${escapeAttr(view.plugin)}" data-view="${escapeAttr(view.view)}"${
          view.destructive ? ' data-destructive="true"' : ""
        }>` +
        `<span class="plugin-row-ico">${icon("sl-plug", "h-3.5 w-3.5")}</span>` +
        `<span class="plugin-row-text"><span class="plugin-row-title">${escapeHtml(view.title)}</span>` +
        `<span class="plugin-row-plugin">${escapeHtml(view.plugin)}</span></span>` +
        `<span class="plugin-row-badges">${badges(view)}</span></button>`,
    )
    .join("");
  const errorRows = errors.length
    ? `<p class="pane-menu-note">${errors.map((e) => escapeHtml(e)).join("<br>")}</p>`
    : "";
  return rows + errorRows;
}

function paint(): void {
  if (!listEl) return;
  listEl.innerHTML = viewsHtml();
}

async function refresh(): Promise<void> {
  try {
    const connection = await bridge().core?.connection();
    local = !connection || connection.mode === "local";
  } catch {
    local = true;
  }
  if (!local) {
    views = [];
    errors = [];
    paint();
    return;
  }
  try {
    const result = await api.pluginViews();
    views = result.views ?? [];
    errors = result.errors ?? [];
  } catch (error) {
    views = [];
    errors = [String(error)];
  }
  paint();
}

async function openFor(plugin: string, view: string, sessionId: string, workspace: string): Promise<void> {
  const plugins = hasBridge() ? bridge().plugins : undefined;
  if (!plugins) {
    toast("Plugin panels need the desktop app", "error");
    return;
  }
  if (!sessionId) {
    toast("Send a message first: plugin panels belong to a conversation", "error");
    return;
  }
  if (!workspace) {
    toast("Pick a workspace first", "error");
    return;
  }
  preparePluginPanel(sessionId);
  try {
    const opened = await plugins.open(plugin, view, sessionId, workspace);
    openPluginTab({
      sessionId,
      panelId: opened.id,
      plugin,
      view,
      title: opened.title,
      url: opened.url,
      workspace,
    });
  } catch (error) {
    toast(`Could not open the plugin panel: ${String(error)}`, "error");
  }
}

async function openFromMenu(plugin: string, view: string): Promise<void> {
  close();
  await openFor(plugin, view, state.sessionId, state.cwd);
}

function reopen(tab: PluginTab): void {
  closePluginPanel(tab);
  void openFor(tab.plugin, tab.view, tab.sessionId, tab.workspace);
}

function closePluginPanel(tab: PluginTab): void {
  // closePluginTab also releases the main-process grant.
  closePluginTab(tab.panelId);
}

function close(): void {
  if (menu && isMenuOpen(menu)) closeMenu(menu);
  button?.setAttribute("aria-expanded", "false");
}

function build(): void {
  const actions =
    document.querySelector<HTMLElement>("#headerStatusActions") ?? document.querySelector<HTMLElement>("#paneActions");
  if (!actions || button) return;
  button = document.createElement("button");
  button.id = "panePluginsBtn";
  button.type = "button";
  button.className = "pane-btn";
  button.title = "Plugin panels";
  button.setAttribute("aria-label", "Plugin panels");
  button.setAttribute("aria-haspopup", "menu");
  button.setAttribute("aria-expanded", "false");
  button.innerHTML = icon("sl-plug", "h-4 w-4");
  menu = document.createElement("div");
  menu.id = "pluginsMenu";
  menu.className = "pane-menu plugins-menu";
  menu.setAttribute("role", "menu");
  menu.classList.add("hidden");
  const head = document.createElement("div");
  head.className = "plugins-menu-head";
  head.innerHTML = `<span>Plugin panels</span><button type="button" class="plugins-refresh" title="Refresh plugin list" aria-label="Refresh plugin list">${icon(
    "repeat",
    "h-3 w-3",
  )}</button>`;
  listEl = document.createElement("div");
  listEl.className = "plugins-list";
  menu.append(head, listEl);
  const controls = actions.querySelector<HTMLElement>("#windowMinimizeBtn")?.parentElement;
  actions.insertBefore(button, controls ?? null);
  actions.append(menu);

  button.addEventListener("click", (event) => {
    event.stopPropagation();
    if (!menu) return;
    const open = toggleMenu(menu);
    button?.setAttribute("aria-expanded", String(open));
    if (open) void refresh();
  });
  head.querySelector(".plugins-refresh")!.addEventListener("click", (event) => {
    event.stopPropagation();
    void refresh();
  });
  listEl.addEventListener("click", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>(".plugin-row");
    if (!row) return;
    void openFromMenu(row.dataset.plugin ?? "", row.dataset.view ?? "");
  });
  document.addEventListener("mousedown", (event) => {
    if (!menu || !isMenuOpen(menu)) return;
    const target = event.target as Node;
    if (!menu.contains(target) && !button?.contains(target)) close();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") close();
  });
}

// initPluginPanels builds the menu and keeps it in step with reloads.
export function initPluginPanels(): void {
  build();
  void refresh();
  api.onAgentEvent((event: TurnEvent) => {
    if (event.event !== "plugin/updated") return;
    void refresh();
    for (const tab of pluginTabList()) {
      markPluginUpdated(tab, () => reopen(tab));
    }
  });
}
