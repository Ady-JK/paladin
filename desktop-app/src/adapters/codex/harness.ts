// External harness selection shares the composer and model picker. The CLI
// transports, catalogs, and permission policy remain in the Go sidecar.
import * as api from "../../core/api";
import { inputEl } from "../../core/dom";
import { CLAUDE, CODEX, CODEX_MODEL_KEY, codexProvider } from "../../core/harness";
import { state } from "../../core/state";
import type { CodexStatus } from "../../core/types";
import { loadJson } from "../../core/storage";
import { toast } from "../../ui/toast";
import { pickModel, restoreModel, syncModelSelects } from "../../features/models";
import { claudeModelFor, loadClaude } from "../claude/harness";

const paladinPlaceholder = inputEl.placeholder;
let installed = false;
let note = "";
export function codexAvailability(): { installed: boolean; note: string } {
  return { installed, note };
}
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
export async function loadCodex(): Promise<boolean> {
  let status: CodexStatus;
  try {
    status = await api.codexStatus(true);
  } catch (error) {
    status = { installed: true, loggedIn: false, models: [], error: String(error) };
  }
  state.codex = status;
  if (status.error) toast(status.error, "error");
  return !status.error && status.models.length > 0;
}
function codexModelFor(preferred?: string): string {
  const models = state.codex?.models ?? [];
  const wanted = preferred ?? (loadJson(CODEX_MODEL_KEY) as string | null | undefined) ?? "";
  return models.find((m) => m.id === wanted)?.id ?? models.find((m) => m.isDefault)?.id ?? models[0]?.id ?? "";
}
export function setHarness(next: "" | "codex" | "claude", model?: string): void {
  const was = state.harness;
  state.harness = next;
  document.body.classList.toggle("harness-codex", next === CODEX);
  document.body.classList.toggle("harness-claude", next === CLAUDE);
  inputEl.dataset.idlePlaceholder =
    next === CODEX
      ? "Ask Codex to build, fix or explain… (@ to tag files)"
      : next === CLAUDE
        ? "Ask Claude Code to build, fix or explain… (@ to tag files, / for Claude commands)"
        : paladinPlaceholder;
  inputEl.placeholder = inputEl.dataset.idlePlaceholder;
  if (next) {
    state.selectedProvider = next === CODEX ? codexProvider().name : CLAUDE;
    state.selectedModel = next === CODEX ? codexModelFor(model) : claudeModelFor(model);
    state.selectedLab = "";
    syncModelSelects();
  } else if (was !== next) {
    restoreModel();
    syncModelSelects();
  }
}
// previewHarness gives the composer a harness session's look (placeholder,
// provider, model) at once, from whatever is cached. A switch must not wait
// for the engine's CLI to list its models; openedHarness finishes the job when
// the catalog is in.
export function previewHarness(harness: string | undefined, model: string): void {
  if (harness !== CODEX && harness !== CLAUDE) return;
  setHarness(harness, model);
  if (!state.selectedModel && model) {
    state.selectedModel = model;
    syncModelSelects();
  }
}
export async function openedHarness(harness: string | undefined, model: string): Promise<void> {
  if (harness !== CODEX && harness !== CLAUDE) {
    setHarness("");
    return;
  }
  const sessionId = state.sessionId;
  if (harness === CODEX && !state.codex) await loadCodex();
  if (harness === CLAUDE) await loadClaude();
  if (state.sessionId !== sessionId) return;
  setHarness(harness, model);
  if (model) pickModel(harness, model);
}
export async function startCodexSession(): Promise<boolean> {
  if (!(await loadCodex())) return false;
  setHarness(CODEX);
  return true;
}
