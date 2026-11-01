// customize.ts is Settings → Customize: every built-in text Paladin
// sends the model (prompt sections, tool guidance and descriptions,
// reminders, subagent roles, commands, skills) plus its settings files,
// in one list. Picking an item opens it in an editor with what it does,
// the {{placeholders}} it fills in, where the override is saved, and
// Save / Reset to default. Skills also switch on and off here. Unsaved
// text is kept (draft) across other tabs and closing Settings, and
// nothing loading in the background replaces it.

import * as api from "../core/api";
import { $ } from "../core/dom";
import { escapeHtml } from "../core/format";
import type { CustomItem, CustomState } from "../core/types";
import { toast } from "../ui/toast";

let items: CustomItem[] = [];
let current: CustomState | null = null;
let query = "";
// pendingSwitch is the item the user picked while the editor had
// unsaved changes; the bar asks before dropping them.
let pendingSwitch = "";
let confirmingReset = false;
// draft is the editor's unsaved text for current, or null.
let draft: string | null = null;
// opening counts item loads, so only the newest one lands.
let opening = 0;

const body = (): HTMLElement => $("#customizeBody");

const KIND_LABEL: Record<CustomItem["kind"], string> = {
  text: "text",
  command: "command",
  skill: "skill",
  json: "JSON",
};

function editorText(): string {
  return body().querySelector<HTMLTextAreaElement>(".cz-text")?.value ?? "";
}

function dirty(): boolean {
  return !!current && body().querySelector(".cz-text") !== null && editorText() !== current.value;
}

function matches(it: CustomItem): boolean {
  if (!query) return true;
  const q = query.toLowerCase();
  return (
    it.title.toLowerCase().includes(q) ||
    it.group.toLowerCase().includes(q) ||
    it.description.toLowerCase().includes(q) ||
    it.id.includes(q)
  );
}

function listHtml(): string {
  const shown = items.filter(matches);
  if (!shown.length) return `<p class="cz-none">Nothing matches “${escapeHtml(query)}”.</p>`;
  let html = "";
  let group = "";
  for (const it of shown) {
    if (it.group !== group) {
      if (group) html += `</ul>`;
      group = it.group;
      html += `<div class="cz-group">${escapeHtml(group)}</div><ul class="cz-items">`;
    }
    const badges =
      (it.customized ? `<span class="cz-badge is-edited">edited</span>` : "") +
      (it.enabled === false ? `<span class="cz-badge is-off">off</span>` : "");
    const on = current?.id === it.id;
    html +=
      `<li><button type="button" class="cz-item${on ? " is-on" : ""}" data-cz-item="${escapeHtml(it.id)}" aria-current="${on}">` +
      `<span class="cz-item-title">${escapeHtml(it.title)}</span>${badges}</button></li>`;
  }
  return `${html}</ul>`;
}

function editorHtml(st: CustomState): string {
  const vars = st.vars?.length
    ? `<div class="cz-vars">Placeholders: ${st.vars.map((v) => `<code>{{${escapeHtml(v)}}}</code>`).join(" ")}</div>`
    : "";
  const missing = st.missing?.length
    ? `<div class="cz-warn" role="status">This text no longer uses ${st.missing
        .map((v) => `<code>{{${escapeHtml(v)}}}</code>`)
        .join(", ")}; that value will not reach the agent.</div>`
    : "";
  const skill =
    st.kind === "skill"
      ? `<label class="cz-skill-switch"><button type="button" class="mcp-switch" role="switch" data-cz-skill aria-checked="${st.enabled !== false}" aria-label="Skill on"><span></span></button>` +
        `<span>${st.enabled === false ? "Off: the agent will not see or load this skill" : "On: listed for the agent and in the / menu"}</span></label>`
      : "";
  const resetLabel = confirmingReset
    ? st.id === "file.settings"
      ? "Confirm: reset every setting"
      : "Confirm reset"
    : "Reset to default";
  return (
    `<div class="cz-head"><div class="cz-title">${escapeHtml(st.title)}<span class="cz-kind">${KIND_LABEL[st.kind]}</span>` +
    (st.customized ? `<span class="cz-badge is-edited">edited</span>` : `<span class="cz-badge">default</span>`) +
    `</div><p class="cz-desc">${escapeHtml(st.description)}</p>${vars}` +
    (st.path
      ? `<div class="cz-path" title="${escapeHtml(st.path)}">Saved in <code>${escapeHtml(st.path)}</code></div>`
      : "") +
    `${skill}</div>` +
    `<div class="cz-dirty" ${pendingSwitch ? "" : "hidden"} role="alert">Unsaved changes. ` +
    `<button type="button" data-cz-discard>Discard and switch</button><button type="button" data-cz-keep>Keep editing</button></div>` +
    missing +
    `<textarea class="cz-text" spellcheck="false" aria-label="${escapeHtml(st.title)} text">${escapeHtml(draft ?? st.value)}</textarea>` +
    `<div class="cz-actions"><span class="cz-status" role="status"></span>` +
    `<button type="button" class="cz-btn" data-cz-revert>Discard changes</button>` +
    `<button type="button" class="cz-btn${confirmingReset ? " is-danger" : ""}" data-cz-reset ${st.customized || confirmingReset ? "" : "disabled"}>${resetLabel}</button>` +
    `<button type="button" class="cz-btn is-primary" data-cz-save>Save</button></div>`
  );
}

function paintList(): void {
  const list = body().querySelector<HTMLElement>(".cz-list");
  if (list) list.innerHTML = listHtml();
}

function paint(): void {
  paintList();
  const editor = body().querySelector<HTMLElement>(".cz-editor");
  if (editor) {
    editor.innerHTML = current
      ? editorHtml(current)
      : `<p class="cz-empty">Pick an item to see and edit the text Paladin sends the model. Edits apply from the next turn.</p>`;
  }
  if (draft !== null) status("Unsaved changes");
}

