// approvals.ts renders the permission gate's approval card inline in
// the turn, Codex-style: what the agent wants to run or write, why it
// is asking, and one button per allowed reply. Once answered, the card
// shrinks to a one-line receipt so the transcript stays readable.

import * as api from "../core/api";
import { escapeHtml, revealHidden } from "../core/format";
import type { Approval, ApprovalDecision } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { argsHtml } from "./toolBodies";

function title(ap: Approval): string {
  if (ap.kind === "permissions") return "Grant additional permissions?";
  if (ap.kind === "mcp") return ap.destructive ? "Call a destructive MCP tool?" : "Call this MCP tool?";
  if (ap.destructive) return "Run a destructive command?";
  if (ap.tool === "shell") return "Run this command?";
  if (ap.kind === "read") return "Read this file?";
  return ap.tool === "edit" ? "Edit this file?" : "Write this file?";
}

function subject(ap: Approval): string {
  if (ap.kind === "mcp" && ap.server) return `${ap.server} › ${ap.mcpTool || ap.tool}`;
  return ap.command || ap.path || ap.tool;
}

function choiceLabel(choice: ApprovalDecision): string {
  switch (choice) {
    case "once":
      return "Allow once";
    case "session":
      return "Allow for this session";
    case "folder":
      return "Always allow in this folder";
    case "global":
      return "Always allow everywhere";
    case "deny":
      return "Deny";
  }
}

const RECEIPT: Record<string, string> = {
  once: "Allowed once",
  session: "Allowed for this session",
  folder: "Always allowed in this folder",
  global: "Always allowed everywhere",
  deny: "Denied",
  interrupted: "Not answered — the turn stopped",
};

function receipt(ap: Approval): string {
  const label = RECEIPT[ap.decision ?? ""] ?? (ap.state === "approved" ? "Allowed" : "Denied");
  const ok = ap.state === "approved";
  return (
    `<div class="approval-receipt">` +
    `<span class="approval-receipt-ico">${icon(ok ? "sl-shield-check" : "sl-shield-off", "h-3.5 w-3.5")}</span>` +
    `<span class="approval-receipt-what">${escapeHtml(label)}</span>` +
    `<span class="approval-receipt-sep" aria-hidden="true">·</span>` +
    `<span class="approval-receipt-cmd" title="${escapeHtml(subject(ap))}">${escapeHtml(subject(ap))}</span>` +
    `</div>`
  );
}

function pendingHtml(ap: Approval): string {
  const choices = ap.choices?.length ? ap.choices : (["once", "deny"] as ApprovalDecision[]);
  const reasons = (ap.reasons ?? []).map((r) => `<span class="approval-reason">${escapeHtml(r)}</span>`).join("");
  const buttons = choices
    .map((c, i) => {
      const cls = c === "deny" ? "approval-btn is-deny" : i === 0 ? "approval-btn is-primary" : "approval-btn";
      const key = c === "deny" ? `<kbd>Esc</kbd>` : i === 0 ? `<kbd>⏎</kbd>` : "";
      return `<button type="button" class="${cls}" data-approve="${c}"><span>${choiceLabel(c)}</span>${key}</button>`;
    })
    .join("");
  const kindIcon =
    ap.destructive || ap.kind === "permissions" ? "sl-shield-off" : ap.kind === "mcp" ? "sl-plug" : "sl-shield";
  const body =
    ap.kind === "mcp"
      ? `<div class="approval-mcp"><span class="approval-mcp-server">${escapeHtml(ap.server ?? "")}</span>` +
        `<span class="approval-mcp-sep" aria-hidden="true">›</span><span class="approval-mcp-tool">${escapeHtml(ap.mcpTool || ap.tool)}</span></div>` +
        `<div class="approval-args">${argsHtml(ap.args ?? "")}</div>`
      : `<pre class="approval-subject"><span class="approval-prompt" aria-hidden="true">${ap.tool === "shell" ? "$" : "›"}</span>${revealHidden(escapeHtml(subject(ap)))}</pre>`;
  return (
    `<div class="approval-head">` +
    `<span class="approval-ico">${icon(kindIcon, "h-4 w-4")}</span>` +
    `<span class="approval-title">${escapeHtml(title(ap))}</span>` +
    `<span class="approval-reasons">${reasons}</span>` +
    `</div>` +
    body +
    `<div class="approval-actions">${buttons}</div>`
  );
}

// paintApproval draws (or redraws) a card for the approval's state.
export function paintApproval(el: HTMLElement, ap: Approval): void {
  const was = el.dataset.state;
  el.dataset.approval = ap.id;
  el.dataset.state = ap.state;
  el.classList.toggle("is-destructive", !!ap.destructive || ap.kind === "permissions");
  el.classList.toggle("is-mcp", ap.kind === "mcp");
  el.classList.toggle("is-resolved", ap.state !== "pending");
  if (ap.state === "pending") {
    if (was !== "pending") el.innerHTML = pendingHtml(ap);
    return;
  }
  el.classList.toggle("is-denied", ap.state === "denied");
  el.innerHTML = receipt(ap);
}

// createApprovalCard builds a card wired to answer for sessionId.
export function createApprovalCard(sessionId: string, ap: Approval): HTMLElement {
  const el = document.createElement("div");
  el.className = "approval-card";
  el.setAttribute("role", "group");
  el.setAttribute("aria-label", "Approval needed");
  paintApproval(el, ap);
  el.addEventListener("click", (event) => {
    const btn = (event.target as HTMLElement).closest<HTMLButtonElement>("[data-approve]");
    if (btn) void answer(el, sessionId, btn.dataset.approve as ApprovalDecision);
  });
  el.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && el.dataset.state === "pending" && el.querySelector('[data-approve="deny"]')) {
      event.preventDefault();
      event.stopPropagation();
      void answer(el, sessionId, "deny");
    }
  });
  return el;
}

// focusApproval puts the keyboard on the primary reply so Enter allows
// once and Esc denies.
export function focusApproval(el: HTMLElement): void {
  const active = document.activeElement as HTMLElement | null;
  // Never steal the caret from a message being typed.
  if (active && active.matches("textarea, input") && (active as HTMLTextAreaElement).value) return;
  el.querySelector<HTMLButtonElement>("[data-approve]")?.focus({ preventScroll: true });
}

async function answer(el: HTMLElement, sessionId: string, decision: ApprovalDecision): Promise<void> {
  if (el.dataset.state !== "pending" || el.classList.contains("is-sending")) return;
  const id = el.dataset.approval ?? "";
  el.classList.add("is-sending");
  el.querySelectorAll<HTMLButtonElement>("[data-approve]").forEach((b) => (b.disabled = true));
  try {
    await api.answerApproval(sessionId, id, decision);
  } catch (error) {
    toast(`Could not answer: ${String(error)}`, "error");
    el.querySelectorAll<HTMLButtonElement>("[data-approve]").forEach((b) => (b.disabled = false));
  } finally {
    el.classList.remove("is-sending");
  }
}
