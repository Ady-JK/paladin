// slash.ts owns the `/` palette in the composer, styled after Codex:
// one card with Commands (run locally or as special turns), Prompts
// (prompt commands the sidecar expands: /init, /review, custom .md
// files) and Skills. Typing filters fuzzily; arrows move; Enter runs
// the row (or completes it when it needs arguments); Tab completes;
// Esc dismisses.

import * as api from "../core/api";
import { inputEl, messagesEl, modelSelectBtn } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { CommandEntry, SkillEntry } from "../core/types";
import { icon, type IconName } from "../icons";
import { toast } from "../ui/toast";
import { addTurn, renderGreeting } from "./chatView";
import { clearLanded } from "./live";
import { setMode } from "./mode";
import { newSession } from "./sessions";
import { undoLast } from "./undo";
import { forkSessionById, renameSessionById } from "./sessionActions";
import { openSettings } from "./settings";
import { attach as attachSkill, loadSkills, parseSkillLine, skillsSnapshot } from "./skills";
import { openToolsMenu } from "./toolSwitches";
import { openUsageDashboard } from "./usage";
import { askSideQuestion } from "./btw";
import { openBudgetMenu, parseBudget, setSessionBudget } from "./budget";

type Args = "none" | "optional" | "required";

export type SlashCommand = {
  name: string;
  label: string;
  desc: string;
  hint: string;
  icon: IconName;
  args: Args;
  // chevron marks rows that open something (a picker, a pane).
  chevron?: boolean;
};

export const SLASH_COMMANDS: SlashCommand[] = [
  { name: "new", label: "New chat", desc: "Start a fresh session", hint: "/new [first message]", icon: "sl-new", args: "optional" },
  { name: "compact", label: "Compact", desc: "Summarize the conversation to free up context", hint: "/compact", icon: "sl-compact", args: "none" },
  { name: "plan", label: "Plan mode", desc: "Read-only: explore, then propose a plan", hint: "/plan [task]", icon: "sl-plan", args: "optional" },
  { name: "build", label: "Build mode", desc: "Let the agent edit files and run commands", hint: "/build", icon: "sl-build", args: "none" },
  { name: "undo", label: "Undo", desc: "Revert the last message: its file changes and the reply", hint: "/undo", icon: "tl-undo", args: "none" },
  { name: "fork", label: "Fork", desc: "Branch this conversation into a new session", hint: "/fork", icon: "sl-fork", args: "none" },
  { name: "btw", label: "By the way", desc: "Ask a side question; the answer is not added to the chat", hint: "/btw <question>", icon: "comments", args: "required" },
  { name: "rename", label: "Rename", desc: "Rename this session", hint: "/rename <title>", icon: "tl-pencil", args: "required" },
  { name: "memories", label: "Memories", desc: "View and edit what Paladin remembers", hint: "/memories", icon: "sl-memory", args: "none", chevron: true },
  { name: "skill", label: "Skill", desc: "Attach a skill to this turn", hint: "/skill <name> [message]", icon: "sl-skill", args: "required", chevron: true },
  { name: "mcp", label: "MCP servers", desc: "Server status, tools and mcp.json", hint: "/mcp", icon: "sl-plug", args: "none", chevron: true },
  { name: "tools", label: "Tools", desc: "Switch tools on or off for this session", hint: "/tools", icon: "tl-tools", args: "none", chevron: true },
  { name: "models", label: "Model", desc: "Switch the provider and model", hint: "/models", icon: "sl-model", args: "none", chevron: true },
  { name: "budget", label: "Budget", desc: "Set this session's spend limit", hint: "/budget [usd|off|default]", icon: "sl-budget", args: "optional" },
  { name: "usage", label: "Usage", desc: "Tokens, cost and context for this session", hint: "/usage", icon: "sl-usage", args: "none", chevron: true },
  { name: "settings", label: "Settings", desc: "Open settings", hint: "/settings", icon: "rl-gear", args: "none", chevron: true },
  { name: "config", label: "Providers", desc: "Configure providers and API keys", hint: "/config", icon: "sl-sliders", args: "none", chevron: true },
  { name: "clear", label: "Clear view", desc: "Clear the conversation view (history stays)", hint: "/clear", icon: "sl-clear", args: "none" },
  { name: "help", label: "Help", desc: "List every command", hint: "/help", icon: "sl-help", args: "none" },
];

