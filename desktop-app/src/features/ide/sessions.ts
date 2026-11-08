import * as api from "../../core/api";
import { $ } from "../../core/dom";
import { escapeAttr, escapeHtml } from "../../core/format";
import { state } from "../../core/state";
import type { SessionInfo } from "../../core/types";
import { newSession, openSession } from "../sessions";

let enabled = false;
let generation = 0;
let timer = 0;
let workspace = "";
let sessions: SessionInfo[] = [];
let showAll = false;
let loaded = false;

function paint(): void {
  const list = $("#ideSessionList");
  const show = enabled && !state.sessionId;
  list.hidden = !show;
  $("#chatView").hidden = show;
  if (!show) return;
  const rows = sessions.filter(
    (session) => !session.subagent && (session.cwd === workspace || session.worktree?.origin === workspace),
  );
  list.innerHTML =
    `<div class="ide-sessions-title">Sessions</div>` +
    (rows.length
      ? rows
          .map(
            (session) =>
              `<button type="button" class="ide-session-row" data-ide-session="${escapeAttr(session.sessionId)}"><span class="ide-session-dot"></span><span class="ide-session-text"><strong>${escapeHtml(session.title || "Untitled chat")}</strong><small>${session.messageCount} ${session.messageCount === 1 ? "message" : "messages"}</small></span></button>`,
          )
          .join("")
      : `<p class="ide-sessions-empty">${loaded ? "No sessions in this workspace yet. Start a conversation below." : "Loading sessions…"}</p>`) +
    (!showAll && sessions.length >= 100
      ? `<button type="button" class="ide-sessions-more" data-ide-more>Show all sessions</button>`
      : "");
}

async function load(): Promise<void> {
  if (!enabled || state.sessionId || !state.cwd) return;
  const root = state.cwd;
  const request = ++generation;
  try {
    const items = await api.listSessions(root, showAll ? 0 : 100);
    if (!enabled || state.cwd !== root || request !== generation) return;
    workspace = root;
    sessions = items ?? [];
    loaded = true;
    paint();
  } catch (error) {
    if (enabled && state.cwd === root && request === generation) {
      $("#ideSessionList").textContent = `Could not load sessions: ${String(error)}`;
    }
  }
}

export function syncIDESessions(on: boolean): void {
  enabled = on;
  if (state.cwd !== workspace) {
    workspace = state.cwd;
    sessions = [];
    showAll = false;
    loaded = false;
  }
  generation++;
  paint();
  if (enabled && !state.sessionId) void load();
}

export function initIDESessions(): void {
  const list = document.createElement("div");
  list.id = "ideSessionList";
  list.className = "ide-session-list";
  list.hidden = true;
  $("#chatView").before(list);
  list.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("[data-ide-more]")) {
      showAll = true;
      void load();
      return;
    }
    const id = target.closest<HTMLElement>("[data-ide-session]")?.dataset.ideSession;
    if (id) void openSession(id);
  });
  $("#ideShowSessions").addEventListener("click", () => newSession());
  window.addEventListener("paladin:sessions-changed", () => {
    if (!enabled || state.sessionId) return;
    window.clearTimeout(timer);
    timer = window.setTimeout(() => void load(), 100);
  });
}
