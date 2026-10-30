// budget.ts owns the composer's spend chip: what the visible session
// has spent against its limit, and a menu to set, clear or inherit the
// per-session budget. When a turn crosses the limit the core disables
// tools and asks the model for a done/remaining summary.

import * as api from "../core/api";
import { sendBtn } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { BudgetView } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { formatUsd } from "./usage";

const EMPTY: BudgetView = { defaultUsd: 0, sessionUsd: null, effectiveUsd: 0, spentUsd: 0, hasCost: false, calls: 0 };

let chip: HTMLButtonElement | null = null;
let menu: HTMLElement | null = null;
let view: BudgetView = EMPTY;
// A pick made before the session exists: undefined = none, null = inherit.
let draft: number | null | undefined;
let syncSeq = 0;

// parseBudget reads "/budget" style input: "3.5" or "$3.5" sets, "off"
// turns the limit off (0), "default" inherits; undefined is not valid.
export function parseBudget(raw: string): number | null | undefined {
  const v = raw.trim().toLowerCase();
  if (v === "off" || v === "none" || v === "no") return 0;
  if (v === "default" || v === "reset" || v === "inherit") return null;
  const n = Number(v.replace(/^\$/, ""));
  return v !== "" && Number.isFinite(n) && n >= 0 ? n : undefined;
}

function withDraft(v: BudgetView): BudgetView {
  if (state.sessionId || draft === undefined) return v;
  return { ...v, sessionUsd: draft, effectiveUsd: draft ?? v.defaultUsd };
}

function level(v: BudgetView): string {
  if (v.effectiveUsd <= 0) return "none";
  if (!v.hasCost && v.calls > 0) return "unknown";
  const ratio = v.spentUsd / v.effectiveUsd;
  return ratio >= 1 ? "over" : ratio >= 0.8 ? "warn" : "ok";
}

function ensureChip(): HTMLButtonElement {
  if (chip) return chip;
  const wrap = document.createElement("div");
  wrap.className = "perm-wrap budget-wrap";
  wrap.innerHTML =
    `<button type="button" id="budgetChip" class="perm-chip budget-chip" aria-haspopup="dialog" aria-expanded="false"></button>` +
    `<div id="budgetMenu" class="perm-menu budget-menu hidden" role="dialog" aria-label="Spend budget"></div>`;
  const group = sendBtn.parentElement;
  group?.parentElement?.insertBefore(wrap, group);
  chip = wrap.querySelector<HTMLButtonElement>("#budgetChip")!;
  menu = wrap.querySelector<HTMLElement>("#budgetMenu")!;
  chip.addEventListener("click", (event) => {
    event.stopPropagation();
    if (menu!.classList.contains("hidden")) openBudgetMenu();
    else closeMenu();
  });
  menu.addEventListener("click", (event) => {
    const btn = (event.target as HTMLElement).closest<HTMLElement>("[data-budget]");
    if (!btn) return;
    const kind = btn.dataset.budget;
    if (kind === "save") {
      const input = menu!.querySelector<HTMLInputElement>("input")!;
      const v = parseBudget(input.value);
      if (v === undefined || v === null) {
        input.setAttribute("aria-invalid", "true");
        input.focus();
        return;
      }
      void setSessionBudget(v);
    } else if (kind === "off") {
      void setSessionBudget(0);
    } else if (kind === "inherit") {
      void setSessionBudget(null);
    }
    closeMenu();
  });
  menu.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && (event.target as HTMLElement).tagName === "INPUT") {
      event.preventDefault();
      menu!.querySelector<HTMLElement>('[data-budget="save"]')?.click();
    } else if (event.key === "Escape") {
      event.stopPropagation();
      closeMenu();
      chip?.focus();
    }
  });
  document.addEventListener("click", (event) => {
    if (menu && !menu.classList.contains("hidden") && !wrap.contains(event.target as Node)) closeMenu();
  });
  return chip;
}

function paintChip(): void {
  const el = ensureChip();
  const v = withDraft(view);
  const lv = level(v);
  el.dataset.level = lv;
  const limit = v.effectiveUsd > 0 ? ` / ${formatUsd(v.effectiveUsd)}` : "";
  const label = v.calls > 0 || limit ? `${formatUsd(v.spentUsd)}${limit}` : "Budget";
  const warn = lv === "unknown" ? `<span class="perm-chip-warn" aria-hidden="true"></span>` : "";
  el.innerHTML = `${icon("sl-budget", "h-3.5 w-3.5")}<span>${escapeHtml(label)}</span>${warn}`;
  el.title =
    lv === "unknown"
      ? "Budget set, but the provider reports no cost — it can't be enforced"
      : v.effectiveUsd > 0
        ? `Spent ${formatUsd(v.spentUsd)} of ${formatUsd(v.effectiveUsd)}`
        : "No spend limit — click to set one";
}

