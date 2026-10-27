// models.ts owns providers and the model picker.
// It loads the catalog, restores the saved pick, and renders the
// provider/lab/model tree, dropdowns, and detail pane.

import * as api from "../core/api";
import {
  filterBarEl,
  filterClearBtn,
  filterLabelEl,
  inputEl,
  labPickEl,
  modelBreadcrumbEl,
  modelDetailEl,
  modelMenu,
  modelPillEl,
  modelQuickBtn,
  modelSearchClearEl,
  modelSearchEl,
  modelSelectBtn,
  modelSelectLabelEl,
  modelTreeEl,
  newSessionCrumbEl,
  newSessionFilterBarEl,
  newSessionFilterClearBtn,
  newSessionFilterLabelEl,
  newSessionSearchEl,
  newSessionTreeEl,
  providerPickEl,
  sessionModalEl,
} from "../core/dom";
import {
  bare,
  escapeAttr,
  escapeHtml,
  fuzzyMatch,
  labKey,
  modelsInLab,
  modelsOf,
  splitModel,
} from "../core/format";
import { collapsedLabs, collapsedProviders, state } from "../core/state";
import type { GroupedLab, GroupedProvider, ProviderInfo } from "../core/types";
import { closeMenu, isMenuOpen, toggleMenu } from "../ui/anim";
import { icon } from "../icons";

// MODEL_KEY is a const in this module.
// It supports the desktop app frontend.
const MODEL_KEY = "paladin.model";

export async function loadProviders(): Promise<void> {
  try {
    state.providers = await api.listProviders();
  } catch {
    state.providers = [];
  }

  restoreModel();
  syncModelSelects();
  state.onProvidersChanged();
}

export async function refreshProviders(): Promise<void> {
  try {
    await api.refreshProviders();
  } catch {
    return;
  }

  window.setTimeout(() => {
    void loadProviders();
  }, 8000);
}

// restoreModel is a function in this module.
// It supports the desktop app frontend.
function restoreModel(): void {
  state.selectedProvider = "";
  state.selectedLab = "";
  state.selectedModel = "";

  try {
    const saved = JSON.parse(localStorage.getItem(MODEL_KEY) ?? "null") as {
      provider?: string;
      lab?: string;
      model?: string;
    } | null;

    if (saved && state.providers.some((p) => p.name === saved.provider)) {
      state.selectedProvider = saved.provider ?? "";
      state.selectedLab = saved.lab ?? "";
      state.selectedModel = saved.model ?? "";
    }
  } catch {
  }

  if (!state.selectedProvider) {
    const fallback = state.providers.find((p) => p.default) ?? state.providers[0];
    if (fallback) {
      state.selectedProvider = fallback.name;
    }
  }

  const provider = state.providers.find((p) => p.name === state.selectedProvider);
  const rawLabs = [...new Set(modelsOf(provider).map((m) => splitModel(m).lab))];
  state.selectedLab = rawLabs.find((lab) => bare(lab) === bare(state.selectedLab)) ?? rawLabs[0] ?? "";
  const models = modelsInLab(provider, state.selectedLab);
  const savedShort = state.selectedModel ? splitModel(state.selectedModel).short : "";
  state.selectedModel =
    models.find((m) => m === state.selectedModel) ??
    (savedShort
      ? [...models]
          .reverse()
          .find(
            (m) =>
              splitModel(m).short.startsWith(savedShort) ||
              savedShort.startsWith(splitModel(m).short),
          )
      : undefined) ??
    models[0] ??
    provider?.model ??
    "";

  paintModel();
}

// syncModelSelects is a function in this module.
// It supports the desktop app frontend.
export function syncModelSelects(): void {
  collapsedProviders.delete(bare(state.selectedProvider));
  collapsedLabs.delete(labKey(bare(state.selectedProvider), bare(state.selectedLab)));

  if (state.treeFilter) {
    const groups = groupedTree("", null);
    const group = groups.find((g) => g.display === state.treeFilter!.provider);
    if (!group || bare(state.selectedProvider) !== state.treeFilter.provider) {
      state.treeFilter = null;
    } else if (state.treeFilter.lab && bare(state.selectedLab) !== state.treeFilter.lab) {
      state.treeFilter = { provider: state.treeFilter.provider, lab: "" };
    }
  }

  const flat = flatModels(groupedTree(modelSearchEl.value));
  if (state.highlightedModel && !flat.some((m) => m.model === state.highlightedModel)) {
    state.highlightedModel = "";
  }

  paintPick();
  renderTrees();
  paintModel();
}

