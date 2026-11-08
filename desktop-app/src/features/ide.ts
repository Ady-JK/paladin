// IDE mode reuses the live chat and session state; file buffers remain in
// memory when changing modes or workspaces until explicitly closed.
import { EditorView } from "codemirror";
import { EditorState } from "@codemirror/state";
import { oneDark } from "@codemirror/theme-one-dark";
import { createIDEEditorState, theme } from "./ide/editor";
import { monitorDiskChanges } from "./ide/diskChanges";
import { initIDEChanges } from "./ide/changes";
import { initIDEPalette } from "./ide/palette";
import { initIDESearch } from "./ide/search";
import { initIDEActivity } from "./ide/activity";
import { initIDEProblems } from "./ide/problems";
import {
  bufferDirty as dirty,
  bufferKey as keyFor,
  inWorkspace,
  LayoutStore,
  restoreBuffers,
  snapshotBuffers,
  validateLayout,
  type IDEBuffer,
} from "./ide/layoutStore";
import { wireResize } from "./ide/layout";
import * as api from "../core/api";
import { $ } from "../core/dom";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { FsList, IDEExplorerSettings } from "../core/types";
import { icon } from "../icons";
import { newSession, openSession } from "./sessions";
import { initIDESessions, syncIDESessions } from "./ide/sessions";
import { activateIDEFile, setIDETerminalMode } from "./sidePane";
import { openNewSession } from "./sessionDialog";

const buffers = new Map<string, IDEBuffer>();
const diskStatuses = new Map<string, string>();
const expanded = new Set<string>();
const listings = new Map<string, FsList>();
let cwd = "";
let active = "";
let enabled = false;
let generation = 0;
let workspaceEpoch = 0;
let editor: EditorView;
let showing = "";
let confirmReload = "";
let openRequest = 0;
let browserActive = false;
let explorerVisible = true;
let agentVisible = true;
let restoring = false;
let restoreInteracted = false;
let layoutTimer = 0;
const layoutSessions = new Map<string, string>();
const layoutBlocked = new Set<string>();
const layoutStore = new LayoutStore((error) => status(`Could not save IDE layout: ${String(error)}`, true));
let explorerSettings: IDEExplorerSettings = { hideIgnored: false, hideDotFolders: false };
const basename = (path: string): string =>
  path
    .replace(/[\\/]+$/, "")
    .split(/[\\/]/)
    .pop() || path;

function status(message: string, error = false): void {
  const el = $("#ideStatus");
  if (!error && cwd && layoutBlocked.has(cwd)) return;
  el.textContent = message;
  el.classList.toggle("is-error", error);
}

function layoutSnapshot(root: string): api.IDELayout {
  const files = snapshotBuffers(root, buffers);
  const dock = document.querySelector<HTMLElement>("#ideTerminalDock");
  return {
    files,
    activePath: active ? (JSON.parse(active) as [string, string])[1] : "",
    expanded: [...expanded],
    explorerVisible,
    agentVisible,
    terminalHeight: Number.parseInt(dock?.style.height ?? "", 10) || 0,
    sessionId: state.cwd === root ? state.sessionId : (layoutSessions.get(root) ?? ""),
  };
}

function scheduleLayout(): void {
  if (!enabled || !cwd || restoring || layoutBlocked.has(cwd)) return;
  window.clearTimeout(layoutTimer);
  layoutTimer = window.setTimeout(() => layoutStore.save(cwd, layoutSnapshot(cwd)), 500);
}