const PROMPT_LABELS: Record<string, string> = { review: "Code review", init: "Init AGENTS.md" };
const PROMPT_ICONS: Record<string, IconName> = { review: "sl-review", init: "sl-init" };

type Kind = "command" | "prompt" | "skill";

type Item = {
  kind: Kind;
  name: string;
  label: string;
  desc: string;
  icon: IconName;
  args: Args;
  chevron: boolean;
  tag: string;
};

// AgentRoute is a slash line that still goes to the agent, with the
// extra submit fields it needs.
export type AgentRoute = {
  message: string;
  display: string;
  command?: string;
  compact?: boolean;
  mode?: "build" | "plan";
  skill?: string;
};

let popup: HTMLElement | null = null;
let filtered: Item[] = [];
let activeIdx = 0;
let open = false;
let debounce = 0;
let prompts: CommandEntry[] | null = null;
let promptsCwd = "";
let runner: () => void = () => {};

// setSlashRunner lets the composer run a completed slash line (chat.ts
// owns send; importing it here would be a cycle).
export function setSlashRunner(run: () => void): void {
  runner = run;
}

// loadPrompts fetches the prompt commands for the working folder once.
export async function loadPrompts(force = false): Promise<CommandEntry[]> {
  if (!force && prompts && promptsCwd === state.cwd) {
    return prompts;
  }
  try {
    prompts = await api.listCommands(state.cwd || null);
    promptsCwd = state.cwd;
  } catch {
    prompts = prompts ?? [];
  }
  return prompts;
}

function promptLabel(name: string): string {
  return PROMPT_LABELS[name] ?? name.replace(/[-_.]+/g, " ").replace(/^./, (c) => c.toUpperCase());
}

function promptArgs(c: CommandEntry): Args {
  const hint = (c.argumentHint || "").trim();
  if (!hint) return "optional";
  return hint.startsWith("<") ? "required" : "optional";
}

function items(): Item[] {
  const out: Item[] = SLASH_COMMANDS.map((c) => ({
    kind: "command" as const,
    name: c.name,
    label: c.label,
    desc: c.desc,
    icon: c.icon,
    args: c.args,
    chevron: !!c.chevron,
    tag: c.hint,
  }));
  const local = new Set(out.map((i) => i.name));
  for (const c of prompts ?? []) {
    if (local.has(c.name)) continue;
    out.push({
      kind: "prompt",
      name: c.name,
      label: promptLabel(c.name),
      desc: c.description || `Prompt from ${c.source}`,
      icon: PROMPT_ICONS[c.name] ?? "sl-prompt",
      args: promptArgs(c),
      chevron: false,
      tag: c.argumentHint ? `/${c.name} ${c.argumentHint}` : `/${c.name}`,
    });
  }
  for (const s of skillsSnapshot()) {
    out.push({
      kind: "skill",
      name: s.name,
      label: s.name,
      desc: s.description || `Skill from ${s.source}`,
      icon: "sl-skill",
      args: "optional",
      chevron: false,
      tag: s.source,
    });
  }
  return out;
}

// score ranks a row for the filter; -1 hides it. Name prefix beats a
// label word prefix, which beats a substring, which beats a fuzzy
// subsequence of the name.
export function score(q: string, name: string, label: string): number {
  if (!q) return 0;
  const n = name.toLowerCase();
  const l = label.toLowerCase();
  if (n.startsWith(q)) return 0;
  if (l.split(/\s+/).some((w) => w.startsWith(q))) return 1;
  if (n.includes(q) || l.includes(q)) return 2;
  let at = 0;
  for (const ch of n) {
    if (ch === q[at]) at++;
    if (at === q.length) return 3;
  }
  return -1;
}

const GROUPS: [Kind, string][] = [
  ["command", "Commands"],
  ["prompt", "Prompts"],
  ["skill", "Skills"],
];

