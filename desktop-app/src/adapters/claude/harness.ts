// Claude-specific catalogs and availability. Execution stays in the Go core.
import * as api from "../../core/api";
import { state } from "../../core/state";
import { loadJson } from "../../core/storage";
import { CLAUDE, CLAUDE_MODEL_KEY } from "../../core/harness";
import { setHarness } from "../codex/harness";
import { toast } from "../../ui/toast";

let installed = false;
let note = "";
export function claudeAvailability(): { installed: boolean; note: string } {
  return { installed, note };
}
export async function refreshClaudeAvailability(): Promise<void> {
  try {
    const status = await api.claudeStatus(false);
    installed = status.installed;
    note = status.error ?? "";
  } catch (error) {
    installed = false;
    note = String(error);
  }
}
// loadClaude reads Claude Code's status and workspace commands. cwd is the
// folder the session is for; it defaults to the current one. Nothing here
// changes what is on screen, so a caller can load for a folder it has not
// switched to yet.
export async function loadClaude(cwd: string = state.cwd): Promise<boolean> {
  const sessionId = state.sessionId;
  const shownCwd = state.cwd;
  try {
    state.claude = await api.claudeStatus(true);
    if (state.claude.error) toast(state.claude.error, "error");
    if (!state.claude.loggedIn || state.claude.error) return false;
    const commands = await api.claudeCommands(cwd);
    if (state.sessionId !== sessionId || state.cwd !== shownCwd) return false;
    state.claudeCommands = commands;
    return state.claude.models.length > 0;
  } catch (error) {
    toast(String(error), "error");
    return false;
  }
}
export function claudeModelFor(preferred?: string): string {
  const models = state.claude?.models ?? [];
  const wanted = preferred ?? (loadJson(CLAUDE_MODEL_KEY) as string | null) ?? "";
  return models.find((m) => m.id === wanted)?.id ?? models.find((m) => m.isDefault)?.id ?? models[0]?.id ?? "";
}
export async function startClaudeSession(): Promise<boolean> {
  if (!(await loadClaude())) return false;
  setHarness(CLAUDE);
  return true;
}