async function restoreLayout(root: string, epoch: number): Promise<void> {
  restoring = true;
  restoreInteracted = false;
  window.clearTimeout(layoutTimer);
  const beforeOpen = openRequest;
  try {
    const layout = await api.ideLayoutGet(root);
    if (!enabled || cwd !== root || workspaceEpoch !== epoch || !layout) return;
    validateLayout(root, layout);
    if (!restoreInteracted) {
      explorerVisible = layout.explorerVisible !== false;
      agentVisible = layout.agentVisible !== false;
      syncPaneVisibility();
    }
    if (
      !restoreInteracted &&
      Number.isFinite(layout.terminalHeight) &&
      layout.terminalHeight >= 0 &&
      layout.terminalHeight <= 2000
    ) {
      const dock = document.querySelector<HTMLElement>("#ideTerminalDock");
      if (dock) dock.style.height = layout.terminalHeight >= 100 ? `${layout.terminalHeight}px` : "";
    }
    if (Array.isArray(layout.expanded)) {
      for (const path of layout.expanded.slice(0, 500)) {
        if (typeof path === "string" && inWorkspace(root, path)) {
          expanded.add(path);
          void loadDirectory(path);
        }
      }
    }
    await restoreBuffers(root, layout, buffers, () => enabled && cwd === root && workspaceEpoch === epoch, editorState);
    if (!enabled || cwd !== root || workspaceEpoch !== epoch) return;
    if (
      !restoreInteracted &&
      openRequest === beforeOpen &&
      typeof layout.activePath === "string" &&
      buffers.has(keyFor(root, layout.activePath))
    ) {
      active = keyFor(root, layout.activePath);
      showing = "";
    }
    paintEditor();
    if (!restoreInteracted && !state.sessionId && typeof layout.sessionId === "string" && layout.sessionId) {
      const sessions = await api.listSessions(root, 0).catch(() => []);
      if (
        enabled &&
        cwd === root &&
        workspaceEpoch === epoch &&
        !restoreInteracted &&
        !state.sessionId &&
        sessions.some(
          (session) =>
            session.sessionId === layout.sessionId && (session.cwd === root || session.worktree?.origin === root),
        )
      )
        void openSession(layout.sessionId);
    }
  } catch (error) {
    layoutBlocked.add(root);
    if (cwd === root && workspaceEpoch === epoch)
      status(`Could not restore IDE layout; layout saving is paused: ${String(error)}`, true);
  } finally {
    if (cwd === root && workspaceEpoch === epoch) {
      restoring = false;
      if (restoreInteracted) scheduleLayout();
    }
  }
}

function treeHTML(path: string, level: number): string {
  const list = listings.get(path);
  if (!list) return "";
  return [
    ...list.directories.map((item) => {
      const open = expanded.has(item.path);
      const row = `<button type="button" class="ide-tree-row ide-folder" data-ide-dir="${escapeAttr(item.path)}" aria-expanded="${open}" style="--depth:${level}" title="${escapeAttr(item.path)}">${icon(open ? "chevron-down" : "chevron-right", "h-3 w-3")}${icon(open ? "folder-open" : "folder", "h-3.5 w-3.5")}<span>${escapeHtml(item.name)}</span></button>`;
      return row + (open ? treeHTML(item.path, level + 1) : "");
    }),
    ...list.files.map(
      (item) =>
        `<button type="button" class="ide-tree-row ide-file${active === keyFor(cwd, item.path) ? " is-active" : ""}" data-ide-file="${escapeAttr(item.path)}" style="--depth:${level}" title="${escapeAttr(item.path)}">${icon("file-code", "h-3.5 w-3.5")}<span>${escapeHtml(item.name)}</span></button>`,
    ),
  ].join("");
}

function paintTree(): void {
  $("#ideFolderName").textContent = basename(cwd) || "Workspace";
  $("#ideFolderName").title = cwd;
  $("#ideTree").innerHTML = treeHTML(cwd, 0);
}

async function loadDirectory(path: string): Promise<void> {
  const epoch = generation;
  try {
    const list = await api.ideList(cwd, path, explorerSettings);
    if (epoch !== generation) return;
    listings.set(path, list);
    paintTree();
  } catch (error) {
    if (epoch === generation) status(`Could not open folder: ${String(error)}`, true);
  }
}