function filter(prefix: string): Item[] {
  const q = prefix.toLowerCase();
  const ranked = items()
    .map((it, i) => ({ it, i, s: score(q, it.name, it.label) }))
    .filter((r) => r.s >= 0);
  const out: Item[] = [];
  for (const [kind] of GROUPS) {
    out.push(
      ...ranked
        .filter((r) => r.it.kind === kind)
        .sort((a, b) => a.s - b.s || a.i - b.i)
        .map((r) => r.it),
    );
  }
  return out;
}

function ensurePopup(): HTMLElement {
  if (popup) {
    return popup;
  }
  const el = document.createElement("div");
  el.id = "slashPopup";
  el.className = "slash-popup hidden";
  el.setAttribute("role", "listbox");
  inputEl.parentElement?.appendChild(el);
  el.addEventListener("mousedown", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    if (row?.dataset.idx !== undefined) {
      event.preventDefault();
      activeIdx = Number(row.dataset.idx);
      runActive(true);
    }
  });
  el.addEventListener("mousemove", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    const idx = Number(row?.dataset.idx ?? -1);
    if (idx >= 0 && idx !== activeIdx) {
      activeIdx = idx;
      paintActive();
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
  const m = /^\/([\w.-]*)$/.exec(value.trimStart());
  return m ? m[1] : null;
}

function closeSlash(): void {
  open = false;
  filtered = [];
  popup?.classList.add("hidden");
}

function rowHtml(it: Item, i: number): string {
  return (
    `<button type="button" role="option" data-idx="${i}" data-kind="${it.kind}" data-name="${escapeHtml(it.name)}" class="slash-row${i === activeIdx ? " is-active" : ""}">` +
    `<span class="slash-icon">${icon(it.icon, "h-4 w-4")}</span>` +
    `<span class="slash-text"><span class="slash-label">${escapeHtml(it.label)}</span>` +
    `<span class="slash-desc">${escapeHtml(it.desc)}</span></span>` +
    `<span class="slash-tag">${escapeHtml(it.kind === "skill" ? it.tag : `/${it.name}`)}</span>` +
    (it.chevron ? `<span class="slash-chev">${icon("rl-chevron", "h-3.5 w-3.5")}</span>` : "") +
    `</button>`
  );
}

function paintSlash(): void {
  const el = ensurePopup();
  if (!open) {
    el.classList.add("hidden");
    return;
  }
  el.classList.remove("hidden");
  if (filtered.length === 0) {
    el.innerHTML = `<div class="slash-empty">No matching commands</div>`;
    return;
  }
  let html = "";
  let group: Kind | "" = "";
  filtered.forEach((it, i) => {
    if (it.kind !== group) {
      group = it.kind;
      html += `<div class="slash-group">${GROUPS.find(([k]) => k === it.kind)?.[1] ?? ""}</div>`;
    }
    html += rowHtml(it, i);
  });
  el.innerHTML = html;
  paintActive();
}

function paintActive(): void {
  if (!popup) return;
  popup.querySelectorAll<HTMLElement>(".slash-row").forEach((row) => {
    const on = Number(row.dataset.idx) === activeIdx;
    row.classList.toggle("is-active", on);
    row.setAttribute("aria-selected", on ? "true" : "false");
    if (on) row.scrollIntoView({ block: "nearest" });
  });
}

function refreshSlash(prefix: string): void {
  filtered = filter(prefix);
  activeIdx = 0;
  paintSlash();
}

function currentPrefix(): string | null {
  return slashPrefix(inputEl.value, inputEl.selectionStart ?? inputEl.value.length);
}

function setComposer(text: string): void {
  inputEl.value = text;
  inputEl.selectionStart = inputEl.value.length;
  inputEl.selectionEnd = inputEl.value.length;
  inputEl.dispatchEvent(new Event("input"));
  inputEl.focus();
}

// complete writes the row into the composer for its arguments.
function complete(it: Item): void {
  closeSlash();
  if (it.kind === "skill") {
    setComposer(`/skill ${it.name} `);
    attachSkill(it.name);
    return;
  }
  setComposer(`/${it.name} `);
}

// runActive runs (or completes, when arguments are required) the row.
function runActive(run: boolean): void {
  const it = filtered[Math.min(Math.max(activeIdx, 0), filtered.length - 1)];
  if (!it) {
    return;
  }
  if (!run || it.args === "required" || it.kind === "skill") {
    complete(it);
    return;
  }
  closeSlash();
  inputEl.value = `/${it.name}`;
  runner();
}

// acceptSlash completes the row into the composer.
export function acceptSlash(idx: number): boolean {
  if (!open || filtered.length === 0) {
    return false;
  }
  activeIdx = idx;
  runActive(false);
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
    event.stopPropagation();
    closeSlash();
    return true;
  }
  // The filter repaints on a debounce; act on what is typed now.
  const prefix = currentPrefix();
  if (prefix === null) {
    closeSlash();
    return false;
  }
  if (["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key)) {
    window.clearTimeout(debounce);
    const before = filtered.map((i) => i.name).join(",");
    const next = filter(prefix);
    if (next.map((i) => i.name).join(",") !== before) {
      filtered = next;
      activeIdx = 0;
      paintSlash();
    }
  }
  if (filtered.length === 0) {
    return false;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    activeIdx = (activeIdx + 1) % filtered.length;
    paintActive();
    return true;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    activeIdx = (activeIdx - 1 + filtered.length) % filtered.length;
    paintActive();
    return true;
  }
  if ((event.key === "Enter" && !event.shiftKey) || event.key === "Tab") {
    event.preventDefault();
    runActive(event.key === "Enter");
    return true;
  }
  return false;
}

// slashHelp lists every command, prompt and skill.
function slashHelp(): string {
  const lines = ["Commands:", ...SLASH_COMMANDS.map((c) => `  ${c.hint} — ${c.desc}`)];
  if (prompts?.length) {
    lines.push("", "Prompts:", ...prompts.map((c) => `  /${c.name}${c.argumentHint ? ` ${c.argumentHint}` : ""} — ${c.description}`));
  }
  const skills = skillsSnapshot();
  if (skills.length) {
    lines.push("", "Skills (/skill <name> or /<name>):", ...skills.map((s: SkillEntry) => `  ${s.name} — ${s.description || s.source}`));
  }
  return lines.join("\n");
}

function needSession(what: string): boolean {
  if (!state.sessionId) {
    toast(`Open a session to ${what}`, "error");
    return false;
  }
  return true;
}

async function forkCurrent(): Promise<void> {
  if (!needSession("fork it")) return;
  await forkSessionById(state.sessionId);
}

async function renameCurrent(title: string): Promise<void> {
  if (!needSession("rename it")) return;
  await renameSessionById(state.sessionId, title);
}

// routeSlash handles a `/` line. "local" means it ran here and must
// not reach the agent; "none" means it is not a slash line; an object
// is a turn for the agent with extra submit fields.
export async function routeSlash(message: string): Promise<"local" | "none" | AgentRoute> {
  const parsed = parseSlashLine(message);
  if (!parsed) {
    return "none";
  }
  const { trimmed, name, rest } = parsed;
  const clear = () => {
    inputEl.value = "";
  };
  switch (name) {
    case "skill":
      // chat.ts splits `/skill <name> [message]` and sends the name
      // separately. A bare `/skill` names nothing: hint instead.
      if (!parseSkillLine(message)) {
        setComposer("/skill ");
        toast("Usage: /skill <name> [message] — pick one from the list", "error");
        return "local";
      }
      return "none";
    case "new":
      newSession();
      inputEl.value = rest;
      toast(rest ? "New session started" : "New session");
      return "local";
    case "compact":
      if (!needSession("compact it")) return "local";
      clear();
      return { message: "", display: "/compact", compact: true };
    case "plan":
      setMode("plan");
      clear();
      if (!rest) {
        toast("Plan mode — the agent stays read-only");
        return "local";
      }
      return { message: rest, display: rest, mode: "plan" };
    case "build":
      setMode("build");
      clear();
      toast("Build mode");
      return "local";
    case "fork":
      clear();
      void forkCurrent();
      return "local";
    case "undo":
      clear();
      void undoLast();
      return "local";
    case "btw":
      clear();
      if (!rest) {
        setComposer("/btw ");
        toast("Usage: /btw <question>", "error");
        return "local";
      }
      if (!needSession("ask a side question")) return "local";
      void askSideQuestion(rest);
      return "local";
    case "rename":
      clear();
      if (!rest) {
        setComposer("/rename ");
        toast("Usage: /rename <title>", "error");
        return "local";
      }
      void renameCurrent(rest);
      return "local";
    case "memories":
    case "memory":
      clear();
      openSettings("memory");
      return "local";
    case "mcp":
      clear();
      openSettings("mcp");
      return "local";
    case "tools":
      clear();
      openToolsMenu();
      return "local";
    case "models":
      clear();
      modelSelectBtn.click();
      return "local";
    case "settings":
      clear();
      openSettings("workspace");
      return "local";
    case "config":
      clear();
      openSettings("providers");
      return "local";
    case "budget": {
      clear();
      if (!rest) {
        openBudgetMenu();
        return "local";
      }
      const v = parseBudget(rest);
      if (v === undefined) {
        setComposer("/budget ");
        toast("Usage: /budget <usd> | off | default", "error");
        return "local";
      }
      void setSessionBudget(v);
      return "local";
    }
    case "usage":
      clear();
      void openUsageDashboard();
      return "local";
    case "clear":
      clear();
      messagesEl.innerHTML = "";
      clearLanded(state.sessionId);
      renderGreeting();
      return "local";
    case "help":
      clear();
      await loadPrompts();
      addTurn("user", message.trim(), { revertable: false });
      addTurn("agent", slashHelp());
      return "local";
  }
  return routeSidecarCommand(name, rest, trimmed);
}

// parseSlashLine splits a `/name args` line. Arguments keep later
// lines: `/review\nfocus on auth` passes both.
function parseSlashLine(message: string): { trimmed: string; name: string; rest: string } | null {
  const trimmed = message.trimStart();
  if (!trimmed.startsWith("/")) {
    return null;
  }
  const firstLine = trimmed.split("\n", 1)[0];
  const space = firstLine.search(/\s/);
  const name = (space < 0 ? firstLine : firstLine.slice(0, space)).slice(1).toLowerCase();
  const rest = (space < 0 ? trimmed.slice(firstLine.length) : trimmed.slice(space)).trim();
  return { trimmed, name, rest };
}

// routeSidecarCommand resolves a name that is not built in: a prompt
// command or a skill from the sidecar. The prompt list refreshes once
// before the name is called unknown.
async function routeSidecarCommand(name: string, rest: string, trimmed: string): Promise<"local" | AgentRoute> {
  let prompt = (await loadPrompts()).find((c) => c.name.toLowerCase() === name);
  if (!prompt) {
    prompt = (await loadPrompts(true)).find((c) => c.name.toLowerCase() === name);
  }
  if (prompt) {
    if (promptArgs(prompt) === "required" && !rest) {
      setComposer(`/${prompt.name} `);
      toast(`Usage: /${prompt.name} ${prompt.argumentHint}`, "error");
      return "local";
    }
    inputEl.value = "";
    const route: AgentRoute = { message: rest, display: trimmed, command: prompt.name };
    if (state.mode === "plan") route.mode = "plan";
    return route;
  }
  const skill = (await loadSkills()).find((s) => s.name.toLowerCase() === name);
  if (skill) {
    inputEl.value = "";
    return { message: rest, display: rest || `/skill ${skill.name}`, skill: skill.name };
  }
  toast(`Unknown command /${name} — try /help`, "error");
  return "local";
}

function onInput(): void {
  const prefix = currentPrefix();
  if (prefix === null) {
    closeSlash();
    return;
  }
  const opening = !open;
  open = true;
  ensurePopup();
  if (opening) {
    // Paint from cache at once; refresh prompts and skills behind it.
    refreshSlash(prefix);
    void Promise.all([loadPrompts(), loadSkills()]).then(() => {
      const p = currentPrefix();
      if (open && p !== null) {
        const keep = filtered[activeIdx]?.name;
        filtered = filter(p);
        const at = filtered.findIndex((i) => i.name === keep);
        activeIdx = at >= 0 ? at : 0;
        paintSlash();
      }
    });
    return;
  }
  window.clearTimeout(debounce);
  debounce = window.setTimeout(() => refreshSlash(prefix), 40);
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