function paintMenu(): void {
  if (!menu) return;
  const v = withDraft(view);
  const own = v.sessionUsd;
  const dflt = v.defaultUsd > 0 ? formatUsd(v.defaultUsd) : "no limit";
  const spent = v.hasCost || v.calls === 0 ? `Spent ${formatUsd(v.spentUsd)} in this session.` : `${v.calls} call${v.calls === 1 ? "" : "s"}, cost unknown.`;
  const noCost =
    !v.hasCost && v.calls > 0
      ? `<div class="budget-warn"><span class="perm-chip-warn" aria-hidden="true"></span><span>This provider reports no cost, so a limit can't be enforced.</span></div>`
      : "";
  const mark = (on: boolean): string => (on ? icon("check", "h-3 w-3") : "");
  menu.innerHTML =
    `<div class="perm-menu-head">Spend budget</div>` +
    `<div class="budget-spent">${escapeHtml(spent)}</div>` +
    noCost +
    `<form class="budget-form" onsubmit="return false">` +
    `<span class="budget-dollar">$</span><input type="number" min="0" step="0.5" inputmode="decimal" placeholder="e.g. 2.50" aria-label="Session budget in USD" value="${own && own > 0 ? own : ""}" />` +
    `<button type="button" class="budget-save" data-budget="save">Set</button>` +
    `</form>` +
    `<button type="button" class="perm-item${own === null ? " is-on" : ""}" data-budget="inherit"><span class="perm-item-text"><span class="perm-item-label">Use app default (${escapeHtml(dflt)})</span></span><span class="perm-item-check">${mark(own === null)}</span></button>` +
    `<button type="button" class="perm-item${own === 0 ? " is-on" : ""}" data-budget="off"><span class="perm-item-text"><span class="perm-item-label">No limit</span></span><span class="perm-item-check">${mark(own === 0)}</span></button>` +
    `<div class="budget-foot">At the limit the agent stops using tools and writes a summary of what's done and what's left.</div>`;
}

export function openBudgetMenu(): void {
  ensureChip();
  paintMenu();
  menu!.classList.remove("hidden");
  chip!.setAttribute("aria-expanded", "true");
  menu!.querySelector<HTMLInputElement>("input")?.focus();
  void syncBudget();
}

function closeMenu(): void {
  menu?.classList.add("hidden");
  chip?.setAttribute("aria-expanded", "false");
}

// setSessionBudget stores the visible session's limit (null inherits
// the app default, 0 turns it off). Before the first message it is
// kept as a draft and applied by applyBudgetDraft.
export async function setSessionBudget(v: number | null): Promise<void> {
  if (!state.sessionId) {
    draft = v;
    paintChip();
    return;
  }
  try {
    view = await api.budgetSet("session", state.cwd || null, state.sessionId, v);
    paintChip();
    toast(v === null ? "Budget: app default" : v === 0 ? "Budget: no limit" : `Budget: ${formatUsd(v)}`);
  } catch (error) {
    toast(`Budget not saved: ${String(error)}`, "error");
  }
}

// applyBudgetDraft saves a pick made before the session existed.
export async function applyBudgetDraft(sessionId: string): Promise<void> {
  if (draft === undefined) return;
  const v = draft;
  draft = undefined;
  try {
    view = await api.budgetSet("session", state.cwd || null, sessionId, v);
  } catch (error) {
    toast(`Budget not saved: ${String(error)}`, "error");
  }
  paintChip();
}

// resetBudgetDraft forgets an unsent pick when the user starts over.
export function resetBudgetDraft(): void {
  draft = undefined;
}

// loadDefaultBudget fills Settings → Analytics with the app default.
export async function loadDefaultBudget(): Promise<void> {
  const input = document.querySelector<HTMLInputElement>("#settingsBudgetDefault");
  if (!input) return;
  try {
    const v = await api.budgetGet(null, null);
    input.value = v.defaultUsd > 0 ? String(v.defaultUsd) : "";
  } catch {
    // Older sidecar: leave the field empty.
  }
}

// saveDefaultBudget stores the app-wide default; empty or 0 clears it.
async function saveDefaultBudget(): Promise<void> {
  const input = document.querySelector<HTMLInputElement>("#settingsBudgetDefault")!;
  const raw = input.value.trim();
  const v = raw === "" ? null : parseBudget(raw);
  if (v === undefined) {
    input.setAttribute("aria-invalid", "true");
    toast("Default budget must be a dollar amount", "error");
    return;
  }
  input.removeAttribute("aria-invalid");
  try {
    await api.budgetSet("global", null, null, v === 0 ? null : v);
    toast(v ? `Default budget saved: ${formatUsd(v)}` : "Default budget cleared");
    void syncBudget();
  } catch (error) {
    toast(`Default budget not saved: ${String(error)}`, "error");
  }
}

export function initBudgetSettings(): void {
  document.querySelector("#settingsBudgetSaveBtn")?.addEventListener("click", () => void saveDefaultBudget());
  document.querySelector("#settingsBudgetDefault")?.addEventListener("keydown", (event) => {
    if ((event as KeyboardEvent).key === "Enter") void saveDefaultBudget();
  });
}

// syncBudget reloads the visible session's spend and limit.
export function syncBudget(): Promise<void> {
  ensureChip();
  const seq = ++syncSeq;
  const sid = state.sessionId;
  return api.budgetGet(state.cwd || null, sid || null).then(
    (v) => {
      if (seq !== syncSeq) return;
      view = v;
      paintChip();
      if (menu && !menu.classList.contains("hidden") && !menu.contains(document.activeElement)) paintMenu();
    },
    () => {
      if (seq !== syncSeq) return;
      view = EMPTY;
      paintChip();
    },
  );
}