// renderTrees repaints the picker trees, but only the visible ones: the
// new-session tree is pointless work while its modal is closed (and vice
// versa). The detail pane is cheap and always fresh.
export function renderTrees(): void {
  if (!modelMenu.classList.contains("hidden")) {
    modelTreeEl.innerHTML = treeHTML(groupedTree(modelSearchEl.value));
  }
  if (!sessionModalEl.classList.contains("hidden")) {
    newSessionTreeEl.innerHTML = treeHTML(groupedTree(newSessionSearchEl.value));
  }
  modelDetailEl.innerHTML = detailHTML();
}

// groupedTree is a function in this module.
// It supports the desktop app frontend.
export function groupedTree(
  query: string,
  filter: { provider: string; lab: string } | null = state.treeFilter,
): GroupedProvider[] {
  const byProvider = new Map<string, ProviderInfo[]>();
  for (const info of state.providers) {
    const key = bare(info.name) || info.name;
    byProvider.set(key, [...(byProvider.get(key) ?? []), info]);
  }

  const out: GroupedProvider[] = [];
  for (const display of [...byProvider.keys()].sort((a, b) => a.localeCompare(b))) {
    const infos = byProvider.get(display)!;

    const byLab = new Map<string, { provider: string; id: string }[]>();
    for (const info of infos) {
      for (const m of modelsOf(info)) {
        const lab = bare(splitModel(m).lab);
        byLab.set(lab, [...(byLab.get(lab) ?? []), { provider: info.name, id: m }]);
      }
    }

    const matchModel = (id: string): boolean =>
      fuzzyMatch(query, id) || fuzzyMatch(query, bare(splitModel(id).short));

    const labs: GroupedLab[] = [];
    for (const lab of [...byLab.keys()].sort((a, b) => a.localeCompare(b))) {
      const models = byLab
        .get(lab)!
        .filter((e) => matchModel(e.id))
        .sort((a, b) => a.id.localeCompare(b.id));
      if (models.length > 0 || fuzzyMatch(query, lab)) {
        labs.push({ display: lab, models });
      }
    }

    if (labs.length === 0) {
      const self =
        fuzzyMatch(query, display) || infos.some((i) => fuzzyMatch(query, i.name));
      if (!self) {
        continue;
      }
      for (const lab of [...byLab.keys()].sort((a, b) => a.localeCompare(b))) {
        labs.push({
          display: lab,
          models: [...byLab.get(lab)!].sort((a, b) => a.id.localeCompare(b.id)),
        });
      }
    }

    out.push({
      display,
      infos,
      isDefault: infos.some((i) => i.default),
      labs,
    });
  }

  if (filter) {
    const group = out.find((g) => g.display === filter.provider);
    if (!group) {
      return [];
    }
    if (filter.lab) {
      const lab = group.labs.find((l) => l.display === filter.lab);
      return lab ? [{ ...group, labs: [lab] }] : [];
    }
    return [group];
  }

  return out;
}

// flatModels is a function in this module.
// It supports the desktop app frontend.
function flatModels(tree: GroupedProvider[]): { provider: string; model: string }[] {
  const out: { provider: string; model: string }[] = [];
  for (const p of tree) {
    for (const lab of p.labs) {
      for (const model of lab.models) {
        out.push({ provider: model.provider, model: model.id });
      }
    }
  }
  return out;
}

