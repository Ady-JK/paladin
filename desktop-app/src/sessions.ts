// Sessions: list with delegated clicks + debounced search, single-layout history.
import { api } from "./api";
import { dayOf, escapeAttr, escapeHtml, renderText } from "./format";
import { addTurn, scrollToBottom } from "./chat";
import { state } from "./state";
import { toolCard } from "./tools";
import type { HistoryMessage } from "./types";
import { toast } from "./ui";
import { icon, paintIcons } from "./icons";

function groupByDay() {
  const groups = new Map<string, typeof state.sessions>();
  for (const s of state.sessions) {
    const day = dayOf(s.createdAt);
    if (!groups.has(day)) groups.set(day, []);
    groups.get(day)!.push(s);
  }
  return groups;
}

export function renderSessions(): void {
  const q = (document.querySelector("#sessionSearch") as HTMLInputElement).value.trim().toLowerCase();
  const list = document.querySelector("#sessionList") as HTMLElement;
  const sessions = q
    ? state.sessions.filter((s) => (s.title || "untitled").toLowerCase().includes(q))
    : state.sessions;
  if (sessions.length === 0) {
    list.innerHTML = `<p style="color:var(--muted);font-size:12px;padding:8px">no sessions</p>`;
    return;
  }
  const byDay = new Map<string, typeof sessions>();
  for (const s of sessions) {
    const day = dayOf(s.createdAt);
    if (!byDay.has(day)) byDay.set(day, []);
    byDay.get(day)!.push(s);
  }
  let html = "";
  for (const [day, group] of byDay) {
    html += `<div class="day">${escapeHtml(day)}</div>`;
    for (const s of group) {
      const active = s.sessionId === state.sessionId ? " active" : "";
      html += `<div class="session-row${active}" data-session="${escapeAttr(s.sessionId)}">
        <div class="row"><div class="t" style="flex:1">${escapeHtml(s.title || "untitled")}</div>
        <button class="btn danger" data-delete="${escapeAttr(s.sessionId)}" title="Delete" style="padding:4px 8px">${icon("trash")}</button></div>
        <div class="m">${escapeHtml(s.provider || "")} ${escapeHtml(s.model || "")} · ${s.messageCount}</div>
      </div>`;
    }
  }
  list.innerHTML = html;
  paintIcons(list);
}

export async function refreshSessions(): Promise<void> {
  try {
    state.sessions = await api.listSessions(state.cwd, 100);
  } catch {
    state.sessions = [];
  }
  renderSessions();
}

let pendingDelete = "";
export function askDelete(id: string): void {
  pendingDelete = id;
  (document.querySelector("#confirmTitle") as HTMLElement).textContent =
    state.sessions.find((s) => s.sessionId === id)?.title || "session";
  (document.querySelector("#confirmModal") as HTMLElement).classList.add("on");
}

export async function openSession(id: string): Promise<void> {
  try {
    const sess = await api.openSession(id, state.cwd);
    state.sessionId = sess.sessionId;
    state.cwd = sess.cwd;
    if (sess.provider) state.selectedProvider = sess.provider;
    if (sess.model) state.selectedModel = sess.model;
    (document.querySelector("#workspaceLabel") as HTMLElement).textContent = state.cwd || "no workspace";
    const history: HistoryMessage[] = await api.sessionHistory(state.sessionId, state.cwd);
    const box = document.querySelector("#messages") as HTMLElement;
    box.innerHTML = "";
    box.style.display = "none";
    // Boxed so closure assignment doesn't confuse CFA (narrowing a
    // captured `let` across branches collapses to never on this TS).
    const agent: { body: HTMLElement | null } = { body: null };
    const openAgent = (text: string): HTMLElement => {
      const t = addTurn("agent", text);
      agent.body = t.body;
      return t.body;
    };
    for (const m of history) {
      if (m.role === "user") {
        agent.body = null;
        addTurn("user", m.content);
      } else if (m.role === "assistant") {
        const host: HTMLElement | null = agent.body;
        if (host) {
          const el = document.createElement("div");
          el.innerHTML = renderText(m.content);
          host.append(el);
        } else {
          openAgent(m.content);
        }
      } else if (m.role === "tool") {
        const host: HTMLElement = agent.body ?? openAgent("");
        const result = m.content.length > 8000 ? m.content.slice(0, 8000) + "\n…(truncated)" : m.content;
        const row = document.createElement("div");
        row.append(
          toolCard({
            id: m.toolCallId || "",
            name: m.name || "tool",
            arguments: m.arguments || "{}",
            result,
            status: m.status || "ok",
          }).el,
        );
        host.append(row);
      }
    }
    box.style.display = "";
    renderSessions();
    scrollToBottom();
  } catch (e) {
    toast(String(e), "error");
  }
}

export function initSessions(): void {
  let timer = 0;
  (document.querySelector("#sessionSearch") as HTMLInputElement).addEventListener("input", () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(renderSessions, 120);
  });
  document.querySelector("#sessionList")!.addEventListener("click", (e) => {
    const t = e.target as HTMLElement;
    const del = t.closest<HTMLElement>("[data-delete]");
    if (del?.dataset.delete) {
      e.stopPropagation();
      askDelete(del.dataset.delete);
      return;
    }
    const pick = t.closest<HTMLElement>("[data-session]");
    if (pick?.dataset.session) void openSession(pick.dataset.session);
  });
  document.querySelector("#confirmCancel")!.addEventListener("click", () =>
    document.querySelector("#confirmModal")!.classList.remove("on"),
  );
  document.querySelector("#confirmDelete")!.addEventListener("click", async () => {
    const id = pendingDelete;
    pendingDelete = "";
    document.querySelector("#confirmModal")!.classList.remove("on");
    if (!id) return;
    try {
      await api.deleteSession(id, state.cwd);
      if (state.sessionId === id) {
        state.sessionId = "";
        (document.querySelector("#messages") as HTMLElement).innerHTML = "";
      }
      await refreshSessions();
    } catch (e) {
      toast(String(e), "error");
    }
  });
}
