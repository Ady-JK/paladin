// undo.ts is "Revert to here": a user message's undo button previews
// the rewind (turns, files restored, files changed since), reverts
// files plus conversation, and leaves a banner with Redo until the next
// message makes it final. /undo reverts the last message.

import * as api from "../core/api";
import { inputEl, messagesEl, sendBtn } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { RevertInfo, RevertPreview } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { clearLanded, isBusy } from "./live";
import { loadHistory, refreshSessions } from "./sessions";

// pending is each session's revert awaiting the next message or Redo.
const pending = new Map<string, RevertInfo>();
// prefill is the composer text a revert put back, cleared on Redo.
let prefill = "";
let pop: HTMLElement | null = null;
let banner: HTMLElement | null = null;

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

function bannerEl(): HTMLElement {
  if (banner) return banner;
  const el = document.createElement("div");
  el.id = "revertBanner";
  el.className = "revert-banner hidden";
  el.setAttribute("role", "status");
  const footer = sendBtn.closest("footer");
  (footer?.firstElementChild ?? footer)?.prepend(el);
  el.addEventListener("click", (event) => {
    if ((event.target as HTMLElement).closest("#redoBtn")) void redo();
  });
  banner = el;
  return el;
}

// paintRevert shows the visible session's pending revert, if any.
export function paintRevert(): void {
  const el = bannerEl();
  const info = state.sessionId ? pending.get(state.sessionId) : undefined;
  if (!info) {
    el.classList.add("hidden");
    el.innerHTML = "";
    return;
  }
  const parts: string[] = [];
  if (!info.snapshots) {
    parts.push("files here are not tracked");
  } else if (info.restored.length === 0 && info.skipped.length === 0) {
    parts.push("no file changes to undo");
  } else {
    parts.push(`${plural(info.restored.length, "file")} restored`);
  }
  if (info.skipped.length) {
    parts.push(
      `<span class="revert-skipped" title="Changed since or could not be restored; left as is:\n${escapeHtml(info.skipped.join("\n"))}">${info.skipped.length} skipped</span>`,
    );
  }
  el.innerHTML =
    `<span class="revert-ico">${icon("tl-undo", "h-3.5 w-3.5")}</span>` +
    `<span class="revert-title">Conversation rewound</span>` +
    `<span class="revert-detail">${parts.join(" · ")}</span>` +
    `<span class="revert-hint">Sending a message makes it final</span>` +
    `<button type="button" id="redoBtn" class="revert-redo">Redo</button>`;
  el.classList.remove("hidden");
}

// setRevert records a session's pending revert (from openSession).
export function setRevert(sessionId: string, info: RevertInfo | null | undefined): void {
  if (info) pending.set(sessionId, info);
  else pending.delete(sessionId);
  if (sessionId === state.sessionId) paintRevert();
}

// noteSent drops the banner: the core commits the revert on the turn.
export function noteSent(sessionId: string): void {
  prefill = "";
  setRevert(sessionId, null);
}

function closePop(): void {
  pop?.remove();
  pop = null;
}

async function reload(sessionId: string): Promise<void> {
  clearLanded(sessionId);
  if (state.sessionId === sessionId) await loadHistory(sessionId);
  void refreshSessions();
}

// resolveIndex finds a user row's transcript index. History rows carry
// it; rows sent live learn it by matching user rows from the end.
async function resolveIndex(btn: HTMLElement): Promise<number | null> {
  if (btn.dataset.revert) return Number(btn.dataset.revert);
  const rows = Array.from(messagesEl.querySelectorAll<HTMLElement>("[data-revert]")).filter(
    (b) => !b.closest(".turn-queued"),
  );
  const fromEnd = rows.length - 1 - rows.indexOf(btn);
  const history = await api.sessionHistory(state.sessionId, null);
  const indexed = history.filter((m) => m.role === "user" && m.index !== undefined);
  const row = indexed[indexed.length - 1 - fromEnd];
  return row?.index ?? null;
}

function guard(sessionId: string): boolean {
  if (!sessionId) return false;
  if (isBusy(sessionId)) {
    toast("Wait for the running turn to finish (or stop it) before reverting", "error");
    return false;
  }
  return true;
}