// treeHTML renders the provider/lab/model tree. Output is capped: a 467+
// row tree rebuilt per keystroke is the picker jank. Past the cap the UI
// says so instead of silently dropping rows.
function treeHTML(tree: GroupedProvider[]): string {
  if (tree.length === 0) {
    return `<p class="px-2 py-3 text-center font-mono text-[11px] text-slate-400">no match</p>`;
  }

  const picked = state.highlightedModel || state.selectedModel;
  const CAP = 300;
  const total = tree.reduce(
    (n, p) => n + p.labs.reduce((m, l) => m + l.models.length, 0),
    0,
  );
  let shown = 0;

  let out = "";
  outer: for (const p of tree) {
    const open = !collapsedProviders.has(p.display);
    out += `
      <button
        data-ptoggle="${escapeAttr(p.display)}"
        class="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-xs font-semibold text-slate-800 hover:bg-slate-100 dark:text-slate-100 dark:hover:bg-slate-800"
      >
        <span class="flex-shrink-0 text-slate-400">${icon(open ? "chevron-down" : "chevron-right", "h-3 w-3")}</span>
        <span class="flex-shrink-0 text-accent-500">${icon("globe", "h-3.5 w-3.5")}</span>
        <span class="truncate">${escapeHtml(p.display)}</span>
        ${p.isDefault ? `<span class="flex-shrink-0 font-mono text-[10px] font-normal text-slate-400">default</span>` : ""}
      </button>`;

    if (!open) {
      continue;
    }

    for (const lab of p.labs) {
      const key = labKey(p.display, lab.display);
      const labOpen = !collapsedLabs.has(key);
      out += `
        <button
          data-ltoggle="${escapeAttr(p.display)}	${escapeAttr(lab.display)}"
          class="flex w-full items-center gap-1.5 rounded-md py-1 pl-6 pr-1.5 text-left text-xs font-medium text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800"
        >
          <span class="flex-shrink-0 text-slate-400">${icon(labOpen ? "chevron-down" : "chevron-right", "h-3 w-3")}</span>
          <span class="flex-shrink-0 text-agent-amber">${icon("flask", "h-3.5 w-3.5")}</span>
          <span class="truncate">${escapeHtml(lab.display)}</span>
        </button>`;

      if (!labOpen) {
        continue;
      }

      for (const entry of lab.models) {
        if (shown >= CAP) {
          break outer;
        }
        shown++;
        const isPicked = entry.id === state.selectedModel && entry.provider === state.selectedProvider;
        const hot = entry.id === picked;
        out += `
          <button
            data-mpick="${escapeAttr(entry.provider)}	${escapeAttr(entry.id)}"
            title="${escapeAttr(entry.id)}"
            class="pick-row flex w-full items-center gap-1.5 rounded-md py-1 pl-11 pr-1.5 text-left font-mono text-[11px] ${
              isPicked
                ? "bg-accent-600/10 font-semibold text-slate-900 ring-1 ring-inset ring-accent-500/60 dark:text-slate-50"
                : hot
                  ? "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200"
                  : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
            }"
          >
            <span class="flex-shrink-0 text-agent-rose">${icon("brain", "h-3.5 w-3.5")}</span>
            <span class="truncate">${escapeHtml(bare(splitModel(entry.id).short))}</span>
          </button>`;
      }
    }
  }

  if (shown < total) {
    out += `<p class="px-2 py-2 text-center font-mono text-[10px] text-slate-400">…and ${total - shown} more — refine the search</p>`;
  }

  return out;
}

