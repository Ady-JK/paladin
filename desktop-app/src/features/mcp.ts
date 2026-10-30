// mcp.ts owns MCP in the renderer: the Settings → MCP pane (server
// cards, folder trust, the mcp.json editor), the composer chip that
// flags failed or untrusted servers, and live mcp/status updates.

import * as api from "../core/api";
import { mcpBodyEl, modelSelectBtn, settingsNavMcpEl } from "../core/dom";
import { escapeHtml, shortPath } from "../core/format";
import { state } from "../core/state";
import type { MCPNamedServer, MCPOverview, MCPScope, MCPServerStatus, MCPState } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";

const STATE_LABEL: Record<MCPState, string> = {
  ready: "Ready",
  starting: "Starting…",
  stopped: "Idle · starts on first use",
  failed: "Failed",
  disabled: "Disabled",
  untrusted: "Waiting for trust",
  removed: "Removed",
};

type Editor = { scope: MCPScope; path: string; text: string; error: string; saving: boolean };
// Paste is the "Add server" box: pasted text, then an editable preview
// of what mcpParse made of it.
type Paste = {
  text: string;
  servers: MCPNamedServer[] | null;
  scope: MCPScope;
  error: string;
  exists: boolean;
  busy: boolean;
};

let overview: MCPOverview | null = null;
let loadedCwd = "";
let loading = 0;
let editor: Editor | null = null;
let paste: Paste | null = null;
const busy = new Set<string>();
let refreshTimer = 0;
let chip: HTMLButtonElement | null = null;
let openPane: () => void = () => {};

const keyOf = (s: { scope: string; name: string }) => `${s.scope}:${s.name}`;
const cwdArg = () => loadedCwd || null;

function failed(o: MCPOverview | null): MCPServerStatus[] {
  return (o?.servers ?? []).filter((s) => s.state === "failed" && !s.shadowed);
}

function needsTrust(o: MCPOverview | null): boolean {
  return !!o && o.hasFolderConfig && !o.trusted;
}

// loadMcp fetches the overview for the working folder and repaints.
export async function loadMcp(): Promise<void> {
  const mine = ++loading;
  const cwd = state.cwd;
  if (cwd !== loadedCwd) editor = null;
  if (!overview || cwd !== loadedCwd) {
    mcpBodyEl.innerHTML = `<div class="mem-empty">Loading MCP servers…</div>`;
  }
  try {
    const v = await api.mcpList(cwd || null);
    if (mine !== loading) return;
    loadedCwd = cwd;
    apply(v);
  } catch (error) {
    if (mine !== loading) return;
    mcpBodyEl.innerHTML = `<div class="mem-empty mem-error">Could not load MCP servers: ${escapeHtml(String(error))}</div>`;
  }
}

// syncMcp reloads when the working folder changed since the last load.
export function syncMcp(): void {
  if (state.cwd !== loadedCwd || !overview) void loadMcp();
}

function apply(v: MCPOverview): void {
  overview = v;
  paintPane();
  paintChip();
}

// ---- live updates ----

function onStatus(srv: MCPServerStatus): void {
  if (!overview) return;
  if (srv.scope === "folder" && srv.folder && srv.folder !== loadedCwd) return;
  const at = overview.servers.findIndex((s) => keyOf(s) === keyOf(srv));
  if (srv.state === "removed") {
    if (at >= 0) overview.servers.splice(at, 1);
  } else if (at >= 0) {
    // Shadowing is decided across files; keep it until the refresh below.
    overview.servers[at] = { ...srv, shadowed: overview.servers[at].shadowed };
  } else {
    overview.servers.push(srv);
  }
  paintPane();
  paintChip();
  window.clearTimeout(refreshTimer);
  refreshTimer = window.setTimeout(() => void loadMcp(), 400);
}

// ---- pane ----

function commandLine(c: MCPNamedServer["config"]): string {
  if (c.url) return c.url;
  return [c.command ?? "", ...(c.args ?? [])].map((a) => (/\s/.test(a) ? JSON.stringify(a) : a)).join(" ");
}