function paintTabs(): void {
  const tabs = [...buffers.entries()].filter(([, buffer]) => buffer.cwd === cwd);
  $("#ideFileTabs").innerHTML = tabs
    .map(
      ([key, buffer]) =>
        `<div class="ide-tab${key === active && !browserActive ? " is-active" : ""}"><button type="button" class="ide-tab-select" data-ide-tab="${escapeAttr(key)}" title="${escapeAttr(buffer.path)}">${escapeHtml(basename(buffer.path))}${dirty(buffer) ? ' <span aria-label="Unsaved">●</span>' : ""}${diskStatuses.has(buffer.path) ? ' <span aria-label="Changed on disk" title="File changed on disk">!</span>' : ""}</button><button type="button" class="ide-tab-close" data-ide-close="${escapeAttr(key)}" aria-label="Close ${escapeAttr(basename(buffer.path))}">×</button></div>`,
    )
    .join("");
}

function editorState(path: string, content: string): EditorState {
  return createIDEEditorState(path, content, (update) => {
    const buffer = buffers.get(showing);
    if (!buffer) return;
    buffer.editorState = update.state;
    if (!update.docChanged) return;
    const wasDirty = dirty(buffer);
    buffer.content = update.state.doc.toString();
    confirmReload = "";
    $("#ideReloadBtn").textContent = "Reload";
    if (wasDirty !== dirty(buffer)) paintTabs();
    $<HTMLButtonElement>("#ideSaveBtn").disabled = !dirty(buffer) || buffer.saving;
    scheduleLayout();
    window.dispatchEvent(new Event("paladin:ide-buffer-changed"));
  });
}

function paintEditor(): void {
  const buffer = buffers.get(active);
  const hasFile = !!buffer && buffer.cwd === cwd;
  $("#ideEditorContent").hidden = browserActive;
  $("#ideEmpty").hidden = hasFile || browserActive;
  $("#ideEditorWrap").hidden = !hasFile || browserActive;
  $("#idePath").hidden = browserActive;
  $<HTMLButtonElement>("#ideSaveBtn").disabled = browserActive || !hasFile || !dirty(buffer) || buffer.saving;
  $<HTMLButtonElement>("#ideReloadBtn").disabled = browserActive || !hasFile;
  if (hasFile) {
    if (showing !== active) {
      showing = active;
      editor.setState(buffer.editorState);
      editor.dispatch({
        effects: theme.reconfigure(document.documentElement.classList.contains("dark") ? oneDark : []),
      });
    }
    $("#idePath").textContent = buffer.path;
  } else {
    showing = "";
    $("#idePath").textContent = "";
  }
  paintTabs();
  paintTree();
  window.dispatchEvent(new Event("paladin:ide-file-changed"));
  scheduleLayout();
}

function goToPosition(line: number, column = 1): void {
  if (!buffers.get(active) || !Number.isFinite(line) || !Number.isFinite(column)) return;
  const row = editor.state.doc.line(Math.max(1, Math.min(Math.trunc(line), editor.state.doc.lines)));
  editor.dispatch({
    selection: { anchor: Math.min(row.to, row.from + Math.max(0, Math.trunc(column) - 1)) },
    scrollIntoView: true,
  });
  editor.focus();
}

async function openFile(path: string, reload = false): Promise<void> {
  activateIDEFile();
  const root = cwd;
  const key = keyFor(root, path);
  const request = ++openRequest;
  if (!reload) {
    confirmReload = "";
    $("#ideReloadBtn").textContent = "Reload";
  }
  if (!reload && buffers.has(key)) {
    active = key;
    paintEditor();
    return;
  }
  const previous = buffers.get(key);
  const previousContent = previous?.content;
  const previousVersion = previous?.version;
  try {
    const file = await api.ideReadFile(root, path);
    if (cwd !== root || request !== openRequest) return;
    if (
      reload &&
      previous &&
      (buffers.get(key) !== previous ||
        previous.saving ||
        previous.content !== previousContent ||
        previous.version !== previousVersion)
    ) {
      status("File changed while reloading; your edits were kept.", true);
      return;
    }
    const content = file.content.replace(/\r\n/g, "\n");
    buffers.set(key, {
      cwd: root,
      path: file.path,
      content,
      saved: content,
      lineEnding: file.content.includes("\r\n") ? "\r\n" : "\n",
      editorState: editorState(file.path, content),
      version: file.version,
      saving: false,
    });
    active = key;
    showing = "";
    status("");
    paintEditor();
  } catch (error) {
    status(`Could not open file: ${String(error)}`, true);
  }
}

