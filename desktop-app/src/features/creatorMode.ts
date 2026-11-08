import * as api from "../core/api";
import { $, workspaceInputEl, workspaceLabelEl } from "../core/dom";
import { shortPath } from "../core/format";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { newSession, openSession } from "./sessions";
import { refreshSkillsCache } from "./skills";

const previousKey = "paladin.creator.previous";
let enabled = false;
let creatorRoot = "";

type Previous = { cwd: string; sessionId: string };

function previous(): Previous | null {
  try {
    const value = JSON.parse(localStorage.getItem(previousKey) ?? "null") as Partial<Previous> | null;
    return value && typeof value.cwd === "string" && typeof value.sessionId === "string"
      ? { cwd: value.cwd, sessionId: value.sessionId }
      : null;
  } catch {
    return null;
  }
}

function paint(button: HTMLButtonElement): void {
  button.setAttribute("aria-pressed", String(enabled));
  button.title = enabled ? "Exit Creator mode" : "Open Creator mode";
  button.textContent = enabled ? "Creator · On" : "Creator";
}

function setWorkspace(path: string): void {
  state.cwd = path;
  refreshSkillsCache();
  workspaceInputEl.value = path;
  workspaceLabelEl.textContent = shortPath(path);
  newSession();
}

async function toggle(button: HTMLButtonElement): Promise<void> {
  if (button.disabled) return;
  button.disabled = true;
  try {
    if (!enabled) {
      localStorage.setItem(previousKey, JSON.stringify({ cwd: state.cwd, sessionId: state.sessionId }));
      await api.creatorModeSet(true);
      try {
        const folder = await api.creatorWorkspace();
        creatorRoot = folder;
        await api.setWorkspace(folder);
        setWorkspace(folder);
        enabled = true;
        if ($("#ideModeBtn").getAttribute("aria-pressed") !== "true") $("#ideModeBtn").click();
      } catch (error) {
        await api.creatorModeSet(false);
        throw error;
      }
    } else {
      await api.creatorModeSet(false);
      enabled = false;
      const prior = previous();
      if (prior?.cwd) {
        await api.setWorkspace(prior.cwd);
        setWorkspace(prior.cwd);
        if (prior.sessionId) await openSession(prior.sessionId);
      }
      localStorage.removeItem(previousKey);
    }
    paint(button);
  } catch (error) {
    paint(button);
    toast(`Could not switch Creator mode: ${String(error)}`, "error");
  } finally {
    button.disabled = false;
  }
}

export function initCreatorMode(): void {
  const button = $<HTMLButtonElement>("#creatorModeBtn");
  button.addEventListener("click", () => void toggle(button));
  window.addEventListener("paladin:workspace-changed", () => {
    if (!enabled || !creatorRoot || !state.cwd || state.cwd === creatorRoot) return;
    setWorkspace(creatorRoot);
    void api
      .setWorkspace(creatorRoot)
      .catch((error: unknown) => toast(`Could not restore Creator workspace: ${String(error)}`, "error"));
    toast("Exit Creator mode to switch workspaces", "error");
  });
  void api
    .creatorModeGet()
    .then(async (value) => {
      enabled = value === true;
      if (enabled) creatorRoot = await api.creatorWorkspace();
      refreshSkillsCache();
      paint(button);
    })
    .catch(() => paint(button));
}