function pasteHtml(p: Paste): string {
  const err = p.error
    ? `<div class="mcp-editor-error">${escapeHtml(p.error)}${p.exists ? ` <button type="button" class="mcp-btn is-danger" data-mcp-replace>Replace</button>` : ""}</div>`
    : "";
  if (!p.servers) {
    return (
      `<div class="mcp-paste mcp-editor">` +
      `<div class="mcp-paste-title">${icon("sl-plug", "h-3.5 w-3.5")}Add an MCP server</div>` +
      `<textarea data-paste-text spellcheck="false" rows="4" aria-label="Server command" placeholder="Paste a command (npx -y @scope/server, uvx mcp-server-git, docker run -i …), a URL, or an mcp.json snippet">${escapeHtml(p.text)}</textarea>` +
      err +
      `<div class="mcp-editor-actions"><span class="mcp-editor-hint">One server per line · <code>KEY=value</code> before a command sets env · <code>claude mcp add …</code> works too</span>` +
      `<button type="button" class="mcp-btn" data-mcp-paste-cancel>Cancel</button>` +
      `<button type="button" class="mcp-btn is-primary" data-mcp-parse${p.busy || !p.text.trim() ? " disabled" : ""}>Preview</button></div>` +
      `</div>`
    );
  }
  const rows = p.servers
    .map((srv, i) => {
      const env = Object.entries(srv.config.env ?? {})
        .map(
          ([k, v]) =>
            `<label class="mcp-preview-env"><code>${escapeHtml(k)}</code><input data-field="env" data-key="${escapeHtml(k)}" type="${/token|key|secret|pass|auth/i.test(k) ? "password" : "text"}" value="${escapeHtml(v)}" spellcheck="false" autocomplete="off" /></label>`,
        )
        .join("");
      return (
        `<li class="mcp-preview" data-index="${i}">` +
        `<div class="mcp-preview-head"><input data-field="name" value="${escapeHtml(srv.name)}" aria-label="Server name" spellcheck="false" autocomplete="off" />` +
        `<span class="mcp-badge">${srv.config.url ? "http" : "stdio"}</span>` +
        (p.servers!.length > 1
          ? `<button type="button" class="mcp-icon-btn" data-mcp-drop title="Leave out" aria-label="Leave out ${escapeHtml(srv.name)}">${icon("xmark", "h-3 w-3")}</button>`
          : "") +
        `</div>` +
        `<code class="mcp-preview-cmd">${escapeHtml(commandLine(srv.config))}</code>` +
        env +
        `</li>`
      );
    })
    .join("");
  const folderOpt = loadedCwd
    ? `<option value="folder"${p.scope === "folder" ? " selected" : ""}>This folder (${escapeHtml(shortPath(loadedCwd))})</option>`
    : "";
  return (
    `<div class="mcp-paste mcp-editor">` +
    `<div class="mcp-paste-title">${icon("sl-plug", "h-3.5 w-3.5")}Add ${p.servers.length} MCP ${p.servers.length === 1 ? "server" : "servers"}</div>` +
    `<ul class="mcp-preview-list">${rows}</ul>` +
    err +
    `<div class="mcp-editor-actions">` +
    `<label class="mcp-paste-scope">Save to <select data-field="scope" aria-label="Where to save"><option value="global"${p.scope === "global" ? " selected" : ""}>Everywhere</option>${folderOpt}</select></label>` +
    `<button type="button" class="mcp-btn" data-mcp-paste-back>Back</button>` +
    `<button type="button" class="mcp-btn is-primary" data-mcp-add${p.busy ? " disabled" : ""}>${p.busy ? "Adding…" : "Add"}</button>` +
    `</div></div>`
  );
}