async function saveFile(): Promise<void> {
  const buffer = buffers.get(active);
  if (!buffer || !dirty(buffer) || buffer.saving) return;
  buffer.saving = true;
  const content = buffer.content;
  paintTabs();
  $<HTMLButtonElement>("#ideSaveBtn").disabled = true;
  try {
    const diskContent = buffer.lineEnding === "\r\n" ? content.replace(/\n/g, "\r\n") : content;
    const saved = await api.ideSaveFile(buffer.cwd, buffer.path, diskContent, buffer.version);
    buffer.saved = content;
    buffer.version = saved.version;
    window.dispatchEvent(new Event("paladin:ide-file-changed"));
    scheduleLayout();
    status(dirty(buffer) ? "Saved; newer edits are unsaved." : "Saved");
  } catch (error) {
    status(`Could not save: ${String(error)}`, true);
  } finally {
    buffer.saving = false;
    paintTabs();
    if (buffers.get(active) === buffer) $<HTMLButtonElement>("#ideSaveBtn").disabled = !dirty(buffer);
  }
}

function updateExplorerControls(disabled: boolean): void {
  const ignored = $<HTMLInputElement>("#ideHideIgnored");
  const dots = $<HTMLInputElement>("#ideHideDotFolders");
  ignored.checked = explorerSettings.hideIgnored;
  dots.checked = explorerSettings.hideDotFolders;
  ignored.disabled = disabled;
  dots.disabled = disabled;
}

function refreshExplorer(): void {
  generation++;
  listings.clear();
  paintTree();
  for (const path of [cwd, ...expanded]) void loadDirectory(path);
}

async function saveExplorerSettings(): Promise<void> {
  const root = cwd;
  const epoch = generation;
  const previous = explorerSettings;
  const next = {
    hideIgnored: $<HTMLInputElement>("#ideHideIgnored").checked,
    hideDotFolders: $<HTMLInputElement>("#ideHideDotFolders").checked,
  };
  explorerSettings = next;
  updateExplorerControls(true);
  try {
    await api.ideExplorerSettingsSet(root, next);
    if (cwd !== root) return;
    if (generation !== epoch) {
      void loadExplorerSettings(root, generation);
      return;
    }
    explorerSettings = next;
    status("");
    updateExplorerControls(false);
    refreshExplorer();
  } catch (error) {
    if (cwd !== root || generation !== epoch) return;
    explorerSettings = previous;
    status(`Could not save Explorer settings: ${String(error)}`, true);
  } finally {
    if (cwd === root && generation === epoch) updateExplorerControls(false);
  }
}

async function loadExplorerSettings(root: string, epoch: number): Promise<void> {
  try {
    const settings = await api.ideExplorerSettingsGet(root);
    if (cwd !== root || generation !== epoch) return;
    explorerSettings = settings;
    updateExplorerControls(false);
    void loadDirectory(root);
  } catch (error) {
    if (cwd !== root || generation !== epoch) return;
    updateExplorerControls(false);
    status(`Could not load Explorer settings: ${String(error)}`, true);
    void loadDirectory(root);
  }
}

