// harness.ts turns what the Codex CLI reported into the provider shape the
// model picker already understands, so a Codex session picks its model with
// the same menu as any other session.

import { state } from "./state";
import type { CodexStatus, ProviderInfo } from "./types";

// CODEX is the harness name and the provider name Codex sessions use.
export const CODEX = "codex";

// CODEX_MODEL_KEY remembers the Codex model the user chose last.
export const CODEX_MODEL_KEY = "paladin.codexModel";
export const CLAUDE = "claude";
export const CLAUDE_MODEL_KEY = "paladin.claudeModel";

let built: { from: CodexStatus; provider: ProviderInfo } | null = null;

// codexProvider is the single provider a Codex session offers: its models.
// The object is stable until the status changes, which the picker's search
// cache relies on.
export function codexProvider(): ProviderInfo {
  const status = state.codex;
  if (built && status && built.from === status) return built.provider;
  const models = (status?.models ?? []).map((model) => model.id);
  const provider: ProviderInfo = {
    name: CODEX,
    apiType: CODEX,
    apiUrl: "",
    model: models[0] ?? "",
    models,
    default: true,
    hasKey: !!status?.loggedIn,
  };
  if (status) built = { from: status, provider };
  return provider;
}

// activeProviders are the providers the picker may choose from right now.
const noProviders: ProviderInfo[] = [];
export function activeProviders(): ProviderInfo[] {
  if (state.harness === CLAUDE) {
    if (!state.claude) return noProviders;
    return [
      {
        name: CLAUDE,
        apiType: CLAUDE,
        apiUrl: "",
        model: state.selectedModel,
        models: state.claude.models.map((m) => m.id),
        default: true,
        hasKey: state.claude.loggedIn,
      },
    ];
  }
  if (state.harness !== CODEX) return state.providers;
  if (!state.codex) return noProviders;
  return [codexProvider()];
}