// detailHTML is a function in this module.
// It supports the desktop app frontend.
function detailHTML(): string {
  const target = state.highlightedModel || state.selectedModel;
  const info = state.providers.find((p) => p.name === state.selectedProvider);
  const lab = target ? bare(splitModel(target).lab) : bare(state.selectedLab);
  const apiType = info?.apiType ?? "";

  if (!target) {
    return `<p class="font-mono text-[11px] text-slate-400">Pick a model from the tree.</p>`;
  }

  const row = (name: string, value: string, real?: string): string => `
    <div class="mt-1.5">
      <div class="text-[10px] font-semibold uppercase tracking-widest text-slate-400 dark:text-slate-500">${name}</div>
      <div class="truncate font-mono text-[11px] text-slate-700 dark:text-slate-200" title="${escapeAttr(real ?? value)}">${escapeHtml(value)}</div>
    </div>`;

  return `
    <div class="flex items-center gap-1.5">
      <span class="flex-shrink-0 text-agent-rose">${icon("brain", "h-5 w-5")}</span>
      <div class="min-w-0 font-mono text-sm font-semibold text-slate-900 dark:text-slate-50" title="${escapeAttr(target)}">${escapeHtml(bare(splitModel(target).short))}</div>
    </div>
    ${row("Full id", target)}
    ${state.selectedProvider ? row("Provider", bare(state.selectedProvider), state.selectedProvider) : ""}
    ${lab ? row("Lab", lab) : ""}
    ${apiType ? row("API", apiType) : ""}`;
}

// paintPick is a function in this module.
// It supports the desktop app frontend.
function paintPick(): void {
  const pickedProvider = bare(state.selectedProvider);
  const pickedLab = bare(state.selectedLab);
  const short = state.selectedModel ? bare(splitModel(state.selectedModel).short) : "";

  const groups = groupedTree("", null);
  providerPickEl.innerHTML =
    groups.length === 0
      ? `<option value="">—</option>`
      : groups
          .map(
            (g) =>
              `<option value="${escapeAttr(g.display)}"${g.display === pickedProvider ? " selected" : ""}>${escapeHtml(g.display)}${g.isDefault ? " (default)" : ""}</option>`,
          )
          .join("");
  providerPickEl.disabled = groups.length === 0;
  if (groups.length > 0 && !groups.some((g) => g.display === pickedProvider)) {
    providerPickEl.value = groups[0].display;
  }

  const group = groups.find((g) => g.display === pickedProvider);
  const labs = group?.labs ?? [];
  labPickEl.innerHTML =
    labs.length === 0
      ? `<option value="">—</option>`
      : labs
          .map(
            (lab) =>
              `<option value="${escapeAttr(lab.display)}"${lab.display === pickedLab ? " selected" : ""}>${escapeHtml(lab.display)}</option>`,
          )
          .join("");
  labPickEl.disabled = labs.length === 0;
  if (labs.length > 0 && !labs.some((lab) => lab.display === pickedLab)) {
    labPickEl.value = labs[0].display;
  }

  modelPillEl.innerHTML = short
    ? `${icon("brain", "h-3.5 w-3.5 flex-shrink-0 text-agent-rose")}<span class="truncate">${escapeHtml(short)}</span>`
    : `<span class="text-slate-400">—</span>`;

  const parts = [pickedProvider, pickedLab, short].filter(Boolean);
  const crumb = parts.length > 0 ? parts.join(" / ") : "default";
  modelBreadcrumbEl.textContent = crumb;
  modelBreadcrumbEl.title = crumb;
  newSessionCrumbEl.textContent = crumb;
  newSessionCrumbEl.title = crumb;

  const bars: [HTMLElement, HTMLElement][] = [
    [filterBarEl, filterLabelEl],
    [newSessionFilterBarEl, newSessionFilterLabelEl],
  ];
  for (const [bar, label] of bars) {
    if (state.treeFilter) {
      bar.classList.remove("hidden");
      bar.classList.add("flex");
      label.textContent =
        `Showing ${state.treeFilter.provider}` +
        (state.treeFilter.lab ? ` / ${state.treeFilter.lab}` : "");
    } else {
      bar.classList.add("hidden");
      bar.classList.remove("flex");
    }
  }
}

// preferReal is a function in this module.
// It supports the desktop app frontend.
function preferReal(infos: ProviderInfo[], display: string): ProviderInfo | undefined {
  return (
    infos.find((i) => i.name === display) ??
    infos.find((i) => i.default) ??
    infos[0]
  );
}

// applyProviderGroup is a function in this module.
// It supports the desktop app frontend.
function applyProviderGroup(display: string): void {
  const group = groupedTree("", null).find((g) => g.display === display);
  if (!group) {
    return;
  }

  const first = group.labs[0]?.models[0];
  if (first) {
    pickModel(first.provider, first.id);
    return;
  }

  const real = preferReal(group.infos, display);
  if (real) {
    state.selectedProvider = real.name;
    state.selectedLab = "";
    state.selectedModel = real.model;
    persistModel();
    syncModelSelects();
  }
}

