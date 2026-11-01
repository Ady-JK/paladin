// findings.ts draws a /review's report_findings call as a card: the
// verdict, then each finding by priority. Opening a finding shows the
// code around it (fileExcerpt) with its lines marked, a link into the
// session's Changes diff when this session changed that file, and a Fix
// button that puts the finding into the composer for a normal turn.

import * as api from "../core/api";
import { inputEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import type { Finding, FindingsReport, ToolInfo } from "../core/types";
import { icon } from "../icons";
import { relPath } from "./toolBodies";

// ChangesLink lets the side pane answer "did this session change the
// file?" and open its diff, without findings importing the pane.
type ChangesLink = {
  has: (sessionId: string, rel: string) => boolean;
  open: (sessionId: string, rel: string, start: number, end: number) => void;
};

let changes: ChangesLink | null = null;

export function registerChangesLink(link: ChangesLink): void {
  changes = link;
}

const VERDICTS: Record<string, string> = {
  looks_good: "Looks good",
  merge_after_fixes: "Merge after fixes",
  needs_rework: "Needs rework",
};

function report(tool: ToolInfo): FindingsReport | null {
  try {
    const parsed = JSON.parse(tool.result) as FindingsReport;
    return parsed && Array.isArray(parsed.findings) ? parsed : null;
  } catch {
    return null;
  }
}

function errorText(tool: ToolInfo): string {
  try {
    const parsed = JSON.parse(tool.result) as { error?: unknown };
    if (typeof parsed.error === "string") return parsed.error;
  } catch {
    // plain text result
  }
  return tool.result || "the review could not be recorded";
}

function lines(f: Finding): string {
  return f.end_line > f.start_line ? `${f.start_line}–${f.end_line}` : `${f.start_line}`;
}

function findingHtml(f: Finding): string {
  const where = `${relPath(f.path)}:${lines(f)}`;
  return (
    `<li class="finding" data-finding="${escapeHtml(f.id)}" data-priority="${escapeHtml(f.priority)}">` +
    `<button type="button" class="finding-head" aria-expanded="false">` +
    `<span class="finding-pri is-${escapeHtml(f.priority.toLowerCase())}">${escapeHtml(f.priority)}</span>` +
    `<span class="finding-title">${escapeHtml(f.title)}</span>` +
    `<span class="finding-loc" title="${escapeHtml(f.path)}">${escapeHtml(where)}</span>` +
    `<span class="finding-caret">${icon("chevron-right", "h-3 w-3")}</span></button>` +
    `<div class="finding-body" hidden>` +
    `<p class="finding-text">${escapeHtml(f.body)}</p>` +
    (f.fix ? `<p class="finding-fix"><b>Fix</b> ${escapeHtml(f.fix)}</p>` : "") +
    `<div class="finding-excerpt" aria-label="Code at ${escapeHtml(where)}"></div>` +
    `<div class="finding-actions">` +
    `<span class="finding-conf">${escapeHtml(f.confidence)} confidence</span>` +
    `<button type="button" class="finding-btn" data-finding-changes hidden>View in changes</button>` +
    `<button type="button" class="finding-btn is-primary" data-finding-fix>Fix</button>` +
    `</div></div></li>`
  );
}

// paintFindings redraws a card for the call's current state.
export function paintFindings(card: HTMLElement, tool: ToolInfo): void {
  card.dataset.status = tool.status;
  if (tool.status === "running") {
    card.innerHTML = `<div class="findings-head">${icon("tl-search", "h-4 w-4")}<span class="findings-title">Review</span><span class="findings-note">Recording findings…</span></div>`;
    return;
  }
  const rep = tool.status === "failed" ? null : report(tool);
  if (!rep) {
    card.innerHTML =
      `<div class="findings-head">${icon("tl-search", "h-4 w-4")}<span class="findings-title">Review</span></div>` +
      `<p class="findings-error">Could not record the findings: ${escapeHtml(errorText(tool))}</p>`;
    return;
  }
  const n = rep.findings.length;
  card.innerHTML =
    `<div class="findings-head">${icon("tl-search", "h-4 w-4")}<span class="findings-title">Review</span>` +
    `<span class="findings-verdict is-${escapeHtml(rep.verdict)}">${escapeHtml(VERDICTS[rep.verdict] ?? rep.verdict)}</span>` +
    `<span class="findings-note">${n === 0 ? "no findings" : `${n} ${n === 1 ? "finding" : "findings"}`}</span></div>` +
    (rep.summary ? `<p class="findings-summary">${escapeHtml(rep.summary)}</p>` : "") +
    (n ? `<ol class="findings-list">${rep.findings.map(findingHtml).join("")}</ol>` : "");
  shown.set(card, rep.findings);
}

// shown holds each card's findings for its click handler.
const shown = new WeakMap<HTMLElement, Finding[]>();

// createFindingsCard builds the card for one report_findings call.
export function createFindingsCard(sessionId: string, tool: ToolInfo): HTMLElement {
  const card = document.createElement("section");
  card.className = "findings-card";
  card.dataset.findings = tool.id;
  card.addEventListener("click", (event) => onClick(sessionId, card, event));
  paintFindings(card, tool);
  return card;
}

function onClick(sessionId: string, card: HTMLElement, event: MouseEvent): void {
  const target = event.target as HTMLElement;
  const item = target.closest<HTMLElement>(".finding");
  const f = item && shown.get(card)?.find((x) => x.id === item.dataset.finding);
  if (!item || !f) return;
  if (target.closest(".finding-head")) {
    toggle(sessionId, item, f);
  } else if (target.closest("[data-finding-fix]")) {
    fill(f);
  } else if (target.closest("[data-finding-changes]")) {
    changes?.open(sessionId, relPath(f.path), f.start_line, f.end_line);
  }
}

function toggle(sessionId: string, item: HTMLElement, f: Finding): void {
  const head = item.querySelector<HTMLElement>(".finding-head")!;
  const body = item.querySelector<HTMLElement>(".finding-body")!;
  const open = head.getAttribute("aria-expanded") !== "true";
  head.setAttribute("aria-expanded", String(open));
  body.hidden = !open;
  if (!open) return;
  const link = item.querySelector<HTMLElement>("[data-finding-changes]")!;
  link.hidden = !changes?.has(sessionId, relPath(f.path));
  const excerpt = item.querySelector<HTMLElement>(".finding-excerpt")!;
  if (excerpt.dataset.loaded) return;
  excerpt.dataset.loaded = "1";
  excerpt.textContent = "Loading code…";
  api
    .fileExcerpt(sessionId, null, f.path, f.start_line, f.end_line)
    .then((ex) => {
      excerpt.innerHTML = ex.lines
        .map((line, i) => {
          const n = ex.startLine + i;
          const mark = n >= f.start_line && n <= f.end_line ? " is-mark" : "";
          return `<div class="file-line${mark}"><span class="ln">${n}</span><code>${escapeHtml(line) || " "}</code></div>`;
        })
        .join("");
    })
    .catch(() => {
      excerpt.textContent = "The code is not available here (the file may be outside the workspace or gone).";
    });
}

// fill puts the finding into the composer; the user sends it.
function fill(f: Finding): void {
  const text =
    `Fix review finding ${f.id} (${f.priority}): ${f.title}\n` +
    `${relPath(f.path)}:${lines(f)}\n${f.body}` +
    (f.fix ? `\nSuggested fix: ${f.fix}` : "");
  inputEl.value = `${inputEl.value.trimEnd()}${inputEl.value.trim() ? "\n\n" : ""}${text}`;
  inputEl.dispatchEvent(new Event("input", { bubbles: true }));
  inputEl.focus();
}