function syncWorkspace(): void {
  const root = state.cwd;
  if (!enabled || !root || root === cwd) return;
  window.clearTimeout(layoutTimer);
  if (cwd && !restoring && !layoutBlocked.has(cwd)) layoutStore.save(cwd, layoutSnapshot(cwd));
  cwd = root;
  workspaceEpoch++;
  layoutSessions.set(root, state.sessionId);
  confirmReload = "";
  $("#ideReloadBtn").textContent = "Reload";
  generation++;
  listings.clear();
  expanded.clear();
  explorerSettings = { hideIgnored: false, hideDotFolders: false };
  updateExplorerControls(true);
  active = [...buffers.entries()].find(([, buffer]) => buffer.cwd === root)?.[0] ?? "";
  status("");
  paintEditor();
  void loadExplorerSettings(root, generation);
  void restoreLayout(root, workspaceEpoch);
}

function syncPaneVisibility(): void {
  $("#agentSplit").classList.toggle("ide-agent-hidden", enabled && !agentVisible);
  $(".ide-explorer").hidden = !explorerVisible;
  $("#ideExplorerResize").hidden = !explorerVisible;
  $("#ideChatResize").hidden = !enabled || !agentVisible;
  for (const [id, visible, name] of [
    ["#ideExplorerToggle", explorerVisible, "explorer"],
    ["#ideAgentToggle", agentVisible, "agent"],
  ] as const) {
    const button = $(id);
    const label = `${visible ? "Hide" : "Show"} ${name}`;
    button.setAttribute("aria-label", label);
    button.setAttribute("title", label);
    button.setAttribute("aria-pressed", String(visible));
  }
  window.dispatchEvent(new Event("resize"));
}

function setEnabled(on: boolean): void {
  enabled = on;
  $("#agentSplit").classList.toggle("ide-mode", on);
  $("#ideWorkspace").hidden = !on;
  $("#ideChatResize").hidden = !on;
  $("#ideChatHead").hidden = !on;
  $("#ideModeBtn").setAttribute("aria-pressed", String(on));
  syncPaneVisibility();
  setIDETerminalMode(on);
  syncIDESessions(on);
  if (on) {
    syncWorkspace();
  }
  window.dispatchEvent(new Event("resize"));
}