// applyLabGroup is a function in this module.
// It supports the desktop app frontend.
function applyLabGroup(display: string): void {
  const group = groupedTree("", null).find((g) => g.display === bare(state.selectedProvider));
  const lab = group?.labs.find((l) => l.display === display);
  const first = lab?.models[0];
  if (first) {
    pickModel(first.provider, first.id);
  }
}

// pickModel is a function in this module.
// It supports the desktop app frontend.
export function pickModel(name: string, model: string): void {
  const provider = state.providers.find((p) => p.name === name);
  if (!provider) {
    return;
  }

  state.selectedProvider = provider.name;
  state.selectedLab = splitModel(model).lab;
  state.selectedModel = model;
  state.highlightedModel = "";
  persistModel();
  syncModelSelects();
}

// quickSelect is a function in this module.
// It supports the desktop app frontend.
function quickSelect(query: string): boolean {
  const flat = flatModels(groupedTree(query));
  const target =
    flat.find((m) => m.model === state.highlightedModel) ??
    flat.find((m) => m.model === state.selectedModel && m.provider === state.selectedProvider) ??
    flat[0];
  if (!target) {
    return false;
  }

  pickModel(target.provider, target.model);
  return true;
}

// moveHighlight is a function in this module.
// It supports the desktop app frontend.
function moveHighlight(query: string, delta: 1 | -1): void {
  const flat = flatModels(groupedTree(query));
  if (flat.length === 0) {
    return;
  }

  const current =
    flat.findIndex((m) => m.model === (state.highlightedModel || state.selectedModel));
  const next = flat[(current + delta + flat.length) % flat.length];
  state.highlightedModel = next.model;

  for (const tree of [modelTreeEl, newSessionTreeEl]) {
    tree
      .querySelectorAll("[data-mpick].bg-slate-100,[data-mpick].dark\\:bg-slate-800")
      .forEach((row) => {
        row.classList.remove("bg-slate-100", "dark:bg-slate-800");
      });
    const row = tree.querySelector(
      `[data-mpick="${CSS.escape(next.provider)}	${CSS.escape(next.model)}"]`,
    );
    row?.classList.add("bg-slate-100", "dark:bg-slate-800");
    row?.scrollIntoView({ block: "nearest" });
  }

  modelDetailEl.innerHTML = detailHTML();
}

// persistModel is a function in this module.
// It supports the desktop app frontend.
function persistModel(): void {
  try {
    localStorage.setItem(
      MODEL_KEY,
      JSON.stringify({ provider: state.selectedProvider, lab: state.selectedLab, model: state.selectedModel }),
    );
  } catch {
  }
}

// paintModel is a function in this module.
// It supports the desktop app frontend.
function paintModel(): void {
  const provider = bare(state.selectedProvider);
  const short = state.selectedModel ? bare(splitModel(state.selectedModel).short) : "";
  const label = short ? (provider ? `${provider}/${short}` : short) : provider || "default";

  modelSelectLabelEl.textContent = label;
  modelSelectLabelEl.title = [state.selectedProvider, state.selectedLab, state.selectedModel]
    .filter(Boolean)
    .join(" / ") || label;

  paintPick();
}

// handleTreeClick toggles provider/lab groups and picks models.
// It supports the desktop app frontend.
export function handleTreeClick(event: Event): void {
  event.stopPropagation();
  const toggle = (event.target as HTMLElement).closest<HTMLElement>("[data-ptoggle],[data-ltoggle]");
  if (toggle) {
    if (toggle.dataset.ptoggle !== undefined) {
      const name = toggle.dataset.ptoggle;
      if (collapsedProviders.has(name)) {
        collapsedProviders.delete(name);
      } else {
        collapsedProviders.add(name);
      }
    } else {
      const [name, lab] = (toggle.dataset.ltoggle ?? "").split("	");
      const key = labKey(name, lab);
      if (collapsedLabs.has(key)) {
        collapsedLabs.delete(key);
      } else {
        collapsedLabs.add(key);
      }
    }
    renderTrees();
    return;
  }

  const pick = (event.target as HTMLElement).closest<HTMLElement>("[data-mpick]");
  if (pick) {
    const [name, model] = (pick.dataset.mpick ?? "").split("	");
    pickModel(name, model);
  }
}

