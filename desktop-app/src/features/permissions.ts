// permissions.ts owns the composer's permission preset chip (per
// session, Codex-style: Read-only / Auto / Full access) and the
// Settings → Permissions pane where saved approval rules are edited.

import * as api from "../core/api";
import { modelSelectBtn, permBodyEl } from "../core/dom";
import { escapeHtml, shortPath } from "../core/format";
import { state } from "../core/state";
import type { PermissionPreset, PermissionRule, PermissionsView } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { claimPopover, registerPopover } from "../ui/popover";
import { loadText, saveText } from "../core/storage";

const DEFAULT_KEY = "paladin.permissions.default";
const SESSION_KEY = "paladin.permissions.session.";
const PRESETS: PermissionPreset[] = ["read-only", "auto", "full-access"];

const INFO: Record<PermissionPreset, { label: string; icon: "sl-eye" | "sl-shield" | "sl-shield-off"; blurb: string }> =
  {
    "read-only": { label: "Read-only", icon: "sl-eye", blurb: "Reads and safe commands only. Asks before any change." },
    auto: {
      label: "Auto",
      icon: "sl-shield",
      blurb: "Works freely in this folder. Asks for new commands and writes outside it.",
    },
    "full-access": {
      label: "Full access",
      icon: "sl-shield-off",
      blurb: "Runs anything without asking, outside the sandbox.",
    },
  };

const RULE_LABEL: Record<PermissionRule["tool"], string> = {
  shell: "command",
  write: "write",
  read: "read",
  mcp: "mcp",
};

const RULE_HINT: Record<PermissionRule["tool"], string> = {
  shell: "npm test",
  write: "/abs/dir (sandboxed commands may write here too)",
  read: "/abs/dir outside the workspace the agent may read",
  mcp: "server/tool   or   server",
};

const SANDBOX: Record<string, string> = {
  seatbelt: "macOS Seatbelt sandbox",
  landlock: "Linux Landlock sandbox",
};

let chip: HTMLButtonElement | null = null;
let menu: HTMLElement | null = null;
let sandboxKind: string | null = null;
let draft: PermissionPreset | null = null;

function isPreset(v: unknown): v is PermissionPreset {
  return typeof v === "string" && (PRESETS as string[]).includes(v);
}

function defaultPreset(): PermissionPreset {
  const v = loadText(DEFAULT_KEY);
  return isPreset(v) ? v : "auto";
}

// currentPreset is what the visible session's next turn runs under.
export function currentPreset(): PermissionPreset {
  if (!state.sessionId) return draft ?? defaultPreset();
  const v = loadText(SESSION_KEY + state.sessionId);
  return isPreset(v) ? v : (draft ?? defaultPreset());
}

// keepDraftFor pins the preset picked before the session existed.
export function keepDraftFor(sessionId: string): void {
  if (draft && sessionId) saveText(SESSION_KEY + sessionId, draft);
  draft = null;
}

// resetDraft forgets an unsent pick when the user starts over.
export function resetDraft(): void {
  draft = null;
  syncPermChip();
}

function setPreset(p: PermissionPreset): void {
  if (state.sessionId) saveText(SESSION_KEY + state.sessionId, p);
  else draft = p;
  syncPermChip();
}

