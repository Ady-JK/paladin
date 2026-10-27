// slash.ts owns `/` commands in the composer.
//
// A message whose first token is a known command never reaches the
// agent: it runs locally (new session, open settings, usage, clear,
// help…). Typing `/` shows the command popup; Enter/Tab accepts,
// Esc dismisses.

import { inputEl, messagesEl, modelSelectBtn } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { addTurn, renderGreeting } from "./chatView";
import { newSession } from "./sessions";
import { openSettings } from "./settings";
import { openUsageDashboard } from "./usage";

export type SlashCommand = {
  name: string;
  desc: string;
  hint: string;
};

export const SLASH_COMMANDS: SlashCommand[] = [
  { name: "new", desc: "Start a fresh session", hint: "/new [first message]" },
  { name: "models", desc: "Open the model picker", hint: "/models" },
  { name: "settings", desc: "Open settings", hint: "/settings" },
  { name: "config", desc: "Open provider config", hint: "/config" },
  { name: "usage", desc: "Open session analytics", hint: "/usage" },
  { name: "clear", desc: "Clear the conversation view", hint: "/clear" },
  { name: "help", desc: "List slash commands", hint: "/help" },
];

let popup: HTMLElement | null = null;
let filtered: SlashCommand[] = [];
let activeIdx = 0;
let open = false;
let debounce = 0;

function ensurePopup(): HTMLElement {
  if (popup) {
    return popup;
  }
  const el = document.createElement("div");
  el.id = "slashPopup";
  el.className =
    "absolute bottom-full left-0 z-50 mb-1 hidden max-h-56 w-80 overflow-y-auto rounded-xl border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-900 dark:shadow-black/50";
  inputEl.parentElement?.appendChild(el);
  el.addEventListener("mousedown", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    if (row?.dataset.idx !== undefined) {
      event.preventDefault();
      acceptSlash(Number(row.dataset.idx));
    }
  });
  popup = el;
  return el;
}

// slashPrefix returns the command filter when the whole composer is
// a `/` prefix with the caret at the end.
export function slashPrefix(value: string, caret: number): string | null {
  if (caret !== value.length || value.includes("\n")) {
    return null;
  }
  const m = /^\/([a-z]*)$/.exec(value.trimStart());
  if (!m || !value.trimStart().startsWith("/")) {
    return null;
  }
  return m[1];
}

function closeSlash(): void {
  open = false;
  filtered = [];
  popup?.classList.add("hidden");
}

function paintSlash(): void {
  const el = ensurePopup();
  if (!open || filtered.length === 0) {
    el.classList.add("hidden");
    return;
  }
  el.classList.remove("hidden");
  el.innerHTML = filtered
    .map(
      (cmd, i) => `<button data-idx="${i}" class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left ${
        i === activeIdx
          ? "bg-accent-600/10 text-slate-900 dark:text-slate-100"
          : "text-slate-600 dark:text-slate-300"
      }"><span class="flex-shrink-0 font-mono text-[11px] font-semibold text-accent-500">/${escapeHtml(cmd.name)}</span><span class="min-w-0 flex-1 truncate text-[11px]">${escapeHtml(cmd.desc)}</span></button>`,
    )
    .join("");
}

function refreshSlash(prefix: string): void {
  filtered = SLASH_COMMANDS.filter((c) => c.name.startsWith(prefix));
  activeIdx = 0;
  paintSlash();
}

function scheduleRefresh(prefix: string): void {
  window.clearTimeout(debounce);
  debounce = window.setTimeout(() => refreshSlash(prefix), 60);
}

// acceptSlash completes the row into the composer.
export function acceptSlash(idx: number): boolean {
  if (!open || filtered.length === 0) {
    return false;
  }
  const cmd = filtered[Math.min(Math.max(idx, 0), filtered.length - 1)];
  inputEl.value = `/${cmd.name} `;
  inputEl.selectionStart = inputEl.value.length;
  inputEl.selectionEnd = inputEl.value.length;
  closeSlash();
  inputEl.focus();
  return true;
}

// handleSlashKey consumes popup navigation keys. Returns true when
// the composer must ignore the keystroke.
export function handleSlashKey(event: KeyboardEvent): boolean {
  if (!open) {
    return false;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    closeSlash();
    return true;
  }
  if (filtered.length === 0) {
    return false;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    activeIdx = (activeIdx + 1) % filtered.length;
    paintSlash();
    return true;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    activeIdx = (activeIdx - 1 + filtered.length) % filtered.length;
    paintSlash();
    return true;
  }
  if (event.key === "Enter" || event.key === "Tab") {
    event.preventDefault();
    acceptSlash(activeIdx);
    return true;
  }
  return false;
}

function slashHelp(): string {
  return `Slash commands:\n${SLASH_COMMANDS.map((c) => `${c.hint} — ${c.desc}`).join("\n")}`;
}

// tryHandleSlash runs a `/` command locally. Returns true when the
// message was consumed and must not reach the agent.
export function tryHandleSlash(message: string): boolean {
  const trimmed = message.trimStart();
  if (!trimmed.startsWith("/")) {
    return false;
  }
  const firstLine = trimmed.split("\n", 1)[0];
  const space = firstLine.search(/\s/);
  const cmd = (space < 0 ? firstLine : firstLine.slice(0, space)).toLowerCase();
  const rest = (space < 0 ? "" : firstLine.slice(space).trim()) || "";
  const known = SLASH_COMMANDS.some((c) => `/${c.name}` === cmd);
  if (!known) {
    toast(`Unknown command ${cmd} — try /help`, "error");
    inputEl.value = "";
    return true;
  }
  switch (cmd) {
    case "/new":
      newSession();
      inputEl.value = rest;
      toast(rest ? "New session started" : "New session");
      break;
    case "/models":
      inputEl.value = "";
      modelSelectBtn.click();
      break;
    case "/settings":
      inputEl.value = "";
      openSettings("workspace");
      break;
    case "/config":
      inputEl.value = "";
      openSettings("providers");
      break;
    case "/usage":
      inputEl.value = "";
      void openUsageDashboard();
      break;
    case "/clear":
      inputEl.value = "";
      messagesEl.innerHTML = "";
      state.shownMessages = 0;
      renderGreeting();
      break;
    case "/help":
      inputEl.value = "";
      addTurn("user", message.trim());
      state.shownMessages++;
      addTurn("agent", slashHelp());
      state.shownMessages++;
      break;
    default:
      return false;
  }
  return true;
}

function onInput(): void {
  const caret = inputEl.selectionStart ?? inputEl.value.length;
  const prefix = slashPrefix(inputEl.value, caret);
  if (prefix === null) {
    closeSlash();
    return;
  }
  open = true;
  ensurePopup();
  scheduleRefresh(prefix);
}

// initSlash wires the composer listener. Key handling lives in
// chat.ts (ordering matters) via handleSlashKey.
export function initSlash(): void {
  inputEl.addEventListener("input", onInput);
  inputEl.addEventListener("click", onInput);
  inputEl.addEventListener("blur", () => {
    window.setTimeout(closeSlash, 120);
  });
}