function toolChips(s: MCPServerStatus): string {
  if (!s.tools.length) return "";
  const chips = s.tools
    .map((t) => {
      const mark = t.destructive
        ? "destructive, always asks"
        : t.readOnly
          ? "server says read-only; still asks"
          : "asks first";
      const title = `${t.exposed}${t.description ? ` — ${t.description}` : ""} (${mark})`;
      return `<span class="mcp-tool" data-ro="${t.readOnly}" data-destructive="${t.destructive}" title="${escapeHtml(title)}">${escapeHtml(t.name)}</span>`;
    })
    .join("");
  return `<div class="mcp-tools">${chips}</div>`;
}

function serverCard(s: MCPServerStatus): string {
  const on = s.state !== "disabled";
  const key = keyOf(s);
  const pending = busy.has(key);
  const impl = s.server?.name ? `${s.server.name}${s.server.version ? ` ${s.server.version}` : ""}` : "";
  const meta = [s.transport, impl].filter(Boolean).map(escapeHtml).join(" · ");
  const count = s.state === "ready" ? ` · ${s.tools.length} ${s.tools.length === 1 ? "tool" : "tools"}` : "";
  const restarts = s.restarts > 0 ? `<span class="mcp-badge">restarted ${s.restarts}×</span>` : "";
  const shadow = s.shadowed ? `<span class="mcp-badge is-muted">Replaced by this folder's server</span>` : "";
  const canRestart = on && s.state !== "untrusted";
  const restart = canRestart
    ? `<button type="button" class="mcp-icon-btn" data-mcp-restart title="Restart ${escapeHtml(s.name)}" aria-label="Restart ${escapeHtml(s.name)}"${pending ? " disabled" : ""}>${icon("repeat", "h-3.5 w-3.5")}</button>`
    : "";
  const toggle =
    `<button type="button" class="mcp-switch" role="switch" data-mcp-toggle aria-checked="${on}" ` +
    `aria-label="${on ? "Disable" : "Enable"} ${escapeHtml(s.name)}" title="${on ? "Disable" : "Enable"}"${pending ? " disabled" : ""}><span></span></button>`;
  const error = s.error ? `<div class="mcp-error">${escapeHtml(s.error)}</div>` : "";
  const log = s.stderr
    ? `<details class="mcp-log"><summary>Server log</summary><pre>${escapeHtml(s.stderr)}</pre></details>`
    : "";
  return (
    `<li class="mcp-server${pending ? " is-busy" : ""}" data-mcp-server="${escapeHtml(s.name)}" data-scope="${s.scope}" data-state="${s.state}">` +
    `<div class="mcp-server-head">` +
    `<span class="mcp-dot" aria-hidden="true"></span>` +
    `<span class="mcp-name">${escapeHtml(s.name)}</span>` +
    (meta ? `<span class="mcp-meta">${meta}</span>` : "") +
    `<span class="mcp-state">${STATE_LABEL[s.state] ?? escapeHtml(s.state)}${count}</span>` +
    restarts +
    shadow +
    `<span class="mcp-actions">${restart}${toggle}</span>` +
    `</div>` +
    error +
    toolChips(s) +
    log +
    `</li>`
  );
}

function editorHtml(e: Editor): string {
  return (
    `<div class="mcp-editor" data-scope="${e.scope}">` +
    `<div class="mcp-editor-path" title="${escapeHtml(e.path)}">${escapeHtml(shortPath(e.path))}</div>` +
    `<textarea spellcheck="false" aria-label="${e.scope} mcp.json">${escapeHtml(e.text)}</textarea>` +
    (e.error ? `<div class="mcp-editor-error">${escapeHtml(e.error)}</div>` : "") +
    `<div class="mcp-editor-actions">` +
    `<span class="mcp-editor-hint">stdio: <code>command</code>, <code>args</code>, <code>env</code> · http: <code>url</code>, <code>headers</code></span>` +
    `<button type="button" class="mcp-btn" data-mcp-cancel>Cancel</button>` +
    `<button type="button" class="mcp-btn is-primary" data-mcp-save${e.saving ? " disabled" : ""}>${e.saving ? "Saving…" : "Save"}</button>` +
    `</div></div>`
  );
}