function ensureChip(): HTMLButtonElement {
  if (chip) return chip;
  const wrap = document.createElement("div");
  wrap.className = "perm-wrap";
  wrap.innerHTML =
    `<button type="button" id="permChip" class="perm-chip" aria-haspopup="menu" aria-expanded="false"></button>` +
    `<div id="permMenu" class="perm-menu hidden" role="menu"></div>`;
  modelSelectBtn.parentElement?.after(wrap);
  chip = wrap.querySelector<HTMLButtonElement>("#permChip")!;
  menu = wrap.querySelector<HTMLElement>("#permMenu")!;
  registerPopover("perm", closeMenu);
  chip.addEventListener("click", (event) => {
    event.stopPropagation();
    toggleMenu();
  });
  menu.addEventListener("click", (event) => {
    const item = (event.target as HTMLElement).closest<HTMLElement>("[data-preset]");
    if (!item || !isPreset(item.dataset.preset)) return;
    setPreset(item.dataset.preset);
    closeMenu();
  });
  menu.addEventListener("keydown", (event) => {
    const items = Array.from(menu!.querySelectorAll<HTMLElement>("[data-preset]"));
    const at = items.indexOf(document.activeElement as HTMLElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const next = (at + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
      items[next]?.focus();
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

function paintMenu(): void {
  if (!menu) return;
  const cur = currentPreset();
  const noSandbox = sandboxKind === "";
  menu.innerHTML =
    `<div class="perm-menu-head">Permissions</div>` +
    PRESETS.map((p) => {
      const info = INFO[p];
      const blurb = p !== "full-access" && noSandbox ? `${info.blurb} No OS sandbox here: approvals only.` : info.blurb;
      return (
        `<button type="button" role="menuitemradio" aria-checked="${p === cur}" class="perm-item${p === cur ? " is-on" : ""}${p === "full-access" ? " is-risky" : ""}" data-preset="${p}">` +
        `<span class="perm-item-ico">${icon(info.icon, "h-4 w-4")}</span>` +
        `<span class="perm-item-text"><span class="perm-item-label">${info.label}</span><span class="perm-item-blurb">${escapeHtml(blurb)}</span></span>` +
        `<span class="perm-item-check">${p === cur ? icon("check", "h-3 w-3") : ""}</span>` +
        `</button>`
      );
    }).join("");
}

function toggleMenu(): void {
  if (!menu || !chip) return;
  if (!menu.classList.contains("hidden")) {
    closeMenu();
    return;
  }
  claimPopover("perm");
  paintMenu();
  menu.classList.remove("hidden");
  chip.setAttribute("aria-expanded", "true");
  menu.querySelector<HTMLElement>(".perm-item.is-on")?.focus();
}

function closeMenu(): void {
  menu?.classList.add("hidden");
  chip?.setAttribute("aria-expanded", "false");
}

// syncPermChip repaints the chip for the visible session.
export function syncPermChip(): void {
  const el = ensureChip();
  const p = currentPreset();
  const info = INFO[p];
  el.dataset.preset = p;
  el.dataset.sandbox = sandboxKind === "" ? "none" : sandboxKind ? sandboxKind : "unknown";
  const warn =
    sandboxKind === "" && p !== "full-access" ? `<span class="perm-chip-warn" aria-hidden="true"></span>` : "";
  el.innerHTML = `${icon(info.icon, "h-3.5 w-3.5")}<span>${info.label}</span>${warn}${icon("chevron-down", "h-3 w-3")}`;
  const where =
    sandboxKind === ""
      ? "no OS sandbox on this platform — approvals only"
      : sandboxKind
        ? (SANDBOX[sandboxKind] ?? sandboxKind)
        : "";
  el.title = `Permissions: ${info.label} — ${info.blurb}${where ? ` (${where})` : ""}`;
  if (menu && !menu.classList.contains("hidden")) paintMenu();
}

export function initPermissions(): void {
  syncPermChip();
  void api
    .permissionsGet(state.cwd || null)
    .then((v) => {
      sandboxKind = v.sandbox?.kind ?? "";
      syncPermChip();
    })
    .catch(() => {
      // Older sidecar: no permissions RPC; keep the chip neutral.
    });
}

// ---- Settings → Permissions ----

let view: PermissionsView | null = null;
let folderCwd = "";
let loading = 0;

export async function loadPermissions(cwd?: string): Promise<void> {
  const mine = ++loading;
  if (cwd !== undefined) folderCwd = cwd;
  if (!folderCwd) folderCwd = state.cwd;
  if (!view) permBodyEl.innerHTML = `<div class="mem-empty">Loading permissions…</div>`;
  try {
    const v = await api.permissionsGet(folderCwd || null, state.sessionId || null);
    if (mine !== loading) return;
    view = v;
    sandboxKind = v.sandbox?.kind ?? "";
    syncPermChip();
    paintPane();
  } catch (error) {
    if (mine !== loading) return;
    permBodyEl.innerHTML = `<div class="mem-empty mem-error">Could not load permissions: ${escapeHtml(String(error))}</div>`;
  }
}

function ruleRow(r: PermissionRule, i: number): string {
  return (
    `<li class="perm-rule" data-rule-row="${i}">` +
    `<span class="perm-rule-tool">${RULE_LABEL[r.tool] ?? r.tool}</span>` +
    `<code class="perm-rule-pattern" title="${escapeHtml(r.pattern)}">${escapeHtml(r.pattern)}</code>` +
    `<span class="perm-rule-action is-${r.action}">${r.action}</span>` +
    `<button type="button" class="mem-x" data-rule-remove="${i}" title="Remove rule" aria-label="Remove rule">${icon("xmark", "h-3 w-3")}</button>` +
    `</li>`
  );
}

function rulesSection(scope: "global" | "folder", rules: PermissionRule[]): string {
  const head =
    scope === "global"
      ? `<div class="mem-title">Everywhere</div><div class="mem-blurb">Rules for every folder. Deny rules win over everything, even Full access.</div>`
      : `<div class="mem-title">This folder <span class="mem-cwd" title="${escapeHtml(folderCwd)}">${escapeHtml(shortPath(folderCwd))}</span></div>` +
        `<div class="mem-blurb">“Always allow in this folder” on an approval card adds here.</div>`;
  const rows = rules.length ? rules.map(ruleRow).join("") : `<li class="mem-none">No rules yet.</li>`;
  return (
    `<section class="mem-section perm-section" data-rules="${scope}">` +
    `<div class="mem-head"><div>${head}</div></div>` +
    `<ul class="mem-list">${rows}</ul>` +
    `<div class="perm-add">` +
    `<select data-rule-tool aria-label="Rule kind"><option value="shell">command</option><option value="write">write</option><option value="read">read</option><option value="mcp">mcp</option></select>` +
    `<input data-rule-pattern type="text" spellcheck="false" placeholder="${RULE_HINT.shell}" aria-label="Pattern" />` +
    `<select data-rule-action aria-label="Action"><option value="allow">allow</option><option value="deny">deny</option></select>` +
    `<button type="button" class="perm-add-btn" data-rule-add>Add</button>` +
    `</div></section>`
  );
}

function paintPane(): void {
  if (!view) return;
  const def = defaultPreset();
  const kind = view.sandbox?.kind ?? "";
  const sandbox = kind
    ? `<span class="perm-sandbox is-on">${icon("sl-shield-check", "h-3.5 w-3.5")}${escapeHtml(SANDBOX[kind] ?? kind)}</span>`
    : `<span class="perm-sandbox is-off">${icon("sl-shield-off", "h-3.5 w-3.5")}No OS sandbox on this platform — approvals only</span>`;
  const presets = PRESETS.map(
    (p) =>
      `<button type="button" class="perm-seg${p === def ? " is-on" : ""}" data-default-preset="${p}" aria-pressed="${p === def}" title="${escapeHtml(INFO[p].blurb)}">` +
      `${icon(INFO[p].icon, "h-3.5 w-3.5")}<span>${INFO[p].label}</span></button>`,
  ).join("");
  const others = view.folders.filter((f) => f.cwd !== folderCwd);
  const folders = others.length
    ? `<section class="mem-section"><div class="mem-title">Other folders with rules</div><ul class="perm-folders">${others
        .map(
          (f) =>
            `<li><button type="button" class="perm-folder" data-perm-folder="${escapeHtml(f.cwd)}" title="${escapeHtml(f.cwd)}">` +
            `${icon("rl-folder", "h-3.5 w-3.5")}<span>${escapeHtml(shortPath(f.cwd))}</span><span class="perm-count">${f.rules.length}</span></button></li>`,
        )
        .join("")}</ul></section>`
    : "";
  const session = view.session.length
    ? `<section class="mem-section"><div class="mem-title">This session</div><div class="mem-blurb">Allowed from approval cards; forgotten when the app quits.</div><ul class="mem-list">${view.session
        .map(
          (r) =>
            `<li class="perm-rule"><span class="perm-rule-tool">${RULE_LABEL[r.tool] ?? r.tool}</span><code class="perm-rule-pattern">${escapeHtml(r.pattern)}</code><span class="perm-rule-action is-${r.action}">${r.action}</span></li>`,
        )
        .join("")}</ul></section>`
    : "";
  permBodyEl.innerHTML =
    `<section class="mem-section"><div class="mem-head"><div><div class="mem-title">Default for new sessions</div>` +
    `<div class="mem-blurb">Each session can switch from the composer chip.</div></div>${sandbox}</div>` +
    (kind
      ? `<div class="mem-blurb perm-grant-note">Allow-write rules also open their folders to sandboxed commands; the rest of the disk stays read-only.</div>`
      : "") +
    `<div class="perm-segs" role="group" aria-label="Default permissions">${presets}</div></section>` +
    rulesSection("global", view.global) +
    (folderCwd ? rulesSection("folder", view.folder) : "") +
    session +
    folders;
}

async function save(scope: "global" | "folder", rules: PermissionRule[]): Promise<boolean> {
  try {
    const v = await api.permissionsSet(scope, scope === "folder" ? folderCwd : null, rules);
    view = v;
    paintPane();
    return true;
  } catch (error) {
    toast(`Could not save rules: ${String(error)}`, "error");
    return false;
  }
}

function addFrom(section: HTMLElement): void {
  if (!view) return;
  const scope = section.dataset.rules as "global" | "folder";
  const tool = section.querySelector<HTMLSelectElement>("[data-rule-tool]")!.value as PermissionRule["tool"];
  const action = section.querySelector<HTMLSelectElement>("[data-rule-action]")!.value as PermissionRule["action"];
  const input = section.querySelector<HTMLInputElement>("[data-rule-pattern]")!;
  const pattern = input.value.trim().replace(/\s+/g, tool === "shell" ? " " : "");
  if (!pattern) {
    input.focus();
    return;
  }
  const rules = scope === "global" ? view.global : view.folder;
  void save(scope, [...rules, { tool, pattern, action }]);
}

export function initPermissionsPane(): void {
  permBodyEl.addEventListener("click", (event) => {
    const t = event.target as HTMLElement;
    const preset = t.closest<HTMLElement>("[data-default-preset]")?.dataset.defaultPreset;
    if (isPreset(preset)) {
      saveText(DEFAULT_KEY, preset);
      paintPane();
      syncPermChip();
      return;
    }
    const section = t.closest<HTMLElement>("[data-rules]");
    if (section && t.closest("[data-rule-add]")) {
      addFrom(section);
      return;
    }
    const remove = t.closest<HTMLElement>("[data-rule-remove]");
    if (section && remove && view) {
      const scope = section.dataset.rules as "global" | "folder";
      const rules = (scope === "global" ? view.global : view.folder).filter(
        (_, i) => i !== Number(remove.dataset.ruleRemove),
      );
      void save(scope, rules);
      return;
    }
    const folder = t.closest<HTMLElement>("[data-perm-folder]")?.dataset.permFolder;
    if (folder) void loadPermissions(folder);
  });
  permBodyEl.addEventListener("keydown", (event) => {
    const t = event.target as HTMLElement;
    if (event.key === "Enter" && t.matches("[data-rule-pattern]")) {
      event.preventDefault();
      const section = t.closest<HTMLElement>("[data-rules]");
      if (section) addFrom(section);
    }
  });
  permBodyEl.addEventListener("change", (event) => {
    const t = event.target as HTMLSelectElement;
    if (!t.matches("[data-rule-tool]")) return;
    const input = t.closest("[data-rules]")?.querySelector<HTMLInputElement>("[data-rule-pattern]");
    if (input) input.placeholder = RULE_HINT[t.value as PermissionRule["tool"]] ?? "";
  });
}
