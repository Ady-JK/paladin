// sessionDialog.ts owns the new-session dialog: a single native-like
// folder browser (breadcrumbs, search, persisted Hidden toggle,
// single-select list with double-click to open). Start creates the
// session in the selected folder — or the current one — using the
// already-selected model: the default on a fresh launch, or the model
// of the session the dialog was opened from.

import * as api from "../core/api";
import {
  $,
  newSessionCrumbsEl,
  newSessionFolderSearchEl,
  newSessionListEl,
  newSessionModelHintEl,
  newSessionPathEl,
  newSessionShowHiddenEl,
  paintIcons,
  sessionModalEl,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { bare, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import type { FsList } from "../core/types";
import { closeModal, openModal } from "../ui/anim";
import { toast } from "../ui/toast";
import { pickModel } from "./models";
import { newSession, refreshSessions } from "./sessions";
import { codexAvailability, loadCodex, refreshCodexAvailability, setHarness } from "../adapters/codex/harness";
import { claudeAvailability, loadClaude, refreshClaudeAvailability } from "../adapters/claude/harness";
import { beginStart } from "./startProgress";
import { crumbsHTML, filterEntries, getShowHidden, rowsHTML, setShowHidden, type PickerSelection } from "./picker";
import { syncMcp } from "./mcp";

let currentList: FsList | null = null;
let selection: PickerSelection | null = null;
let query = "";
let showHidden = false;
let browseGeneration = 0;
let choosingFolder = false;

// modelHint mirrors the composer label so the dialog states which model
// the new session will use before it starts. The model is Paladin's: a Codex
// session picks one of Codex's own once it has started.
function modelHint(): string {
  const short = state.selectedModel ? bare(splitModel(state.selectedModel).short) : "";
  const provider = bare(state.selectedProvider);
  const label = short ? (provider ? `${provider}/${short}` : short) : provider || "default";
  return `model: ${label}`;
}

// paintFolder repaints the browser from the cached list plus the current
// filter, selection, and toggle. The pending workspace tracks the
// highlight, falling back to the browsed directory, so Start always acts
// on exactly what the path line shows.
function paintFolder(): void {
  if (!currentList) {
    return;
  }
  state.pendingWorkspace = selection?.path ?? currentList.currentPath;
  const visible = filterEntries(currentList, showHidden, query);
  newSessionCrumbsEl.innerHTML = crumbsHTML(currentList.currentPath);
  newSessionPathEl.textContent = state.pendingWorkspace;
  newSessionPathEl.title = state.pendingWorkspace;
  newSessionModelHintEl.textContent = modelHint();
  newSessionModelHintEl.title =
    [state.selectedProvider, state.selectedModel].filter(Boolean).join(" / ") || "default model";
  newSessionListEl.innerHTML = rowsHTML({ ...currentList, directories: visible.directories, files: [] }, selection, {
    filesEnabled: false,
  });
  paintIcons(sessionModalEl);

  newSessionCrumbsEl.querySelectorAll<HTMLButtonElement>("button[data-fb-crumb]").forEach((button) => {
    button.addEventListener("click", () => {
      void browseNewSession(button.dataset.fbCrumb ?? "");
    });
  });
  newSessionListEl.querySelectorAll<HTMLElement>("[data-fb-open]").forEach((el) => {
    el.addEventListener("click", (event) => {
      event.stopPropagation();
      void browseNewSession(el.dataset.fbOpen ?? "");
    });
    el.addEventListener("dblclick", (event) => {
      event.stopPropagation();
    });
  });
  newSessionListEl.querySelectorAll<HTMLButtonElement>("button[data-fb-select]").forEach((button) => {
    const path = button.dataset.fbSelect ?? "";
    button.addEventListener("click", () => {
      selection = { path, kind: "folder" };
      paintFolder();
      button.focus();
    });
    button.addEventListener("dblclick", () => {
      void browseNewSession(path);
    });
  });
}

async function browseNewSession(path?: string): Promise<void> {
  const target = path ?? state.pendingWorkspace ?? state.cwd ?? null;

  const request = ++browseGeneration;
  const startButton = $<HTMLButtonElement>("#sessionStartBtn");
  startButton.disabled = true;
  $<HTMLButtonElement>("#sessionStartCodexBtn").disabled = true;
  $<HTMLButtonElement>("#sessionStartClaudeBtn").disabled = true;
  try {
    const list = await api.fsList(target);
    if (request !== browseGeneration) return;
    currentList = list;
    // Browsing to a parent must not leave a previously selected child armed.
    if (selection && selection.path !== list.currentPath) {
      selection = null;
    }
    paintFolder();
  } catch (error) {
    if (request === browseGeneration) toast(String(error), "error");
  } finally {
    if (request === browseGeneration) {
      startButton.disabled = false;
      paintEngines();
    }
  }
}

// paintEngines shows the engine choice that follows the folder: Paladin, or Codex
// when its CLI is on this machine. Without it the Codex choice stays, greyed,
// saying why, so it never just disappears.
function paintEngines(): void {
  const codex = $<HTMLButtonElement>("#sessionStartCodexBtn");
  const { installed, note } = codexAvailability();
  codex.hidden = choosingFolder;
  $("#sessionStartLabel").hidden = choosingFolder;
  // Both starts wait for the folder list, as the Paladin one always has.
  codex.disabled = !installed || $<HTMLButtonElement>("#sessionStartBtn").disabled;
  codex.title = installed
    ? "Run the session on your Codex CLI, with Codex's models"
    : note || "Codex was not found on this Mac: install the Codex app or CLI to use it";
  const claude = $<HTMLButtonElement>("#sessionStartClaudeBtn");
  const availability = claudeAvailability();
  claude.hidden = choosingFolder;
  claude.disabled = !availability.installed || $<HTMLButtonElement>("#sessionStartBtn").disabled;
  claude.title = availability.installed
    ? "Run with your Claude Code CLI and subscription"
    : availability.note || "Install Claude Code to use it";
}

export async function openNewSession(options?: { chooseFolder?: boolean }): Promise<void> {
  choosingFolder = !!options?.chooseFolder;
  $("#sessionDialogTitle").textContent = options?.chooseFolder ? "Open folder" : "New Session";
  $("#sessionStartBtn").textContent = options?.chooseFolder ? "Open folder" : "Paladin";
  $("#sessionStartBtn").title = options?.chooseFolder
    ? "Use this folder"
    : "Run the session on Paladin's own agent and your model providers";
  newSessionModelHintEl.hidden = !!options?.chooseFolder;
  state.pendingWorkspace = state.cwd;
  selection = null;
  query = "";
  currentList = null;
  newSessionCrumbsEl.replaceChildren();
  newSessionListEl.textContent = "Loading…";
  newSessionPathEl.textContent = state.cwd;
  showHidden = getShowHidden();
  newSessionFolderSearchEl.value = "";
  newSessionShowHiddenEl.checked = showHidden;
  openModal(sessionModalEl);
  // Re-check Codex each time: it may have been installed or updated since launch.
  void refreshCodexAvailability().then(paintEngines);
  void refreshClaudeAvailability().then(paintEngines);
  paintEngines();

  await browseNewSession();
}

export function closeNewSession(): void {
  browseGeneration++;
  closeModal(sessionModalEl);
}

export type SessionEngine = "" | "codex" | "claude";

const ENGINE_NAMES = { codex: "Codex", claude: "Claude Code" } as const;

// launchGen numbers launches so a slow one that was overtaken stays quiet.
let launchGen = 0;

// launchSession starts a blank session in cwd on one engine: the same steps
// the dialog's three buttons take, so a sidebar shortcut cannot drift from
// them. beforeNew runs once the engine is ready, just before the view switches.
//
// Codex and Claude Code launch their CLI to read models and sign-in, which
// takes a moment, and the app stays usable meanwhile (a status pill says so).
// So nothing on screen changes until the engine is ready, and a launch that
// the user has moved on from (opened another chat, started another session)
// quietly drops instead of switching the view under them.
async function launchSession(cwd: string, engine: SessionEngine, beforeNew?: () => void): Promise<void> {
  const gen = ++launchGen;
  const from = state.sessionId;
  const target = cwd.trim() || state.cwd;
  if (engine) {
    const end = beginStart(ENGINE_NAMES[engine], target);
    try {
      const ready = engine === "codex" ? await loadCodex() : await loadClaude(target);
      if (!ready || gen !== launchGen || state.sessionId !== from) return;
    } finally {
      end();
    }
  }
  if (cwd.trim()) {
    state.cwd = target;
    workspaceLabelEl.textContent = shortPath(state.cwd);
    workspaceInputEl.value = state.cwd;
    syncMcp();
  }

  if (!engine) pickModel(state.selectedProvider, state.selectedModel);
  beforeNew?.();
  newSession();
  if (engine) setHarness(engine);
  // The workspace may have changed folders: refetch the global list so
  // sessions from other folders stay visible.
  void refreshSessions();
}

// startSessionIn is the sidebar's "+" on a folder: a new session there, on the
// chosen engine, without opening the dialog.
export function startSessionIn(cwd: string, engine: SessionEngine): Promise<void> {
  return launchSession(cwd, engine);
}

async function startNewSession(engine: SessionEngine = ""): Promise<void> {
  if (choosingFolder && state.pendingWorkspace.trim() === state.cwd) {
    closeNewSession();
    return;
  }
  // A CLI start can take a while: leave the dialog at once so the status pill
  // behind it is visible, instead of a dialog that looks stuck.
  if (engine) closeNewSession();
  return launchSession(state.pendingWorkspace, engine, engine ? undefined : closeNewSession);
}

// initSessionDialog wires the new-session dialog controls.
export function initSessionDialog(): void {
  $("#sessionCloseBtn").addEventListener("click", closeNewSession);
  $("#sessionCancelBtn").addEventListener("click", closeNewSession);
  $("#sessionStartBtn").addEventListener("click", () => void startNewSession(""));
  $("#sessionStartCodexBtn").addEventListener("click", () => void startNewSession("codex"));
  $("#sessionStartClaudeBtn").addEventListener("click", () => void startNewSession("claude"));

  newSessionFolderSearchEl.addEventListener("input", () => {
    query = newSessionFolderSearchEl.value;
    paintFolder();
  });

  newSessionShowHiddenEl.addEventListener("change", () => {
    showHidden = newSessionShowHiddenEl.checked;
    setShowHidden(showHidden);
    paintFolder();
  });

  sessionModalEl.addEventListener("click", (event) => {
    if (event.target === sessionModalEl) {
      closeNewSession();
    }
  });
}