// initModelPicker wires the model menu, dropdowns, and search fields.
// It supports the desktop app frontend.
export function initModelPicker(): void {
  let searchTimer = 0;
  providerPickEl.addEventListener("change", () => {
    const value = providerPickEl.value;
    if (!groupedTree("", null).some((g) => g.display === value)) {
      return;
    }
    state.treeFilter = { provider: value, lab: "" };
    applyProviderGroup(value);
  });

  labPickEl.addEventListener("change", () => {
    const value = labPickEl.value;
    const group = groupedTree("", null).find((g) => g.display === bare(state.selectedProvider));
    if (!group?.labs.some((lab) => lab.display === value)) {
      return;
    }
    applyLabGroup(value);
    state.treeFilter = { provider: bare(state.selectedProvider), lab: bare(state.selectedLab) };
    syncModelSelects();
  });

  const clearFilter = (): void => {
    state.treeFilter = null;
    syncModelSelects();
  };
  filterClearBtn.addEventListener("click", clearFilter);
  newSessionFilterClearBtn.addEventListener("click", clearFilter);

  modelSelectBtn.addEventListener("click", (event) => {
    event.stopPropagation();
    const opened = toggleMenu(modelMenu);
    syncModelSelects();
    if (opened) {
      modelSearchEl.focus();
    }
  });

  document.addEventListener("click", (event) => {
    const inside = event
      .composedPath()
      .some((node) => node instanceof Element && node.matches("#modelSelectBtn, #modelMenu"));
    if (!inside) {
      closeMenu(modelMenu);
    }
  });

  modelTreeEl.addEventListener("click", handleTreeClick);
  newSessionTreeEl.addEventListener("click", handleTreeClick);

  modelSearchEl.addEventListener("input", () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => {
      state.highlightedModel = "";
      syncModelSelects();
    }, 150);
  });
  modelSearchEl.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      moveHighlight(modelSearchEl.value, event.key === "ArrowDown" ? 1 : -1);
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (quickSelect(modelSearchEl.value)) {
        closeMenu(modelMenu);
        inputEl.focus();
      }
    } else if (event.key === "Escape") {
      closeMenu(modelMenu);
      inputEl.focus();
    }
  });
  modelSearchClearEl.addEventListener("click", () => {
    modelSearchEl.value = "";
    state.highlightedModel = "";
    syncModelSelects();
    modelSearchEl.focus();
  });

  newSessionSearchEl.addEventListener("input", () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => {
      state.highlightedModel = "";
      syncModelSelects();
    }, 150);
  });
  newSessionSearchEl.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      moveHighlight(newSessionSearchEl.value, event.key === "ArrowDown" ? 1 : -1);
    } else if (event.key === "Enter") {
      event.preventDefault();
      quickSelect(newSessionSearchEl.value);
    }
  });

  modelQuickBtn.addEventListener("click", () => {
    if (quickSelect(modelSearchEl.value)) {
      closeMenu(modelMenu);
      inputEl.focus();
    }
  });

  document.addEventListener("keydown", (event) => {
    const mod = event.metaKey || event.ctrlKey;
    if (mod && event.key.toLowerCase() === "k") {
      event.preventDefault();
      const opened = toggleMenu(modelMenu);
      syncModelSelects();
      if (opened) {
        modelSearchEl.focus();
      }
    } else if (event.ctrlKey && event.key === "1" && isMenuOpen(modelMenu)) {
      event.preventDefault();
      if (quickSelect(modelSearchEl.value)) {
        closeMenu(modelMenu);
        inputEl.focus();
      }
    }
  });
}