function section(scope: MCPScope, o: MCPOverview): string {
  const servers = o.servers.filter((s) => s.scope === scope);
  const path = scope === "global" ? o.globalPath : (o.folderPath ?? "");
  const title =
    scope === "global"
      ? `<div class="mem-title">Everywhere</div><div class="mem-blurb">Servers from <code>${escapeHtml(shortPath(path))}</code>, available in every folder.</div>`
      : `<div class="mem-title">This folder <span class="mem-cwd" title="${escapeHtml(loadedCwd)}">${escapeHtml(shortPath(loadedCwd))}</span></div>` +
        `<div class="mem-blurb">Servers from <code>.paladin/mcp.json</code>; one with a global server's name replaces it here.</div>`;
  const trustedTag =
    scope === "folder" && o.hasFolderConfig && o.trusted
      ? `<button type="button" class="mcp-trusted" data-mcp-revoke title="Stop trusting this folder's mcp.json">${icon("sl-shield-check", "h-3.5 w-3.5")}Trusted</button>`
      : "";
  const edit = `<button type="button" class="mem-refresh" data-mcp-edit="${scope}">${icon("tl-pencil", "h-3.5 w-3.5")}${scope === "folder" && !o.hasFolderConfig ? "Add" : "Edit"} mcp.json</button>`;
  const err = o.errors?.[scope]
    ? `<div class="mcp-config-error">${icon("sl-shield-off", "h-3.5 w-3.5")}<span>${escapeHtml(o.errors[scope])}</span></div>`
    : "";
  const body = editor?.scope === scope ? editorHtml(editor) : "";
  const list = servers.length
    ? `<ul class="mcp-list">${servers.map(serverCard).join("")}</ul>`
    : `<div class="mem-none">No MCP servers configured${scope === "folder" ? " for this folder" : ""}.</div>`;
  return (
    `<section class="mem-section" data-mcp-section="${scope}">` +
    `<div class="mem-head"><div>${title}</div><div class="mcp-head-actions">${trustedTag}${edit}</div></div>` +
    err +
    body +
    list +
    `</section>`
  );
}

function trustBanner(o: MCPOverview): string {
  if (!needsTrust(o)) return "";
  const names = o.servers.filter((s) => s.scope === "folder").map((s) => s.name);
  const list = names.length ? names.map((n) => `<code>${escapeHtml(n)}</code>`).join(", ") : "its servers";
  return (
    `<div class="mcp-trust" role="alert">` +
    `<span class="mcp-trust-ico">${icon("sl-shield", "h-4 w-4")}</span>` +
    `<div class="mcp-trust-text"><div class="mcp-trust-title">Trust this folder's MCP servers?</div>` +
    `<div>This folder's <code>.paladin/mcp.json</code> wants to start ${list}. They run programs on your machine, so they stay off until you trust this file.</div></div>` +
    `<button type="button" class="mcp-btn is-primary" data-mcp-trust>Trust and start</button>` +
    `</div>`
  );
}

function paintPane(): void {
  paintNavDot();
  if (!overview) return;
  // A live repaint must not steal an edit in progress.
  const area = mcpBodyEl.querySelector<HTMLTextAreaElement>(".mcp-editor:not(.mcp-paste) textarea");
  const focused = !!area && document.activeElement === area;
  const sel = area ? [area.selectionStart, area.selectionEnd, area.scrollTop] : null;
  const pasteFocus = focusKey();
  const opened = new Set(
    Array.from(mcpBodyEl.querySelectorAll<HTMLElement>(".mcp-server"))
      .filter((el) => el.querySelector<HTMLDetailsElement>(".mcp-log")?.open)
      .map((el) => `${el.dataset.scope}:${el.dataset.mcpServer}`),
  );
  mcpBodyEl.innerHTML =
    `<div class="mem-intro"><span>Model Context Protocol servers add tools the agent can call. Read-only tools run freely; others ask first.</span>` +
    `<span class="mcp-head-actions"><button type="button" class="mem-refresh" data-mcp-paste>${icon("plus", "h-3.5 w-3.5")}Add server</button>` +
    `<button type="button" class="mem-refresh" data-mcp-refresh>${icon("repeat", "h-3.5 w-3.5")}Refresh</button></span></div>` +
    (paste ? pasteHtml(paste) : "") +
    trustBanner(overview) +
    section("global", overview) +
    (loadedCwd ? section("folder", overview) : "");
  mcpBodyEl.querySelectorAll<HTMLElement>(".mcp-server").forEach((el) => {
    if (opened.has(`${el.dataset.scope}:${el.dataset.mcpServer}`))
      el.querySelector(".mcp-log")?.setAttribute("open", "");
  });
  if (pasteFocus) restoreFocus(pasteFocus);
  const next = mcpBodyEl.querySelector<HTMLTextAreaElement>(".mcp-editor:not(.mcp-paste) textarea");
  if (next && sel) {
    next.setSelectionRange(sel[0], sel[1]);
    next.scrollTop = sel[2];
    if (focused) next.focus();
  }
}

