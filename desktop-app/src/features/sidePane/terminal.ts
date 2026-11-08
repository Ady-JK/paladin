import { errorText } from "./utils";
import * as api from "../../core/api";
import { escapeHtml } from "../../core/format";
import { icon } from "../../icons";
import type { TabSet, TerminalTab } from "./types";

type PendingEvents = { data: string[]; exitCode?: number; truncated: boolean };
const pendingTerminalData = new Map<string, PendingEvents>();
// At most 16 Ki UTF-16 code units fit under the host's 64 KiB UTF-8 cap.
const maxPendingInput = 1 << 14;

function pendingEvents(id: string): PendingEvents {
  let pending = pendingTerminalData.get(id);
  if (!pending) {
    if (pendingTerminalData.size >= 32) pendingTerminalData.delete(pendingTerminalData.keys().next().value!);
    pending = { data: [], truncated: false };
    pendingTerminalData.set(id, pending);
  }
  return pending;
}

function terminalExited(tab: TerminalTab, code?: number): void {
  tab.term?.write(`\r\n[terminal exited${code ? `: ${code}` : ""}]\r\n`);
  tab.terminalId = "";
  tab.pendingInput = "";
  tab.closed = true;
}

export function terminalEvent(raw: unknown, sets: Map<string, TabSet>): void {
  if (!raw || typeof raw !== "object") return;
  const e = raw as { event?: string; sessionId?: string; terminalId?: string; data?: string; exitCode?: number };
  if (!e.event?.startsWith("terminal/") || !e.terminalId || !e.sessionId) return;
  const findTab = (set: TabSet | undefined): TerminalTab | undefined =>
    set?.tabs.find((t): t is TerminalTab => t.kind === "terminal" && t.terminalId === e.terminalId);
  let tab = findTab(sets.get(e.sessionId));
  if (!tab) {
    for (const set of sets.values()) {
      tab = findTab(set);
      if (tab) break;
    }
  }
  if (e.event === "terminal/data" && e.data) {
    if (!tab) {
      const pending = pendingEvents(e.terminalId);
      if (pending.data.length < 32) pending.data.push(e.data);
      else pending.truncated = true;
      return;
    }
    tab.term?.write(Uint8Array.from(atob(e.data), (char) => char.charCodeAt(0)));
  } else if (e.event === "terminal/exit" && tab) {
    terminalExited(tab, e.exitCode);
  } else if (e.event === "terminal/exit") {
    pendingEvents(e.terminalId).exitCode = e.exitCode ?? 0;
  }
}

export function newTerminalTab(sessionId: string, cwd: string, key: string): TerminalTab {
  const body = document.createElement("div");
  body.className = "pane-terminal";
  body.hidden = true;
  body.innerHTML = `<div class="terminal-heading"><span>${icon("terminal", "h-3.5 w-3.5")} Terminal</span><small>${escapeHtml(cwd)}</small></div><div class="terminal-surface" aria-label="Interactive terminal"></div>`;
  const tab: TerminalTab = {
    kind: "terminal",
    key,
    sessionId,
    cwd,
    title: "Terminal",
    body,
    term: null,
    fit: null,
    pendingText: [],
    pendingInput: "",
    terminalId: "",
    starting: null,
    closed: false,
  };
  void Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]).then(
    ([{ Terminal }, { FitAddon }]) => {
      if (tab.closed) return;
      const term = new Terminal({
        cursorBlink: true,
        fontFamily: '"JetBrains Mono", ui-monospace, monospace',
        fontSize: 12,
        scrollback: 5000,
        theme: { background: "#17171a", foreground: "#e9e9ed", cursor: "#8faafa" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      tab.term = term;
      tab.fit = fit;
      term.onData((data) => {
        if (tab.terminalId)
          void api.terminalWrite(tab.sessionId, tab.terminalId, data).catch((error: unknown) => {
            term.write(`\r\nTerminal input could not be sent: ${errorText(error)}\r\n`);
          });
        else if (!tab.closed && tab.pendingInput.length + data.length <= maxPendingInput) tab.pendingInput += data;
        else if (!tab.closed) term.write("\r\n[Terminal input buffer is full; wait for the shell to start.]\r\n");
      });
      term.onResize(({ cols, rows }) => {
        if (tab.terminalId) void api.terminalResize(tab.sessionId, tab.terminalId, cols, rows).catch(() => undefined);
      });
      for (const text of tab.pendingText) term.write(text);
      tab.pendingText = [];
      if (!tab.body.hidden) activateTerminal(tab);
    },
    (error) => {
      body.querySelector<HTMLElement>(".terminal-surface")!.textContent =
        `Terminal could not load: ${errorText(error)}`;
    },
  );
  return tab;
}

export function activateTerminal(tab: TerminalTab): void {
  if (!tab.term || !tab.fit) return;
  const term = tab.term;
  const fit = tab.fit;
  const surface = tab.body.querySelector<HTMLElement>(".terminal-surface")!;
  if (!surface.querySelector(".xterm")) term.open(surface);
  if (!tab.body.hidden) fit.fit();
  if (tab.starting || tab.terminalId || tab.closed) return;
  tab.starting = api
    .terminalStart(tab.sessionId, tab.cwd, Math.max(term.cols, 20), Math.max(term.rows, 5))
    .then((id) => {
      if (tab.closed) {
        pendingTerminalData.delete(id);
        void api.terminalClose(tab.sessionId, id).catch(() => undefined);
        return;
      }
      tab.terminalId = id;
      const pending = pendingTerminalData.get(id);
      for (const chunk of pending?.data ?? []) {
        term.write(Uint8Array.from(atob(chunk), (char) => char.charCodeAt(0)));
      }
      pendingTerminalData.delete(id);
      if (pending?.truncated) term.write("\r\n[Some startup output was omitted.]\r\n");
      if (pending?.exitCode !== undefined) {
        terminalExited(tab, pending.exitCode);
        return;
      }
      if (tab.pendingInput) {
        const input = tab.pendingInput;
        tab.pendingInput = "";
        void api.terminalWrite(tab.sessionId, id, input).catch((error: unknown) => {
          term.write(`\r\nTerminal input could not be sent: ${errorText(error)}\r\n`);
        });
      }
      if (!tab.body.hidden) term.focus();
    })
    .catch((error) => term.write(`\r\nTerminal could not start: ${errorText(error)}\r\n`))
    .finally(() => {
      tab.starting = null;
    });
}
