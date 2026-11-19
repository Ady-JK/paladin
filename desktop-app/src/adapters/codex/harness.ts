// harness.ts lets a session run on an external agent: today the user's own
// Codex CLI. A Codex session uses the same chat, tool cards and approval
// cards as any other; only its model list (Codex's), its start button in the
// sidebar and a small badge differ. The core fixes a session's harness when
// it is created, so this module decides it at that moment and then follows
// whatever session is open.

import * as api from "../../core/api";
import { inputEl } from "../../core/dom";
import { CODEX, CODEX_MODEL_KEY, codexProvider } from "../../core/harness";
import { state } from "../../core/state";
import type { CodexStatus } from "../../core/types";
import { loadJson } from "../../core/storage";
import { toast } from "../../ui/toast";
import { syncPermChip } from "../../features/permissions";
import { pickModel, restoreModel, syncModelSelects } from "../../features/models";

// The composer's hint, which a Codex session words for Codex.
const paladinPlaceholder = inputEl.placeholder;
const CODEX_PLACEHOLDER = "Ask Codex to build, fix or explain… (@ to tag files)";

// installed: the new-session dialog offers Codex only when the CLI is on this
// machine. note is why it is not.
let installed = false;
let note = "";

// codexAvailability is what the new-session dialog shows for its Codex choice.
export function codexAvailability(): { installed: boolean; note: string } {
  return { installed, note };
}

// refreshCodexAvailability checks, without starting anything, whether Codex is
// installed. It runs at launch and each time the new-session dialog opens, so a
// Codex installed or updated while Paladin runs is picked up without a restart.
export async function refreshCodexAvailability(): Promise<void> {
  try {
    const status = await api.codexStatus(false);
    installed = status.installed;
    note = installed ? "" : (status.error ?? "");
  } catch (error) {
    installed = false;
    note = String(error);
  }
}

// loadCodex asks the CLI who is signed in and which models it offers.
async function loadCodex(): Promise<boolean> {
  let status: CodexStatus;
  try {
    status = await api.codexStatus(true);
  } catch (error) {
    status = { installed: true, loggedIn: false, models: [], permissions: [], error: String(error) };
  }
  state.codex = status;
  if (status.error) toast(status.error, "error");
  return !status.error && status.models.length > 0;
}

// codexModelFor picks the model a new Codex session starts on: the last one
// chosen, else Codex's own default.
function codexModelFor(preferred?: string): string {
  const models = state.codex?.models ?? [];
  const wanted = preferred ?? (loadJson(CODEX_MODEL_KEY) as string | null | undefined) ?? "";
  return models.find((m) => m.id === wanted)?.id ?? models.find((m) => m.isDefault)?.id ?? models[0]?.id ?? "";
}

// setHarness switches the picker between Paladin's providers and Codex's
// models. Leaving Codex restores the model the user had chosen before.
export function setHarness(next: "" | "codex", model?: string): void {
  const was = state.harness;
  state.harness = next;
  document.body.classList.toggle("harness-codex", next === CODEX);
  // live.ts restores the idle hint from here after a turn.
  inputEl.dataset.idlePlaceholder = next === CODEX ? CODEX_PLACEHOLDER : paladinPlaceholder;
  inputEl.placeholder = inputEl.dataset.idlePlaceholder;
  syncPermChip();
  if (next === CODEX) {
    state.selectedProvider = codexProvider().name;
    state.selectedModel = codexModelFor(model);
    state.selectedLab = "";
    syncModelSelects();
  } else if (was !== next) {
    restoreModel();
    syncModelSelects();
  }
}

// openedHarness follows the session that was just opened: a Codex session
// brings its own model list and model.
export async function openedHarness(harness: string | undefined, model: string): Promise<void> {
  if (harness !== CODEX) {
    setHarness("");
    return;
  }
  if (!state.codex) await loadCodex();
  setHarness(CODEX, model);
  if (model) pickModel(CODEX, model);
}

// startCodexSession prepares a new, empty Codex session; the core creates
// it on the first message, with the model picked here.
export async function startCodexSession(): Promise<boolean> {
  if (!(await loadCodex())) return false;
  setHarness(CODEX);
  return true;
}