type FocusKey = { sel: string; start: number | null; end: number | null };

// focusKey names the focused field of the paste box so a live repaint
// can put the caret back.
function focusKey(): FocusKey | null {
  const el = document.activeElement as HTMLInputElement | HTMLTextAreaElement | null;
  if (!el || !mcpBodyEl.contains(el) || !el.closest(".mcp-paste")) return null;
  const row = el.closest<HTMLElement>(".mcp-preview");
  let sel = el.matches("[data-paste-text]") ? "[data-paste-text]" : `[data-field="${el.dataset.field}"]`;
  if (el.dataset.key) sel += `[data-key="${CSS.escape(el.dataset.key)}"]`;
  if (row) sel = `.mcp-preview[data-index="${row.dataset.index}"] ${sel}`;
  const text = "selectionStart" in el && el.type !== "select-one";
  return { sel: `.mcp-paste ${sel}`, start: text ? el.selectionStart : null, end: text ? el.selectionEnd : null };
}

function restoreFocus(k: FocusKey): void {
  const el = mcpBodyEl.querySelector<HTMLInputElement>(k.sel);
  if (!el) return;
  el.focus();
  if (k.start !== null) el.setSelectionRange(k.start, k.end);
}

function paintNavDot(): void {
  const bad = failed(overview).length > 0 || needsTrust(overview);
  let dot = settingsNavMcpEl.querySelector<HTMLElement>(".mcp-nav-dot");
  if (bad && !dot) {
    dot = document.createElement("span");
    dot.className = "mcp-nav-dot";
    settingsNavMcpEl.append(dot);
  } else if (!bad && dot) {
    dot.remove();
  }
  if (dot) dot.dataset.tone = failed(overview).length ? "error" : "warn";
}

// ---- actions ----

async function act(key: string, run: () => Promise<MCPOverview>, what: string): Promise<void> {
  busy.add(key);
  paintPane();
  try {
    apply(await run());
  } catch (error) {
    toast(`Could not ${what}: ${String(error)}`, "error");
  } finally {
    busy.delete(key);
    paintPane();
  }
}

async function parsePaste(): Promise<void> {
  const p = paste;
  if (!p || p.busy || !p.text.trim()) return;
  p.busy = true;
  p.error = "";
  paintPane();
  try {
    const { servers } = await api.mcpParse(p.text);
    if (paste !== p) return;
    p.servers = servers;
    p.scope = "global";
  } catch (error) {
    p.error = errText(error);
  } finally {
    p.busy = false;
    if (paste === p) {
      paintPane();
      mcpBodyEl.querySelector<HTMLInputElement>('.mcp-preview [data-field="name"]')?.focus();
    }
  }
}