function previewHtml(pv: RevertPreview): string {
  const list = (cls: string, files: string[]) =>
    `<ul class="${cls}">${files.map((f) => `<li title="${escapeHtml(f)}">${escapeHtml(f)}</li>`).join("")}</ul>`;
  let body = `<p class="revert-sub">Rewinds ${plural(pv.turns || 1, "turn")}: this message and everything after it.</p>`;
  if (!pv.snapshots) {
    body += `<p class="revert-note">Files in this folder are not tracked, so only the conversation is rewound.</p>`;
  } else if (pv.files.length === 0 && pv.conflicts.length === 0) {
    body += `<p class="revert-note">No file changes to undo.</p>`;
  }
  if (pv.files.length) {
    body +=
      `<div class="revert-label">Restores ${plural(pv.files.length, "file")}</div>` + list("revert-files", pv.files);
  }
  if (pv.conflicts.length) {
    body +=
      `<div class="revert-label is-warn">Changed since — left as is</div>` + list("revert-conflicts", pv.conflicts);
  }
  return (
    `<div class="revert-head">${icon("tl-undo", "h-3.5 w-3.5")}<span>Revert to here?</span></div>` +
    body +
    `<div class="revert-actions">` +
    `<button type="button" id="revertCancelBtn" class="revert-cancel">Cancel</button>` +
    `<button type="button" id="revertConfirmBtn" class="revert-confirm">Revert</button>` +
    `</div>`
  );
}

async function openPreview(btn: HTMLElement): Promise<void> {
  closePop();
  const sessionId = state.sessionId;
  if (!guard(sessionId)) return;
  let index: number | null;
  let pv: RevertPreview;
  try {
    index = await resolveIndex(btn);
    if (index === null) throw new Error("this message is not saved yet");
    pv = await api.revertPreview(sessionId, null, index);
  } catch (error) {
    toast(`Cannot revert: ${String(error)}`, "error");
    return;
  }
  if (state.sessionId !== sessionId) return;
  const el = document.createElement("div");
  el.id = "revertPop";
  el.className = "revert-pop";
  el.setAttribute("role", "dialog");
  el.setAttribute("aria-label", "Revert to here");
  el.innerHTML = previewHtml(pv);
  document.body.append(el);
  const r = btn.getBoundingClientRect();
  const w = el.offsetWidth;
  const h = el.offsetHeight;
  el.style.left = `${Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w))}px`;
  el.style.top = `${r.bottom + 6 + h > window.innerHeight ? Math.max(8, r.top - h - 6) : r.bottom + 6}px`;
  el.addEventListener("click", (event) => {
    const t = event.target as HTMLElement;
    if (t.closest("#revertCancelBtn")) closePop();
    else if (t.closest("#revertConfirmBtn")) {
      closePop();
      void revert(sessionId, index);
    }
  });
  pop = el;
  el.querySelector<HTMLElement>("#revertConfirmBtn")?.focus();
}

async function revert(sessionId: string, index: number): Promise<void> {
  if (!guard(sessionId)) return;
  try {
    const res = await api.revertSession(sessionId, null, index);
    pending.set(sessionId, {
      index: res.index,
      restored: res.restored,
      skipped: res.skipped,
      snapshots: res.snapshots,
    });
    await reload(sessionId);
    if (state.sessionId === sessionId) {
      prefill = res.message || "";
      if (prefill && !inputEl.value.trim()) {
        inputEl.value = prefill;
        inputEl.dispatchEvent(new Event("input"));
      }
      inputEl.focus();
    }
    paintRevert();
  } catch (error) {
    toast(`Revert failed: ${String(error)}`, "error");
  }
}

async function redo(): Promise<void> {
  const sessionId = state.sessionId;
  if (!guard(sessionId)) return;
  try {
    await api.unrevertSession(sessionId, null);
    pending.delete(sessionId);
    if (prefill && inputEl.value === prefill) {
      inputEl.value = "";
      inputEl.dispatchEvent(new Event("input"));
    }
    prefill = "";
    paintRevert();
    await reload(sessionId);
  } catch (error) {
    toast(`Redo failed: ${String(error)}`, "error");
  }
}

// undoLast is /undo: revert the last user message without a preview
// (the banner's Redo is the safety net).
export async function undoLast(): Promise<void> {
  const sessionId = state.sessionId;
  if (!sessionId) {
    toast("Nothing to undo yet", "error");
    return;
  }
  if (!guard(sessionId)) return;
  try {
    const history = await api.sessionHistory(sessionId, null);
    const last = history.filter((m) => m.role === "user" && m.index !== undefined).pop();
    if (last?.index === undefined) {
      toast("Nothing to undo yet", "error");
      return;
    }
    await revert(sessionId, last.index);
  } catch (error) {
    toast(`Undo failed: ${String(error)}`, "error");
  }
}

export function initUndo(): void {
  messagesEl.addEventListener("click", (event) => {
    const btn = (event.target as HTMLElement).closest<HTMLElement>("[data-revert]");
    if (btn) {
      event.stopPropagation();
      void openPreview(btn);
    }
  });
  document.addEventListener("mousedown", (event) => {
    if (pop && !pop.contains(event.target as Node) && !(event.target as HTMLElement).closest("[data-revert]"))
      closePop();
  });
  document.addEventListener(
    "keydown",
    (event) => {
      if (event.key === "Escape" && pop) {
        event.stopPropagation();
        closePop();
      }
    },
    true,
  );
}
