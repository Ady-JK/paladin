// worktrees.ts is the Codex-style worktree flow: a composer toggle that
// starts the next session in its own git worktree (branch paladin/…),
// "Fork into worktree" for an existing session, and a bar on worktree
// sessions with the branch, its changes, and Merge back.

import * as api from "../core/api";
import { modelSelectBtn, sendBtn } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { GitInfo, WorktreeInfo, WorktreeStatus } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { isBusy } from "./live";
import { openSession, refreshSessions } from "./sessions";

let chip: HTMLButtonElement | null = null;
let bar: HTMLElement | null = null;
// wanted is the toggle for the next new session.
let wanted = false;
let git: { cwd: string; info: GitInfo | null; loading: boolean } = { cwd: "", info: null, loading: false };
// current is the visible session's worktree link.
let current: WorktreeInfo | null = null;
let merging = false;

function chipEl(): HTMLButtonElement {
  if (chip) return chip;
  const el = document.createElement("button");
  el.type = "button";
  el.id = "worktreeChip";
  el.className = "wt-chip hidden";
  el.setAttribute("aria-pressed", "false");
  el.innerHTML = `${icon("code-branch", "h-3 w-3")}<span>Worktree</span>`;
  el.addEventListener("click", () => {
    wanted = !wanted;
    paintChip();
  });
  const anchor = document.querySelector(".perm-wrap") ?? modelSelectBtn.parentElement;
  anchor?.after(el);
  chip = el;
  return el;
}

function paintChip(): void {
  const el = chipEl();
  const info = git.cwd === state.cwd ? git.info : null;
  const show = !state.sessionId && !!info?.repo && !!info.hasCommits;
  el.classList.toggle("hidden", !show);
  el.setAttribute("aria-pressed", String(show && wanted));
  el.classList.toggle("is-on", show && wanted);
  el.title = wanted
    ? `The next session works in its own git worktree, branched from ${info?.branch || "HEAD"}`
    : "Start this session in a separate git worktree";
}

// syncWorktreeChip shows the toggle for a new session in a git repo. It
// asks the core once per folder.
export function syncWorktreeChip(): void {
  if (state.cwd && git.cwd !== state.cwd && !git.loading) {
    const cwd = state.cwd;
    git = { cwd, info: null, loading: true };
    api
      .gitInfo(cwd)
      .then((info) => {
        if (git.cwd === cwd) git = { cwd, info, loading: false };
      })
      .catch(() => {
        if (git.cwd === cwd) git = { cwd, info: null, loading: false };
      })
      .finally(paintChip);
  }
  paintChip();
}

// takeWorktreeRequest is consumed by the first send of a new session.
export function takeWorktreeRequest(message: string): api.WorktreeOpts {
  const info = git.cwd === state.cwd ? git.info : null;
  if (!wanted || !info?.repo || !info.hasCommits) return {};
  wanted = false;
  const name = message.split("\n")[0].trim().slice(0, 48).trim();
  return { worktree: true, name };
}

function barEl(): HTMLElement {
  if (bar) return bar;
  const el = document.createElement("div");
  el.id = "worktreeBar";
  el.className = "wt-bar hidden";
  const footer = sendBtn.closest("footer");
  (footer?.firstElementChild ?? footer)?.prepend(el);
  el.addEventListener("click", (event) => {
    if ((event.target as HTMLElement).closest("#mergeBtn")) void merge();
  });
  bar = el;
  return el;
}

function paintBar(st: WorktreeStatus | null): void {
  const el = barEl();
  if (!current) {
    el.classList.add("hidden");
    el.innerHTML = "";
    return;
  }
  const into = st?.into || current.baseBranch || "main";
  const stats: string[] = [];
  if (st && !st.exists) stats.push("worktree missing");
  else if (st) {
    stats.push(st.changed ? `${st.changed} changed` : "clean");
    if (st.ahead) stats.push(`${st.ahead} ahead`);
  }
  el.innerHTML =
    `<span class="wt-ico">${icon("code-branch", "h-3.5 w-3.5")}</span>` +
    `<span class="wt-branch" title="${escapeHtml(current.path)}">${escapeHtml(current.branch)}</span>` +
    `<span class="wt-into">→ ${escapeHtml(into)}</span>` +
    (stats.length ? `<span class="wt-stats">${escapeHtml(stats.join(" · "))}</span>` : "") +
    `<button type="button" id="mergeBtn" class="wt-merge"${merging || (st && !st.exists) ? " disabled" : ""} title="Commit the worktree's changes and merge ${escapeHtml(current.branch)} into ${escapeHtml(into)}">${icon("sl-merge", "h-3 w-3")}<span>${merging ? "Merging…" : "Merge back"}</span></button>`;
  el.classList.remove("hidden");
}

// setWorktree shows (or hides) the bar for the visible session.
export function setWorktree(info: WorktreeInfo | null | undefined): void {
  current = info ?? null;
  paintBar(null);
  void refreshWorktreeBar();
}

// refreshWorktreeBar re-reads the branch's changes (after a turn).
export async function refreshWorktreeBar(): Promise<void> {
  if (!current || !state.sessionId) return;
  const sessionId = state.sessionId;
  try {
    const st = await api.worktreeStatus(sessionId, null);
    if (state.sessionId === sessionId && current) paintBar(st);
  } catch {
    if (state.sessionId === sessionId) paintBar(null);
  }
}

async function merge(): Promise<void> {
  const sessionId = state.sessionId;
  if (!current || merging || !sessionId) return;
  if (isBusy(sessionId)) {
    toast("Wait for the running turn to finish before merging", "error");
    return;
  }
  merging = true;
  paintBar(null);
  try {
    const res = await api.worktreeMerge(sessionId, null);
    const into = res.into || current.baseBranch || "main";
    if (res.conflicts?.length) {
      toast(`Merge conflicts in ${res.conflicts.join(", ")} — merge aborted, ${into} is unchanged`, "error");
    } else if (res.nothingToMerge) {
      toast(`Nothing to merge — ${current.branch} has no new changes`);
    } else if (res.merged) {
      toast(`Merged into ${into}${res.commit ? ` (${res.commit.slice(0, 7)})` : ""}`);
    }
  } catch (error) {
    toast(`Merge failed: ${String(error).replace(/^Error:\s*/, "")}`, "error");
  } finally {
    merging = false;
    await refreshWorktreeBar();
  }
}

// forkIntoWorktree copies a session's conversation into a new session
// working in its own worktree, then opens it.
export async function forkIntoWorktree(id: string): Promise<void> {
  const src = state.sessions.find((s) => s.sessionId === id);
  try {
    const info = await api.forkSession(id, src?.cwd ?? null, { worktree: true, name: src?.title || "" });
    await refreshSessions();
    await openSession(info.sessionId);
    toast(`Forked into worktree ${info.worktree?.branch ?? ""}`.trim());
  } catch (error) {
    toast(`Fork into worktree failed: ${String(error).replace(/^Error:\s*/, "")}`, "error");
  }
}