async function addPasted(replace: boolean): Promise<void> {
  const p = paste;
  if (!p?.servers?.length || p.busy) return;
  const empty = p.servers.find((s) => !s.name.trim());
  if (empty) {
    p.error = "Every server needs a name.";
    paintPane();
    return;
  }
  p.busy = true;
  p.error = "";
  p.exists = false;
  paintPane();
  try {
    const servers = p.servers.map((s) => ({ ...s, name: s.name.trim() }));
    const v = await api.mcpAdd(p.scope, cwdArg(), servers, replace);
    if (paste === p) paste = null;
    apply(v);
    toast(`Added ${servers.map((s) => s.name).join(", ")}`);
  } catch (error) {
    p.busy = false;
    p.error = errText(error);
    p.exists = /already exists/.test(p.error);
    paintPane();
  }
}

const errText = (error: unknown) => String(error instanceof Error ? error.message : error).replace(/^Error:\s*/, "");

async function startEdit(scope: MCPScope): Promise<void> {
  try {
    const file = await api.mcpConfig(scope, cwdArg());
    editor = { scope, path: file.path, text: file.text, error: "", saving: false };
    paintPane();
    const area = mcpBodyEl.querySelector<HTMLTextAreaElement>(".mcp-editor textarea");
    area?.focus();
    area?.setSelectionRange(0, 0);
  } catch (error) {
    toast(`Could not open mcp.json: ${String(error)}`, "error");
  }
}

async function saveEdit(): Promise<void> {
  if (!editor || editor.saving) return;
  const e = editor;
  e.saving = true;
  e.error = "";
  paintPane();
  try {
    const v = await api.mcpSetConfig(e.scope, cwdArg(), e.text);
    if (editor === e) editor = null;
    apply(v);
    toast("Saved mcp.json");
  } catch (error) {
    e.saving = false;
    e.error = String(error instanceof Error ? error.message : error).replace(/^Error:\s*/, "");
    paintPane();
  }
}

function onClick(event: MouseEvent): void {
  const t = event.target as HTMLElement;
  if (t.closest("[data-mcp-refresh]")) {
    void loadMcp();
    return;
  }
  if (t.closest("[data-mcp-paste]")) {
    paste = paste ?? { text: "", servers: null, scope: "global", error: "", exists: false, busy: false };
    paintPane();
    mcpBodyEl.querySelector<HTMLTextAreaElement>(".mcp-paste textarea")?.focus();
    return;
  }
  if (t.closest("[data-mcp-paste-cancel]")) {
    paste = null;
    paintPane();
    return;
  }
  if (t.closest("[data-mcp-paste-back]") && paste) {
    paste.servers = null;
    paste.error = "";
    paste.exists = false;
    paintPane();
    return;
  }
  if (t.closest("[data-mcp-parse]")) {
    void parsePaste();
    return;
  }
  if (t.closest("[data-mcp-add]")) {
    void addPasted(false);
    return;
  }
  if (t.closest("[data-mcp-replace]")) {
    void addPasted(true);
    return;
  }
  const drop = t.closest(".mcp-preview")?.getAttribute("data-index");
  if (t.closest("[data-mcp-drop]") && drop && paste?.servers) {
    paste.servers.splice(Number(drop), 1);
    paintPane();
    return;
  }
  if (t.closest("[data-mcp-trust]") && loadedCwd) {
    void act("trust", () => api.mcpTrust(loadedCwd, true), "trust this folder");
    return;
  }
  if (t.closest("[data-mcp-revoke]") && loadedCwd) {
    void act("trust", () => api.mcpTrust(loadedCwd, false), "revoke trust");
    return;
  }
  const edit = t.closest<HTMLElement>("[data-mcp-edit]")?.dataset.mcpEdit as MCPScope | undefined;
  if (edit) {
    void startEdit(edit);
    return;
  }
  if (t.closest("[data-mcp-cancel]")) {
    editor = null;
    paintPane();
    return;
  }
  if (t.closest("[data-mcp-save]")) {
    void saveEdit();
    return;
  }
  const card = t.closest<HTMLElement>(".mcp-server");
  if (!card) return;
  const scope = card.dataset.scope as MCPScope;
  const name = card.dataset.mcpServer ?? "";
  const key = `${scope}:${name}`;
  if (busy.has(key)) return;
  if (t.closest("[data-mcp-restart]")) {
    void act(key, () => api.mcpRestart(scope, cwdArg(), name), `restart ${name}`);
  } else if (t.closest("[data-mcp-toggle]")) {
    const enable = card.dataset.state === "disabled";
    void act(key, () => api.mcpSetEnabled(scope, cwdArg(), name, enable), `${enable ? "enable" : "disable"} ${name}`);
  }
}

