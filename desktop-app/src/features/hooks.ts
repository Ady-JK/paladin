// hooks.ts owns Settings → Hooks: the global and folder hook files
// (Paladin's own plus the folder's Codex and Claude ones), per-handler
// switches, folder trust and the hooks.json editor.

import * as api from "../core/api";
import { hooksBodyEl } from "../core/dom";
import { escapeHtml, shortPath } from "../core/format";
import { state } from "../core/state";
import type { HookHandler, HookScope, HookSource, HooksOverview } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";

const STARTER = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "echo checked" }]
      }
    ]
  }
}
`;

const FORMAT_LABEL: Record<HookSource["format"], string> = {
  paladin: "Paladin",
  codex: "Codex",
  claude: "Claude Code",
};

type Editor = { scope: HookScope; text: string; error: string; saving: boolean };

let overview: HooksOverview | null = null;
let loadedCwd = "";
let loading = 0;
let editor: Editor | null = null;
const busy = new Set<string>();
let wired = false;

const cwdArg = () => loadedCwd || null;

// loadHooks fetches the working folder's hook files and repaints.
export async function loadHooks(): Promise<void> {
  wire();
  const mine = ++loading;
  const cwd = state.cwd;
  if (cwd !== loadedCwd) editor = null;
  if (!overview || cwd !== loadedCwd) {
    hooksBodyEl.innerHTML = `<div class="mem-empty">Loading hooks…</div>`;
  }
  try {
    const v = await api.hooksList(cwd || null);
    if (mine !== loading) return;
    loadedCwd = cwd;
    overview = v;
    paint();
  } catch (error) {
    if (mine !== loading) return;
    hooksBodyEl.innerHTML = `<div class="mem-empty mem-error">Could not load hooks: ${escapeHtml(String(error))}</div>`;
  }
}

// ---- pane ----

function handlerRow(src: HookSource, h: HookHandler): string {
  const pending = busy.has(h.key);
  const live = h.enabled && src.trusted;
  const matcher = h.matcher && h.matcher !== "*" ? `<code class="hook-matcher" title="Matcher">${escapeHtml(h.matcher)}</code>` : "";
  const timeout = h.timeout ? `<span class="mcp-meta">${h.timeout}s</span>` : "";
  return (
    `<li class="hook-handler${pending ? " is-busy" : ""}" data-hook-key="${escapeHtml(h.key)}" data-live="${live}">` +
    `<span class="mcp-badge hook-event">${escapeHtml(h.event)}</span>` +
    matcher +
    `<code class="hook-command" title="${escapeHtml(h.command)}">${escapeHtml(h.command)}</code>` +
    timeout +
    `<button type="button" class="mcp-switch" role="switch" data-hook-toggle aria-checked="${h.enabled}" ` +
    `aria-label="${h.enabled ? "Disable" : "Enable"} ${escapeHtml(h.event)} hook" title="${h.enabled ? "Disable" : "Enable"}"${pending ? " disabled" : ""}><span></span></button>` +
    `</li>`
  );
}

function editorHtml(e: Editor, path: string): string {
  return (
    `<div class="mcp-editor hook-editor" data-scope="${e.scope}">` +
    `<div class="mcp-editor-path" title="${escapeHtml(path)}">${escapeHtml(shortPath(path))}</div>` +
    `<textarea spellcheck="false" aria-label="${e.scope} hooks.json">${escapeHtml(e.text)}</textarea>` +
    (e.error ? `<div class="mcp-editor-error">${escapeHtml(e.error)}</div>` : "") +
    `<div class="mcp-editor-actions">` +
    `<span class="mcp-editor-hint">Same format as Codex and Claude Code · exit 2 blocks · empty file removes it</span>` +
    `<button type="button" class="mcp-btn" data-hook-cancel>Cancel</button>` +
    `<button type="button" class="mcp-btn is-primary" data-hook-save${e.saving ? " disabled" : ""}>${e.saving ? "Saving…" : "Save"}</button>` +
    `</div></div>`
  );
}

function sourceCard(src: HookSource): string {
  const handlers = src.handlers ?? [];
  const folder = src.scope === "folder";
  let trust = "";
  if (folder && src.exists && handlers.length) {
    trust = src.trusted
      ? `<button type="button" class="mcp-trusted" data-hook-revoke title="Stop running this file's hooks">${icon("sl-shield-check", "h-3.5 w-3.5")}Trusted</button>`
      : `<button type="button" class="mcp-btn is-primary" data-hook-trust>Trust</button>`;
  }
  const edit = src.editable
    ? `<button type="button" class="mem-refresh" data-hook-edit="${src.scope}">${icon("tl-pencil", "h-3.5 w-3.5")}${src.exists ? "Edit" : "Add"}</button>`
    : "";
  const untrusted =
    folder && src.exists && handlers.length && !src.trusted
      ? `<div class="hook-untrusted">${icon("sl-shield", "h-3.5 w-3.5")}<span>These hooks run commands on your machine, outside the sandbox, so they stay off until you trust this file. Trust is pinned to its current content.</span></div>`
      : "";
  const error = src.error ? `<div class="mcp-config-error">${icon("sl-shield-off", "h-3.5 w-3.5")}<span>${escapeHtml(src.error)}</span></div>` : "";
  const warnings = (src.warnings ?? []).map((w) => `<div class="hook-warning">${escapeHtml(w)}</div>`).join("");
  const body = editor?.scope === src.scope && src.editable ? editorHtml(editor, src.path) : "";
  const list = handlers.length
    ? `<ul class="hook-list">${handlers.map((h) => handlerRow(src, h)).join("")}</ul>`
    : src.exists && !src.error
      ? `<div class="mem-none">No command hooks in this file.</div>`
      : "";
  return (
    `<div class="hook-source" data-hook-path="${escapeHtml(src.path)}" data-format="${src.format}" data-trusted="${src.trusted}">` +
    `<div class="hook-source-head">` +
    `<span class="hook-format">${FORMAT_LABEL[src.format]}</span>` +
    `<code class="hook-path" title="${escapeHtml(src.path)}">${escapeHtml(shortPath(src.path))}</code>` +
    `<span class="mcp-head-actions">${trust}${edit}</span>` +
    `</div>` +
    untrusted +
    error +
    warnings +
    body +
    list +
    `</div>`
  );
}

function section(scope: HookScope, o: HooksOverview): string {
  // Only Paladin's own file shows when missing (so it can be added);
  // Codex and Claude files show once they exist.
  const sources = o.sources.filter((s) => s.scope === scope && (s.exists || s.editable));
  const title =
    scope === "global"
      ? `<div class="mem-title">Everywhere</div><div class="mem-blurb">Run in every folder.</div>`
      : `<div class="mem-title">This folder <span class="mem-cwd" title="${escapeHtml(loadedCwd)}">${escapeHtml(shortPath(loadedCwd))}</span></div>` +
        `<div class="mem-blurb">Paladin, Codex and Claude Code hook files in this folder. Each needs your trust before it runs.</div>`;
  return (
    `<section class="mem-section" data-hook-section="${scope}">` +
    `<div class="mem-head"><div>${title}</div></div>` +
    sources.map(sourceCard).join("") +
    `</section>`
  );
}

function paint(): void {
  if (!overview) return;
  const area = hooksBodyEl.querySelector<HTMLTextAreaElement>(".hook-editor textarea");
  const sel = area ? [area.selectionStart, area.selectionEnd, area.scrollTop] : null;
  const focused = !!area && document.activeElement === area;
  hooksBodyEl.innerHTML =
    `<div class="mem-intro"><span>Hooks run your own commands before or after a tool call, when you send a message, when a turn ends and around compaction. They run outside the sandbox.</span>` +
    `<span class="mcp-head-actions"><button type="button" class="mem-refresh" data-hook-refresh>${icon("repeat", "h-3.5 w-3.5")}Refresh</button></span></div>` +
    section("global", overview) +
    (loadedCwd ? section("folder", overview) : "");
  const next = hooksBodyEl.querySelector<HTMLTextAreaElement>(".hook-editor textarea");
  if (next && sel) {
    next.setSelectionRange(sel[0], sel[1]);
    next.scrollTop = sel[2];
    if (focused) next.focus();
  }
}

// ---- actions ----

const errText = (e: unknown) => String(e instanceof Error ? e.message : e).replace(/^Error:\s*/, "");

async function act(key: string, run: () => Promise<HooksOverview>, what: string): Promise<void> {
  busy.add(key);
  paint();
  try {
    overview = await run();
  } catch (error) {
    toast(`Could not ${what}: ${errText(error)}`, "error");
  } finally {
    busy.delete(key);
    paint();
  }
}

function sourceOf(el: HTMLElement): HookSource | undefined {
  const path = el.closest<HTMLElement>("[data-hook-path]")?.dataset.hookPath;
  return overview?.sources.find((s) => s.path === path);
}

async function save(): Promise<void> {
  const e = editor;
  if (!e || e.saving) return;
  e.text = hooksBodyEl.querySelector<HTMLTextAreaElement>(".hook-editor textarea")?.value ?? e.text;
  e.saving = true;
  e.error = "";
  paint();
  try {
    overview = await api.hooksSetConfig(e.scope, cwdArg(), e.text);
    if (editor === e) editor = null;
    toast(e.text.trim() ? "Hooks saved" : "Hooks file removed", "ok");
  } catch (error) {
    e.error = errText(error);
  } finally {
    e.saving = false;
    paint();
  }
}

function wire(): void {
  if (wired) return;
  wired = true;
  hooksBodyEl.addEventListener("input", (ev) => {
    const t = ev.target as HTMLElement;
    if (editor && t.matches(".hook-editor textarea")) editor.text = (t as HTMLTextAreaElement).value;
  });
  hooksBodyEl.addEventListener("click", (ev) => {
    const t = (ev.target as HTMLElement).closest<HTMLButtonElement>("button");
    if (!t || t.disabled) return;
    if (t.matches("[data-hook-refresh]")) {
      void loadHooks();
    } else if (t.matches("[data-hook-toggle]")) {
      const key = t.closest<HTMLElement>("[data-hook-key]")?.dataset.hookKey;
      if (!key) return;
      const on = t.getAttribute("aria-checked") !== "true";
      void act(key, () => api.hooksToggle(cwdArg(), key, on), `${on ? "enable" : "disable"} the hook`);
    } else if (t.matches("[data-hook-trust], [data-hook-revoke]")) {
      const src = sourceOf(t);
      if (!src || !loadedCwd) return;
      const trust = t.matches("[data-hook-trust]");
      void act(src.path, () => api.hooksTrust(loadedCwd, src.path, trust), trust ? "trust the hooks" : "revoke trust");
    } else if (t.matches("[data-hook-edit]")) {
      const src = sourceOf(t);
      if (!src) return;
      editor = { scope: src.scope, text: src.text || (src.exists ? "" : STARTER), error: "", saving: false };
      paint();
      hooksBodyEl.querySelector<HTMLTextAreaElement>(".hook-editor textarea")?.focus();
    } else if (t.matches("[data-hook-cancel]")) {
      editor = null;
      paint();
    } else if (t.matches("[data-hook-save]")) {
      void save();
    }
  });
  hooksBodyEl.addEventListener("keydown", (ev) => {
    if ((ev.metaKey || ev.ctrlKey) && ev.key === "s" && (ev.target as HTMLElement).matches(".hook-editor textarea")) {
      ev.preventDefault();
      void save();
    }
  });
}
