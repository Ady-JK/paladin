// sessionActions.ts holds session operations shared by the `/` palette
// and the sidebar row menu: fork (copy the conversation into a new
// session and open it) and rename.

import * as api from "../core/api";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { openSession, refreshSessions, renderSessions } from "./sessions";

function cwdOf(id: string): string | null {
  return state.sessions.find((s) => s.sessionId === id)?.cwd || (id === state.sessionId ? state.cwd : "") || null;
}

export async function forkSessionById(id: string): Promise<void> {
  try {
    const info = await api.forkSession(id, cwdOf(id));
    await refreshSessions();
    await openSession(info.sessionId);
    toast(`Forked into “${info.title || info.sessionId.slice(0, 8)}”`);
  } catch (error) {
    toast(`Fork failed: ${String(error)}`, "error");
  }
}

export async function renameSessionById(id: string, title: string): Promise<void> {
  const s = state.sessions.find((x) => x.sessionId === id);
  const before = s?.title;
  const clean = title.replace(/\s+/g, " ").trim();
  if (s && clean) {
    s.title = clean;
    renderSessions();
  }
  try {
    await api.renameSession(id, cwdOf(id), clean);
    await refreshSessions();
    toast(clean ? `Renamed to “${clean}”` : "Title reset");
  } catch (error) {
    if (s && before !== undefined) {
      s.title = before;
      renderSessions();
    }
    toast(`Rename failed: ${String(error)}`, "error");
  }
}