export function initIDE(): void {
  const root = document.createElement("section");
  root.id = "ideWorkspace";
  root.className = "ide-workspace";
  root.hidden = true;
  root.setAttribute("aria-label", "Workspace editor");
  root.innerHTML = `<aside class="ide-explorer"><div class="ide-explorer-head"><span>EXPLORER</span><button id="ideSearchBtn" type="button" title="Search workspace" aria-label="Search workspace">${icon("search", "h-4 w-4")}</button><button id="ideChooseFolder" type="button" title="Choose folder" aria-label="Choose folder">${icon("folder-open", "h-4 w-4")}</button><button id="ideRefresh" type="button" title="Refresh files" aria-label="Refresh files">${icon("repeat", "h-4 w-4")}</button></div><div class="ide-root"><span>${icon("chevron-down", "h-3 w-3")}</span><strong id="ideFolderName"></strong></div><div class="ide-explorer-filters"><label><input id="ideHideIgnored" type="checkbox" disabled> Hide Git-ignored items</label><label><input id="ideHideDotFolders" type="checkbox" disabled> Hide dot folders</label></div><div id="ideTree" class="ide-tree"></div></aside><div id="ideExplorerResize" class="ide-resize" role="separator" tabindex="0" aria-label="Resize explorer" aria-orientation="vertical"></div><section class="ide-editor-area"><div class="ide-editor-bar"><div id="ideTabs" class="ide-tabs"><div id="ideFileTabs"></div><div id="ideBrowserTabs"></div></div><button id="ideActivityToggle" type="button" aria-expanded="false">Activity</button><button id="ideExplorerToggle" type="button">Explorer</button><button id="ideAgentToggle" type="button">Agent</button><button id="ideChangesToggle" type="button">Changes</button><button id="ideProblemsToggle" type="button">Problems</button><button id="ideTerminalToggle" type="button" title="Toggle terminal">Terminal</button><button id="ideReloadBtn" type="button" title="Discard changes and reload" disabled>Reload</button><button id="ideSaveBtn" type="button" disabled>Save</button><button id="ideCloseBtn" type="button" title="Exit IDE mode" aria-label="Exit IDE mode">×</button></div><div id="idePath" class="ide-path"></div><div id="ideEditorContent" class="ide-editor-content"><div id="ideEmpty" class="ide-empty"><span>${icon("code", "h-9 w-9")}</span><strong>Open a file to start editing</strong><span>Select a file in Explorer</span></div><div id="ideEditorWrap" class="ide-editor-wrap" hidden><div id="ideEditor"></div></div><aside id="ideChangesPanel" class="ide-changes-panel" aria-label="Session changes" hidden></aside><aside id="ideProblemsPanel" class="ide-problems-panel" aria-label="Problems" hidden></aside></div><div id="ideBrowserArea" class="ide-browser-area" hidden></div><div id="ideStatus" class="ide-status" role="status"></div></section>`;
  const chatResize = document.createElement("div");
  chatResize.id = "ideChatResize";
  chatResize.className = "ide-resize";
  chatResize.hidden = true;
  chatResize.tabIndex = 0;
  chatResize.setAttribute("role", "separator");
  chatResize.setAttribute("aria-label", "Resize chat");
  chatResize.setAttribute("aria-orientation", "vertical");
  $("#agentSplit").prepend(root, chatResize);
  wireResize($("#ideExplorerResize"), root, "--ide-explorer-width", false, 140, 400);
  wireResize(chatResize, $("#agentSplit"), "--ide-chat-width", true, 260, 700);
  editor = new EditorView({ parent: $("#ideEditor"), state: EditorState.create({ doc: "" }) });
  monitorDiskChanges(
    () => (enabled && !browserActive ? buffers.get(active) : undefined),
    () => (enabled ? [...buffers.values()].filter((buffer) => buffer.cwd === cwd) : []),
    (statuses) => {
      const next = new Map(statuses.filter((item) => item.state !== "same").map((item) => [item.path, item.state]));
      if (JSON.stringify([...next]) !== JSON.stringify([...diskStatuses])) {
        diskStatuses.clear();
        for (const [path, value] of next) diskStatuses.set(path, value);
        paintTabs();
      }
    },
    () => $("#ideReloadBtn").click(),
  );
  const chatHead = document.createElement("div");
  chatHead.id = "ideChatHead";
  chatHead.className = "ide-chat-head";
  chatHead.hidden = true;
  chatHead.innerHTML = `<strong>Agent</strong><button id="ideShowSessions" type="button">Sessions</button><button id="ideNewChat" type="button" title="New chat" aria-label="New chat">+</button>`;
  $("#agentMain").prepend(chatHead);
  initIDESessions();
  initIDEChanges(activateIDEFile);
  initIDEActivity();
  initIDEProblems({
    current: () => (enabled && !browserActive ? buffers.get(active) : undefined),
    openEditor: activateIDEFile,
    goTo: goToPosition,
  });
  initIDEPalette({ enabled: () => enabled, openFile: (path) => void openFile(path), goToLine: goToPosition });
  initIDESearch(
    () => enabled,
    (path, line) => {
      void openFile(path).then(() => {
        const buffer = buffers.get(active);
        if (!enabled || !buffer || active !== keyFor(cwd, path)) return;
        goToPosition(line);
      });
    },
  );
  $("#ideModeBtn").addEventListener("click", () => {
    setEnabled(!enabled);
  });
  $("#ideExplorerToggle").addEventListener("click", () => {
    explorerVisible = !explorerVisible;
    syncPaneVisibility();
    scheduleLayout();
  });
  $("#ideAgentToggle").addEventListener("click", () => {
    agentVisible = !agentVisible;
    syncPaneVisibility();
    scheduleLayout();
  });
  window.addEventListener("paladin:ide-show-agent", () => {
    if (!enabled) return;
    agentVisible = true;
    syncPaneVisibility();
    scheduleLayout();
  });
  $("#ideCloseBtn").addEventListener("click", () => setEnabled(false));
  $("#ideRefresh").addEventListener("click", () => {
    listings.clear();
    expanded.clear();
    void loadDirectory(cwd);
  });
  $("#ideChooseFolder").addEventListener("click", () => void openNewSession({ chooseFolder: true }));
  $("#ideHideIgnored").addEventListener("change", () => void saveExplorerSettings());
  $("#ideHideDotFolders").addEventListener("change", () => void saveExplorerSettings());
  $("#ideTree").addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const folder = target.closest<HTMLElement>("[data-ide-dir]")?.dataset.ideDir;
    const file = target.closest<HTMLElement>("[data-ide-file]")?.dataset.ideFile;
    if (file) void openFile(file);
    else if (folder) {
      if (expanded.delete(folder)) paintTree();
      else {
        expanded.add(folder);
        if (listings.has(folder)) paintTree();
        else void loadDirectory(folder);
      }
      scheduleLayout();
    }
  });
  $("#ideTabs").addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const key = target.closest<HTMLElement>("[data-ide-tab]")?.dataset.ideTab;
    const close = target.closest<HTMLElement>("[data-ide-close]")?.dataset.ideClose;
    if (key) {
      activateIDEFile();
      confirmReload = "";
      $("#ideReloadBtn").textContent = "Reload";
      active = key;
      paintEditor();
    }
    if (close) {
      const buffer = buffers.get(close);
      if (buffer && (dirty(buffer) || buffer.saving)) {
        status("Save or reload this file before closing it.", true);
        return;
      }
      buffers.delete(close);
      if (active === close) active = [...buffers.entries()].find(([, item]) => item.cwd === cwd)?.[0] ?? "";
      paintEditor();
    }
  });
  window.addEventListener("paladin:ide-browser-active", (event) => {
    browserActive = (event as CustomEvent<boolean>).detail;
    paintEditor();
  });
  document.addEventListener("keydown", (event) => {
    if (enabled && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
      event.preventDefault();
      void saveFile();
    }
  });
  $("#ideSaveBtn").addEventListener("click", () => void saveFile());
  $("#ideReloadBtn").addEventListener("click", () => {
    const buffer = buffers.get(active);
    if (!buffer || buffer.saving) return;
    if (dirty(buffer) && confirmReload !== active) {
      confirmReload = active;
      $("#ideReloadBtn").textContent = "Confirm discard";
      status("Click Confirm discard to discard your edits and reload this file.", true);
      return;
    }
    confirmReload = "";
    $("#ideReloadBtn").textContent = "Reload";
    void openFile(buffer.path, true);
  });
  $("#ideNewChat").addEventListener("click", () => newSession());
  window.addEventListener("paladin:workspace-changed", () => {
    syncWorkspace();
    syncIDESessions(enabled);
  });
  window.addEventListener("paladin:sessions-changed", () => {
    if (cwd && state.cwd === cwd && layoutSessions.get(cwd) !== state.sessionId) {
      layoutSessions.set(cwd, state.sessionId);
      scheduleLayout();
    }
  });
  window.addEventListener("paladin:ide-layout-changed", scheduleLayout);
  document.addEventListener("pointerdown", () => {
    if (restoring) restoreInteracted = true;
  });
  new MutationObserver(() => {
    if (showing)
      editor.dispatch({
        effects: theme.reconfigure(document.documentElement.classList.contains("dark") ? oneDark : []),
      });
  }).observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  window.addEventListener("beforeunload", (event) => {
    if ([...buffers.values()].some(dirty)) {
      event.preventDefault();
      event.returnValue = "";
    }
  });
}