function status(text: string, tone: "ok" | "warn" | "" = ""): void {
  const el = body().querySelector<HTMLElement>(".cz-status");
  if (!el) return;
  el.textContent = text;
  el.dataset.tone = tone;
}

function updateRow(st: CustomState): void {
  const i = items.findIndex((it) => it.id === st.id);
  if (i >= 0) items[i] = { ...items[i], customized: st.customized, enabled: st.enabled };
}

// open loads an item into the editor. If the user typed into the open
// item while it loaded, the bar asks before their text goes.
async function open(id: string, discard = false): Promise<void> {
  const seq = ++opening;
  try {
    const st = await api.customGet(id);
    if (seq !== opening) return;
    if (!discard && dirty()) {
      pendingSwitch = id;
      body().querySelector<HTMLElement>(".cz-dirty")!.hidden = false;
      return;
    }
    current = st;
    draft = null;
    pendingSwitch = "";
    confirmingReset = false;
    paint();
  } catch (error) {
    toast(`Could not open ${id}: ${String(error)}`, "error");
  }
}

async function save(): Promise<void> {
  if (!current) return;
  const keepScroll = body().querySelector<HTMLTextAreaElement>(".cz-text")?.scrollTop ?? 0;
  try {
    const st = await api.customSet(current.id, editorText());
    current = st;
    draft = null;
    updateRow(st);
    confirmingReset = false;
    paint();
    const ta = body().querySelector<HTMLTextAreaElement>(".cz-text");
    if (ta) ta.scrollTop = keepScroll;
    status(st.customized ? "Saved. Applies from the next turn." : "Same as the default, so the default is used.", "ok");
  } catch (error) {
    status(String(error).replace(/^Error:\s*/, ""), "warn");
  }
}

async function reset(): Promise<void> {
  if (!current) return;
  if (!confirmingReset) {
    confirmingReset = true;
    paint();
    return;
  }
  try {
    const st = await api.customReset(current.id);
    current = st;
    draft = null;
    updateRow(st);
    confirmingReset = false;
    paint();
    status("Back to the default.", "ok");
  } catch (error) {
    status(String(error), "warn");
  }
}

async function toggleSkill(): Promise<void> {
  if (!current || current.kind !== "skill") return;
  const on = current.enabled === false;
  try {
    await api.skillEnabledSet(current.id.replace(/^skill\./, ""), on);
    current = { ...current, enabled: on };
    updateRow(current);
    paint();
  } catch (error) {
    toast(`Could not switch the skill: ${String(error)}`, "error");
  }
}

// loadCustomize fills the pane (on opening the Customize tab). Only the
// list is refreshed once the editor exists: what the user is typing,
// saved or not, stays as it is.
export async function loadCustomize(): Promise<void> {
  const root = body();
  if (!root.querySelector(".cz-list")) {
    root.innerHTML =
      `<div class="cz-wrap"><div class="cz-side"><input class="cz-search" type="search" placeholder="Search prompts, tools, skills…" aria-label="Search customizable items" />` +
      `<div class="cz-list" role="navigation" aria-label="Customizable items"></div></div>` +
      `<div class="cz-editor"></div></div>`;
  }
  try {
    items = await api.customList();
  } catch (error) {
    root.querySelector(".cz-list")!.innerHTML = `<p class="cz-none">Could not load: ${escapeHtml(String(error))}</p>`;
    return;
  }
  const editorBuilt = !!root.querySelector(".cz-editor > *");
  if (current && !items.some((it) => it.id === current!.id) && draft === null) {
    current = null;
    paint();
    return;
  }
  if (editorBuilt) paintList();
  else paint();
}

// initCustomize wires the pane's events once.
export function initCustomize(): void {
  const root = body();
  root.addEventListener("input", (event) => {
    const t = event.target as HTMLElement;
    if (t.classList.contains("cz-search")) {
      query = (t as HTMLInputElement).value.trim();
      const list = root.querySelector<HTMLElement>(".cz-list");
      if (list) list.innerHTML = listHtml();
    } else if (t.classList.contains("cz-text")) {
      draft = dirty() ? editorText() : null;
      status(draft !== null ? "Unsaved changes" : "");
    }
  });
  root.addEventListener("keydown", (event) => {
    if (
      (event.metaKey || event.ctrlKey) &&
      event.key === "s" &&
      (event.target as HTMLElement).classList.contains("cz-text")
    ) {
      event.preventDefault();
      void save();
    }
  });
  root.addEventListener("click", (event) => {
    const t = event.target as HTMLElement;
    const pick = t.closest<HTMLElement>("[data-cz-item]")?.dataset.czItem;
    if (pick) {
      if (pick === current?.id) return;
      if (dirty()) {
        pendingSwitch = pick;
        root.querySelector<HTMLElement>(".cz-dirty")!.hidden = false;
        return;
      }
      void open(pick);
    } else if (t.closest("[data-cz-discard]") && pendingSwitch) {
      void open(pendingSwitch, true);
    } else if (t.closest("[data-cz-keep]")) {
      pendingSwitch = "";
      root.querySelector<HTMLElement>(".cz-dirty")!.hidden = true;
    } else if (t.closest("[data-cz-save]")) {
      void save();
    } else if (t.closest("[data-cz-reset]")) {
      void reset();
    } else if (t.closest("[data-cz-revert]") && current) {
      root.querySelector<HTMLTextAreaElement>(".cz-text")!.value = current.value;
      draft = null;
      status("");
    } else if (t.closest("[data-cz-skill]")) {
      void toggleSkill();
    }
  });
}
