// projectNew.ts is the "+" on a sidebar folder: a small menu of the three
// engines that starts a blank session in that folder, with no dialog between.
// It shares its start steps with the New Session dialog (startSessionIn).

import { sessionListEl } from "../core/dom";
import { bare, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { claudeAvailability, refreshClaudeAvailability } from "../adapters/claude/harness";
import { codexAvailability, refreshCodexAvailability } from "../adapters/codex/harness";
import { startSessionIn, type SessionEngine } from "./sessionDialog";

const ENGINE_ITEMS: { engine: SessionEngine; label: string }[] = [
  { engine: "", label: "Paladin" },
  { engine: "codex", label: "Codex" },
  { engine: "claude", label: "Claude Code" },
];

let menu: HTMLElement | null = null;
let anchor: HTMLElement | null = null;

function closeProjectMenu(restoreFocus = false): void {
  menu?.remove();
  menu = null;
  anchor?.setAttribute("aria-expanded", "false");
  if (restoreFocus) anchor?.focus();
  anchor = null;
}

// engineState says whether an engine can start here and what to tell the user
// when it cannot; the answers are the dialog's, so both always agree.
function engineState(engine: SessionEngine): { enabled: boolean; hint: string; title: string } {
  if (engine === "codex") {
    const { installed, note } = codexAvailability();
    return {
      enabled: installed,
      hint: installed ? "" : "not installed",
      title: installed
        ? "Run the session on your Codex CLI, with Codex's models"
        : note || "Codex was not found on this Mac: install the Codex app or CLI to use it",
    };
  }
  if (engine === "claude") {
    const { installed, note } = claudeAvailability();
    return {
      enabled: installed,
      hint: installed ? "" : "not installed",
      title: installed ? "Run with your Claude Code CLI and subscription" : note || "Install Claude Code to use it",
    };
  }
  // Paladin uses the composer's current model, as the dialog's Paladin button does.
  const model = state.selectedModel ? bare(splitModel(state.selectedModel).short) : "";
  return { enabled: true, hint: model, title: "Run the session on Paladin's own agent and your model providers" };
}

function paintItems(el: HTMLElement): void {
  for (const button of Array.from(el.querySelectorAll<HTMLButtonElement>("[data-new-engine]"))) {
    const engine = button.dataset.newEngine as SessionEngine;
    const { enabled, hint, title } = engineState(engine);
    button.disabled = !enabled;
    button.title = title;
    const small = button.querySelector("small")!;
    small.textContent = hint;
  }
}

function openProjectMenu(cwd: string, trigger: HTMLElement): void {
  closeProjectMenu();
  const el = document.createElement("div");
  el.id = "projectNewMenu";
  el.className = "row-menu";
  el.setAttribute("role", "menu");
  el.setAttribute("aria-label", `New session in ${shortPath(cwd)}`);
  const label = document.createElement("div");
  label.className = "row-menu-label";
  label.textContent = `New session in ${shortPath(cwd)}`;
  el.append(label);
  for (const { engine, label: text } of ENGINE_ITEMS) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "row-menu-item";
    item.setAttribute("role", "menuitem");
    item.dataset.newEngine = engine;
    const name = document.createElement("span");
    name.textContent = text;
    item.append(name, document.createElement("small"));
    item.addEventListener("click", () => {
      closeProjectMenu();
      // In IDE mode the chat is hidden behind the editor; show it, as the
      // New Session button does.
      window.dispatchEvent(new Event("paladin:ide-show-agent"));
      void startSessionIn(cwd, engine).catch((error: unknown) => toast(String(error), "error"));
    });
    el.append(item);
  }
  paintItems(el);
  document.body.append(el);
  const r = trigger.getBoundingClientRect();
  const w = el.offsetWidth;
  const h = el.offsetHeight;
  el.style.left = `${Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w))}px`;
  el.style.top = `${r.bottom + 4 + h > window.innerHeight ? r.top - h - 4 : r.bottom + 4}px`;
  menu = el;
  anchor = trigger;
  trigger.setAttribute("aria-expanded", "true");
  el.querySelector<HTMLButtonElement>("[role=menuitem]:not(:disabled)")?.focus();
  // The CLIs may have been installed since launch, as the dialog re-checks.
  void Promise.all([refreshCodexAvailability(), refreshClaudeAvailability()]).then(() => {
    if (menu === el) paintItems(el);
  });
}

// initProjectNew wires the folder "+" buttons of the sidebar list.
export function initProjectNew(): void {
  sessionListEl.addEventListener("click", (event) => {
    const trigger = (event.target as HTMLElement).closest<HTMLElement>("[data-new-in]");
    if (!trigger?.dataset.newIn) return;
    event.stopPropagation();
    if (menu && anchor === trigger) closeProjectMenu();
    else openProjectMenu(trigger.dataset.newIn, trigger);
  });
  document.addEventListener("mousedown", (event) => {
    const target = event.target as HTMLElement;
    if (menu && !menu.contains(target) && !target.closest("[data-new-in]")) closeProjectMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (!menu) return;
    if (event.key === "Escape") {
      closeProjectMenu(true);
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const items = Array.from(menu.querySelectorAll<HTMLButtonElement>("[role=menuitem]:not(:disabled)"));
    if (items.length === 0) return;
    event.preventDefault();
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === "ArrowDown" ? at + 1 : at - 1;
    items[(next + items.length) % items.length].focus();
  });
}