function onPasteInput(t: HTMLInputElement): void {
  if (!paste) return;
  if (t.matches("[data-paste-text]")) {
    paste.text = t.value;
    const btn = mcpBodyEl.querySelector<HTMLButtonElement>("[data-mcp-parse]");
    if (btn) btn.disabled = paste.busy || !paste.text.trim();
    return;
  }
  const i = Number(t.closest<HTMLElement>(".mcp-preview")?.dataset.index);
  const srv = paste.servers?.[i];
  if (!srv) return;
  if (t.dataset.field === "name") srv.name = t.value;
  else if (t.dataset.field === "env" && t.dataset.key) srv.config.env = { ...srv.config.env, [t.dataset.key]: t.value };
}

// ---- composer chip ----

function ensureChip(): HTMLButtonElement {
  if (chip) return chip;
  chip = document.createElement("button");
  chip.type = "button";
  chip.id = "mcpChip";
  chip.className = "mcp-chip";
  chip.hidden = true;
  chip.addEventListener("click", () => openPane());
  const anchor = document.querySelector(".perm-wrap") ?? modelSelectBtn.parentElement;
  anchor?.after(chip);
  return chip;
}

function paintChip(): void {
  const el = ensureChip();
  const bad = failed(overview);
  if (bad.length) {
    el.dataset.tone = "error";
    el.innerHTML = `${icon("sl-plug", "h-3.5 w-3.5")}<span>MCP · ${bad.length} failed</span>`;
    el.title = `MCP servers failed: ${bad.map((s) => s.name).join(", ")} — open Settings → MCP`;
    el.hidden = false;
  } else if (needsTrust(overview)) {
    el.dataset.tone = "warn";
    el.innerHTML = `${icon("sl-shield", "h-3.5 w-3.5")}<span>Trust MCP servers?</span>`;
    el.title = "This folder has MCP servers that wait for your trust";
    el.hidden = false;
  } else {
    el.hidden = true;
    el.removeAttribute("data-tone");
  }
}

// initMcp wires the pane, the chip and mcp/status; open shows the pane.
export function initMcp(open: () => void): void {
  openPane = open;
  mcpBodyEl.addEventListener("click", onClick);
  mcpBodyEl.addEventListener("input", (event) => {
    const t = event.target as HTMLElement;
    if (editor && t.matches(".mcp-editor:not(.mcp-paste) textarea")) editor.text = (t as HTMLTextAreaElement).value;
    if (paste) onPasteInput(t as HTMLInputElement);
  });
  mcpBodyEl.addEventListener("change", (event) => {
    const t = event.target as HTMLSelectElement;
    if (paste && t.matches('.mcp-paste [data-field="scope"]')) paste.scope = t.value as MCPScope;
  });
  mcpBodyEl.addEventListener("keydown", (event) => {
    const t = event.target as HTMLElement;
    if (t.matches(".mcp-paste textarea") && event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void parsePaste();
      return;
    }
    if (t.matches(".mcp-preview input") && event.key === "Enter") {
      event.preventDefault();
      void addPasted(false);
      return;
    }
    if (!t.matches(".mcp-editor:not(.mcp-paste) textarea")) return;
    if (event.key === "s" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void saveEdit();
    } else if (event.key === "Tab" && !event.shiftKey) {
      event.preventDefault();
      document.execCommand("insertText", false, "  ");
    }
  });
  api.onAgentEvent((ev) => {
    if (ev.event === "mcp/status" && ev.server) onStatus(ev.server);
  });
  ensureChip();
  void loadMcp();
}
