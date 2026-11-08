// undo.ts is "Revert to here": a user message's undo button previews
// the rewind, then restores the folder to the snapshot taken before that
// message and drops the message and everything after it. The restore is
// recoverLatest restores the state saved immediately before the last undo.

import * as api from "../core/api";
import { inputEl, messagesEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { RevertPreview } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { clearLanded, isBusy } from "./live";
import { loadHistory, refreshSessions } from "./sessions";

let pop: HTMLElement | null = null;
const recoveryBar = document.querySelector<HTMLElement>("#recoveryBar")!;

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

function closePop(): void {
  pop?.remove();
  pop = null;
}

async function reload(sessionId: string): Promise<void> {
  clearLanded(sessionId);
  if (state.sessionId === sessionId) await loadHistory(sessionId);
  void refreshSessions();
}

export async function refreshRecovery(sessionId: string): Promise<void> {
  recoveryBar.hidden = true;
  if (!sessionId) return;
  try {
    const available = await api.recoveryAvailable(sessionId);
    if (state.sessionId === sessionId) recoveryBar.hidden = !available;
  } catch {
    // Older sidecars may not support recovery.
  }
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
    const reason = pv.protection?.reason ? ` ${escapeHtml(pv.protection.reason)}` : "";
    body += `<p class="revert-note">Only the conversation is rewound; no usable undo snapshot is available.${reason}</p>`;
  } else if (pv.files.length === 0 && pv.deleted.length === 0 && pv.skipped.length === 0) {
    body += `<p class="revert-note">No file changes to undo.</p>`;
  }
  if (pv.files.length) {
    body +=
      `<div class="revert-label">Restores ${plural(pv.files.length, "file")}</div>` + list("revert-files", pv.files);
  }
  if (pv.deleted.length) {
    body +=
      `<div class="revert-label is-warn">Deletes ${plural(pv.deleted.length, "file")} created since</div>` +
      list("revert-conflicts", pv.deleted);
  }
  if (pv.skipped.length) {
    body +=
      `<div class="revert-label is-warn">Leaves ${plural(pv.skipped.length, "file")} alone (too large or binary)</div>` +
      list("revert-conflicts", pv.skipped);
  }
  if (pv.protection?.excluded?.length) {
    body +=
      `<div class="revert-label is-warn">Excluded from snapshots; not restored</div>` +
      list("revert-excluded", pv.protection.excluded);
  }
  if (pv.protection?.truncated) {
    body += `<p class="revert-note">The exclusion list is incomplete; more paths may not be restored.</p>`;
  }
  if (pv.snapshots) {
    body += `<p class="revert-note">These files may include later changes by you or another chat. A recovery snapshot will be saved before undo. Ignored, large and binary files may not be captured.</p>`;
  } else {
    body += `<p class="revert-note">The conversation can be recovered after undo.</p>`;
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
      void revert(sessionId, index, pv.version);
    }
  });
  pop = el;
  el.querySelector<HTMLElement>("#revertConfirmBtn")?.focus();
}

async function revert(sessionId: string, index: number, version?: string): Promise<void> {
  if (!guard(sessionId)) return;
  try {
    const res = await api.revertSession(sessionId, null, index, version);
    await reload(sessionId);
    await refreshRecovery(sessionId);
    if (state.sessionId === sessionId) {
      const prefill = res.message || "";
      if (prefill && !inputEl.value.trim()) {
        inputEl.value = prefill;
        inputEl.dispatchEvent(new Event("input"));
      }
      inputEl.focus();
    }
    const parts: string[] = [];
    if (res.restored.length) parts.push(`${plural(res.restored.length, "file")} restored`);
    if (res.deleted.length) parts.push(`${plural(res.deleted.length, "file")} deleted`);
    if (res.skipped.length) parts.push(`${plural(res.skipped.length, "file")} left as is`);
    toast(parts.length ? `Rewound: ${parts.join(", ")}.` : "Conversation rewound.");
  } catch (error) {
    toast(`Revert failed: ${String(error)}`, "error");
  }
}

async function openRecovery(btn: HTMLElement): Promise<void> {
  closePop();
  const sessionId = state.sessionId;
  if (!guard(sessionId)) return;
  try {
    const pv = await api.recoveryPreview(sessionId);
    if (state.sessionId !== sessionId) return;
    const list = (files: string[]) =>
      `<ul class="revert-files">${files.map((f) => `<li>${escapeHtml(f)}</li>`).join("")}</ul>`;
    const el = document.createElement("div");
    el.id = "revertPop";
    el.className = "revert-pop";
    el.setAttribute("role", "dialog");
    el.setAttribute("aria-label", "Recover before undo");
    el.innerHTML =
      `<div class="revert-head">Recover before undo?</div>` +
      `<p class="revert-sub">Restores the conversation and folder to their state before the latest undo. Later messages will be removed and later file changes may be overwritten or deleted. Ignored, large and binary files may not be captured.</p>` +
      (pv.files.length ? `<div class="revert-label">Files to restore</div>${list(pv.files)}` : "") +
      (pv.deleted.length ? `<div class="revert-label is-warn">Files to delete</div>${list(pv.deleted)}` : "") +
      (pv.skipped.length ? `<div class="revert-label is-warn">Files left unchanged</div>${list(pv.skipped)}` : "") +
      `<div class="revert-actions"><button type="button" id="revertCancelBtn" class="revert-cancel">Cancel</button><button type="button" id="recoverConfirmBtn" class="revert-confirm">Recover</button></div>`;
    document.body.append(el);
    const r = btn.getBoundingClientRect();
    el.style.left = `${Math.max(8, Math.min(window.innerWidth - el.offsetWidth - 8, r.right - el.offsetWidth))}px`;
    el.style.top = `${Math.min(window.innerHeight - el.offsetHeight - 8, r.bottom + 6)}px`;
    el.addEventListener("click", (event) => {
      const target = event.target as HTMLElement;
      if (target.closest("#revertCancelBtn")) closePop();
      if (target.closest("#recoverConfirmBtn")) {
        closePop();
        void recover(sessionId, pv.version);
      }
    });
    pop = el;
    el.querySelector<HTMLElement>("#recoverConfirmBtn")?.focus();
  } catch (error) {
    toast(`Cannot preview recovery: ${String(error)}`, "error");
  }
}

async function recover(sessionId: string, version?: string): Promise<void> {
  if (!guard(sessionId)) return;
  try {
    const result = await api.recoverLatest(sessionId, version);
    await reload(sessionId);
    await refreshRecovery(sessionId);
    toast(
      result.skipped.length
        ? `Recovered; ${plural(result.skipped.length, "file")} left unchanged.`
        : "Recovered the state before undo.",
    );
  } catch (error) {
    toast(`Recovery failed: ${String(error)}`, "error");
  }
}

// undoLast is /undo: show the same confirmation as the message control.
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
    const buttons = Array.from(messagesEl.querySelectorAll<HTMLElement>("[data-revert]")).filter(
      (btn) => !btn.closest(".turn-queued"),
    );
    const btn = buttons.at(-1);
    if (!btn) throw new Error("the last message is not displayed");
    await openPreview(btn);
  } catch (error) {
    toast(`Undo failed: ${String(error)}`, "error");
  }
}

export function initUndo(): void {
  document.querySelector<HTMLElement>("#recoverLatestBtn")?.addEventListener("click", (event) => {
    void openRecovery(event.currentTarget as HTMLElement);
  });
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
