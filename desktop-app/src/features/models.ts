// Loads providers and manages the composer's model picker.
import * as api from "../core/api";
import {
  inputEl,
  modelMenu,
  modelSearchClearEl,
  modelSearchEl,
  modelSelectBtn,
  modelSelectLabelEl,
  modelTreeEl,
} from "../core/dom";
import { bare, escapeAttr, escapeHtml, modelsOf, splitModel } from "../core/format";
import { state } from "../core/state";
import { loadJson, saveJson } from "../core/storage";
import { closeMenu, isMenuOpen, toggleMenu } from "../ui/anim";
import { claimPopover, registerPopover } from "../ui/popover";
import { toast } from "../ui/toast";

const MODEL_KEY = "paladin.model";
const DEFAULT_CATALOG_TTL = 30 * 60_000;
const DEFAULT_CATALOG_CHECK_INTERVAL = 5 * 60_000;
type ModelChoice = { provider: string; model: string };
const providerFilter = document.querySelector<HTMLSelectElement>("#modelProviderFilter")!;
const providerLabel = document.querySelector<HTMLElement>("#modelSelectProvider")!;
let filter = "";
let highlighted: ModelChoice | null = null;
let defaultRefreshInFlight = false;
let nextDefaultRefresh = 0;
let defaultRefreshGeneration = 0;

export function resetDefaultRefresh(): void {
  defaultRefreshGeneration++;
  defaultRefreshInFlight = false;
  nextDefaultRefresh = 0;
}

// Only the default provider is fetched, with a cooldown between refreshes.
export async function refreshDefaultCatalog(): Promise<void> {
  const provider = state.providers.find((item) => item.default);
  if (!provider || defaultRefreshInFlight || Date.now() < nextDefaultRefresh) return;
  const generation = defaultRefreshGeneration;
  defaultRefreshInFlight = true;
  nextDefaultRefresh = Date.now() + 60_000;
  try {
    const result = await api.refreshProvider(provider.name);
    if (result.error || generation !== defaultRefreshGeneration) return;
    const providers = await api.listProviders();
    if (generation !== defaultRefreshGeneration) return;
    state.providers = [...providers];
    restoreModel();
    syncModelSelects();
    void loadContext();
    state.onProvidersChanged();
    nextDefaultRefresh = Date.now() + DEFAULT_CATALOG_TTL;
    window.dispatchEvent(new CustomEvent("paladin:provider-catalog-refreshed", { detail: provider.name }));
  } catch {
    return;
  } finally {
    if (generation === defaultRefreshGeneration) defaultRefreshInFlight = false;
  }
}

export async function loadProviders(): Promise<void> {
  try {
    state.providers = await api.listProviders();
  } catch {
    state.providers = [];
  }
  restoreModel();
  syncModelSelects();
  void loadContext();
  state.onProvidersChanged();
}

function restoreModel(): void {
  const saved = loadJson(MODEL_KEY) as { provider?: string; model?: string } | null | undefined;
  const provider =
    state.providers.find((p) => p.name === saved?.provider) ??
    state.providers.find((p) => p.default) ??
    state.providers[0];
  state.selectedProvider = provider?.name ?? "";
  const models = modelsOf(provider);
  const short = saved?.model ? splitModel(saved.model).short : "";
  state.selectedModel =
    models.find((m) => m === saved?.model) ??
    (short
      ? [...models]
          .reverse()
          .find((m) => splitModel(m).short.startsWith(short) || short.startsWith(splitModel(m).short))
      : undefined) ??
    models[0] ??
    provider?.model ??
    "";
  state.selectedLab = state.selectedModel ? splitModel(state.selectedModel).lab : "";
}

// Prefer exact and contiguous matches, then compact subsequences. A small edit
// distance also permits common typing errors without flooding the result list.
function score(query: string, text: string): number {
  const q = query;
  const t = text.toLowerCase();
  if (!q) return 0;
  if (t === q) return 0;
  const at = t.indexOf(q);
  if (at >= 0) return 10 + at + (t.length - q.length) / 100;
  let pos = -1;
  let first = -1;
  for (const char of q) {
    pos = t.indexOf(char, pos + 1);
    if (pos < 0) break;
    if (first < 0) first = pos;
  }
  if (pos >= 0) return 30 + pos - first - q.length + 1 + first / 100;
  const maxDistance = Math.min(3, Math.floor(q.length / 3));
  if (Math.abs(t.length - q.length) > maxDistance) return Infinity;
  let previous = Array.from({ length: q.length + 1 }, (_, i) => i);
  for (let i = 0; i < t.length; i++) {
    const current = [i + 1];
    for (let j = 0; j < q.length; j++)
      current.push(Math.min(current[j] + 1, previous[j + 1] + 1, previous[j] + (t[i] === q[j] ? 0 : 1)));
    previous = current;
  }
  const distance = previous[q.length];
  return distance <= maxDistance ? 60 + distance : Infinity;
}

// Search results are reused for keyboard navigation until the catalog or query changes.
let lastSearch:
  { providers: typeof state.providers; query: string; filter: string; choices: ModelChoice[] } | undefined;

function choices(query: string): ModelChoice[] {
  const normalized = query.toLowerCase().trim();
  if (lastSearch?.providers === state.providers && lastSearch.query === normalized && lastSearch.filter === filter)
    return lastSearch.choices;

  // A provider or model can occur many times in a catalog. Score each distinct
  // text once per query; the provider is still part of each returned choice.
  const scores = new Map<string, number>();
  const scoreOnce = (text: string): number => {
    const cached = scores.get(text);
    if (cached !== undefined) return cached;
    const rank = score(normalized, text);
    scores.set(text, rank);
    return rank;
  };
  const matches = state.providers
    .filter((provider) => filter === "*" || provider.name === filter)
    .flatMap((provider) => {
      const providerScore = scoreOnce(bare(provider.name));
      return modelsOf(provider).map((model) => {
        const rank =
          providerScore === 0
            ? 0
            : Math.min(providerScore, scoreOnce(bare(splitModel(model).short)), scoreOnce(bare(model)));
        return { choice: { provider: provider.name, model }, rank };
      });
    });
  const catalogPriority = (provider: string): number =>
    state.providers.find((p) => p.name === provider)?.default ? 0 : 1;
  const result = matches
    .filter((item) => Number.isFinite(item.rank))
    .sort(
      (a, b) =>
        catalogPriority(a.choice.provider) - catalogPriority(b.choice.provider) ||
        a.choice.provider.localeCompare(b.choice.provider) ||
        a.rank - b.rank ||
        a.choice.model.localeCompare(b.choice.model) ||
        a.choice.provider.localeCompare(b.choice.provider),
    )
    .map((item) => item.choice);
  lastSearch = { providers: state.providers, query: normalized, filter, choices: result };
  return result;
}

function same(a: ModelChoice | null, b: ModelChoice): boolean {
  return a?.provider === b.provider && a.model === b.model;
}

export function syncModelSelects(): void {
  paintModel();
  if (!isMenuOpen(modelMenu)) return;
  hidePrice();
  renderProviderFilter();
  const all = choices(modelSearchEl.value);
  if (highlighted && !all.some((choice) => same(highlighted, choice))) highlighted = null;
  const rows = all
    .slice(0, 300)
    .map((choice) => {
      const selected = choice.provider === state.selectedProvider && choice.model === state.selectedModel;
      const hot = same(highlighted, choice);
      return `<button type="button" data-mpick="${escapeAttr(choice.provider)}&#9;${escapeAttr(choice.model)}" aria-pressed="${selected}" class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left ${hot ? "bg-slate-100 dark:bg-slate-800" : "hover:bg-slate-100 dark:hover:bg-slate-800"}">
      <span class="min-w-0 flex-1"><span class="block truncate text-sm text-slate-900 dark:text-slate-100">${escapeHtml(bare(splitModel(choice.model).short))}</span><span class="block truncate text-xs text-slate-500 dark:text-slate-400">${escapeHtml(bare(choice.provider))}</span></span>
      ${selected ? '<span aria-hidden="true" class="text-accent-600 dark:text-accent-400">✓</span>' : ""}</button>`;
    })
    .join("");
  modelTreeEl.innerHTML = rows || '<p class="px-3 py-4 text-sm text-slate-500 dark:text-slate-400">No models found</p>';
  if (all.length > 300)
    modelTreeEl.insertAdjacentHTML(
      "beforeend",
      `<p class="px-3 py-2 text-xs text-slate-500">${all.length - 300} more — refine your search</p>`,
    );
}

export function pickModel(name: string, model: string): void {
  if (!state.providers.some((p) => p.name === name)) return;
  state.selectedProvider = name;
  state.selectedLab = splitModel(model).lab;
  state.selectedModel = model;
  highlighted = null;
  persistModelChoice();
  syncModelSelects();
}

export function persistModelChoice(): void {
  saveJson(MODEL_KEY, { provider: state.selectedProvider, model: state.selectedModel });
}

const modelListeners: Array<() => void> = [];
let lastPicked = "";
export function onModelChange(fn: () => void): void {
  modelListeners.push(fn);
}

function paintModel(): void {
  const picked = `${state.selectedProvider}\u0000${state.selectedModel}`;
  if (picked !== lastPicked) {
    lastPicked = picked;
    for (const fn of modelListeners) fn();
    void loadContext();
  }
  const label = state.selectedModel ? bare(splitModel(state.selectedModel).short) : "default";
  providerLabel.textContent = bare(state.selectedProvider);
  modelSelectLabelEl.textContent = label;
  modelSelectLabelEl.title = label;
}

export function openModelMenu(): void {
  void refreshDefaultCatalog();
  if (!isMenuOpen(modelMenu)) {
    claimPopover("model");
    toggleMenu(modelMenu);
  }
  highlighted = null;
  filter = state.providers.find((p) => p.default)?.name ?? state.providers[0]?.name ?? "";
  modelSearchEl.value = "";
  syncModelSelects();
  modelSearchEl.focus();
}

function selectChoice(choice: ModelChoice): void {
  hidePrice();
  pickModel(choice.provider, choice.model);
  closeMenu(modelMenu);
  inputEl.focus();
}

export function initModelPicker(): void {
  registerPopover("model", () => closeMenu(modelMenu));
  initContextControl();
  window.addEventListener("paladin:session-selected", () => void loadContext());
  window.setInterval(() => void refreshDefaultCatalog(), DEFAULT_CATALOG_CHECK_INTERVAL);
  modelSelectBtn.addEventListener("click", (event) => {
    event.stopPropagation();
    if (isMenuOpen(modelMenu)) closeMenu(modelMenu);
    else openModelMenu();
  });
  document.addEventListener("click", (event) => {
    if (!event.composedPath().some((node) => node instanceof Element && node.matches("#modelSelectBtn, #modelMenu")))
      closeMenu(modelMenu);
  });
  modelTreeEl.addEventListener("click", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-mpick]");
    if (!row) return;
    const [provider, model] = (row.dataset.mpick ?? "").split("\t");
    selectChoice({ provider, model });
  });
  document.querySelector<HTMLButtonElement>("#modelRefresh")!.addEventListener("click", () => {
    void refreshPickerProvider();
  });
  providerFilter.addEventListener("change", () => {
    filter = providerFilter.value;
    highlighted = null;
    syncModelSelects();
  });
  modelTreeEl.addEventListener("mouseover", showPrice);
  modelTreeEl.addEventListener("focusin", showPrice);
  modelTreeEl.addEventListener("mouseout", hidePrice);
  modelTreeEl.addEventListener("focusout", hidePrice);
  modelTreeEl.addEventListener("scroll", hidePrice);
  modelSearchEl.addEventListener("input", () => {
    highlighted = null;
    syncModelSelects();
  });
  modelSearchEl.addEventListener("keydown", (event) => {
    const all = choices(modelSearchEl.value).slice(0, 300);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!all.length) return;
      const current = all.findIndex((choice) => same(highlighted, choice));
      const next =
        current < 0
          ? event.key === "ArrowDown"
            ? 0
            : all.length - 1
          : (current + (event.key === "ArrowDown" ? 1 : -1) + all.length) % all.length;
      highlighted = all[next];
      syncModelSelects();
      modelTreeEl.querySelectorAll<HTMLElement>("[data-mpick]")[next]?.scrollIntoView({ block: "nearest" });
    } else if (event.key === "Enter") {
      event.preventDefault();
      const choice = highlighted ?? all[0];
      if (choice) selectChoice(choice);
    } else if (event.key === "Escape") {
      closeMenu(modelMenu);
      inputEl.focus();
    }
  });
  modelSearchClearEl.addEventListener("click", () => {
    modelSearchEl.value = "";
    highlighted = null;
    syncModelSelects();
    modelSearchEl.focus();
  });
  document.addEventListener("keydown", (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
      event.preventDefault();
      if (isMenuOpen(modelMenu)) closeMenu(modelMenu);
      else openModelMenu();
    }
  });
}

function renderProviderFilter(): void {
  const providers = [...state.providers].sort(
    (a, b) => Number(b.default) - Number(a.default) || a.name.localeCompare(b.name),
  );
  if (filter !== "*" && !providers.some((p) => p.name === filter)) filter = providers[0]?.name ?? "";
  providerFilter.innerHTML =
    providers
      .map(
        (p) =>
          `<option value="${escapeAttr(p.name)}">${escapeHtml(bare(p.name))}${p.default ? " (default)" : ""}</option>`,
      )
      .join("") + '<option value="*">All providers</option>';
  providerFilter.value = filter;
}

function hidePrice(): void {
  document.querySelector("#modelPriceTooltip")?.remove();
}

function showPrice(event: Event): void {
  const row = (event.target as HTMLElement).closest<HTMLElement>("[data-mpick]");
  if (!row) return;
  hidePrice();
  const [provider, model] = (row.dataset.mpick ?? "").split("\t");
  const metadata = state.providers.find((p) => p.name === provider)?.meta?.[model];
  const pricing = metadata?.pricing;
  const tiers = pricing?.tiers ?? [];
  const oldRates = metadata && [
    metadata.priceInput,
    metadata.priceOutput,
    metadata.priceCacheRead,
    metadata.priceCacheWrite,
  ];
  const fallback = oldRates?.some((rate) => rate !== undefined)
    ? [
        {
          aboveInputTokens: 0,
          input: oldRates[0],
          output: oldRates[1],
          cacheRead: oldRates[2],
          cacheWrite: oldRates[3],
        },
      ]
    : [];
  const display = tiers.length ? tiers : fallback;
  const amount = (n: number | undefined): string =>
    n === undefined ? "Unavailable" : `$${n.toLocaleString(undefined, { maximumFractionDigits: 8 })}`;
  const tooltip = document.createElement("div");
  tooltip.id = "modelPriceTooltip";
  tooltip.className = "model-price-tooltip";
  tooltip.setAttribute("role", "tooltip");
  tooltip.innerHTML = `<strong>Price per 1M tokens</strong>${
    display.length
      ? display
          .map(
            (tier, index) =>
              `<div class="model-price-tier">${"aboveInputTokens" in tier && tier.aboveInputTokens ? `Above ${tier.aboveInputTokens.toLocaleString()} input tokens` : display[index + 1] && "aboveInputTokens" in display[index + 1] && display[index + 1].aboveInputTokens ? `Up to ${display[index + 1].aboveInputTokens!.toLocaleString()} input tokens` : "Standard"}</div>${[
                ["Input", tier.input],
                ["Output", tier.output],
                ["Cache read", tier.cacheRead],
                ["Cache write", tier.cacheWrite],
              ]
                .map(
                  ([name, rate]) =>
                    `<div class="model-price-line"><span>${name}</span><span>${amount(rate as number | undefined)}</span></div>`,
                )
                .join("")}`,
          )
          .join("")
      : '<div class="model-price-tier">Pricing unavailable</div>'
  }${pricing ? `<div class="model-price-note">${pricing.source === "litellm" ? "LiteLLM" : "GitHub Copilot"} · as of ${new Date(pricing.fetchedAt * 1000).toLocaleDateString()}</div>` : ""}${pricing && pricing.source !== "litellm" ? '<div class="model-price-note">100 AI credits = $1 · estimated usage</div>' : ""}`;
  document.body.append(tooltip);
  const bounds = row.getBoundingClientRect();
  const rect = tooltip.getBoundingClientRect();
  tooltip.style.left = `${Math.max(8, Math.min(window.innerWidth - rect.width - 8, bounds.right + 8))}px`;
  tooltip.style.top = `${Math.max(8, Math.min(window.innerHeight - rect.height - 8, bounds.top))}px`;
}

const contextButton = document.querySelector<HTMLButtonElement>("#contextButton")!;
const contextMenu = document.querySelector<HTMLElement>("#contextMenu")!;
const contextDescription = document.querySelector<HTMLElement>("#contextDescription")!;
const contextPercent = document.querySelector<HTMLInputElement>("#contextPercent")!;
const contextSizeRow = document.querySelector<HTMLElement>("#contextSizeRow")!;
const contextSize = document.querySelector<HTMLSelectElement>("#contextSize")!;
const contextTokens = document.querySelector<HTMLInputElement>("#contextTokens")!;
const contextError = document.querySelector<HTMLElement>("#contextError")!;
const contextSave = document.querySelector<HTMLButtonElement>("#contextSave")!;
let contextView: api.CompactionView | undefined;
let contextRequest = 0;

function shortTokens(tokens: number): string {
  if (tokens >= 1_000_000) return `${Number((tokens / 1_000_000).toFixed(2))}M`;
  if (tokens >= 1000) return `${Number((tokens / 1000).toFixed(1))}k`;
  return String(Math.round(tokens));
}

async function loadContext(): Promise<void> {
  const request = ++contextRequest;
  contextView = undefined;
  contextMenu.hidden = true;
  contextButton.setAttribute("aria-expanded", "false");
  contextButton.textContent = "Ctx —";
  if (!state.selectedProvider || !state.selectedModel) return;
  try {
    const view = await api.compactionGet(state.selectedProvider, state.selectedModel, state.sessionId || undefined);
    if (request !== contextRequest) return;
    contextView = view;
    paintContextButton();
    renderContext();
  } catch {
    if (request === contextRequest) contextButton.textContent = "Ctx —";
  }
}

function paintContextButton(): void {
  if (!contextView) return;
  const total = contextView.options?.find((option) => option.window === contextView!.window)?.totalWindow;
  contextButton.textContent = `${contextView.known ? "" : "~"}${shortTokens(total ?? contextView.window)}`;
  contextButton.title = `Input limit: ${shortTokens(contextView.window)} tokens`;
}

function renderContext(): void {
  if (!contextView) return;
  const choices = contextView.options ?? [];
  contextSizeRow.hidden = choices.length < 2;
  contextSize.innerHTML = choices
    .map(
      (choice) =>
        `<option value="${choice.window}">${choice.totalWindow ? `${shortTokens(choice.totalWindow)} context · ` : ""}${shortTokens(choice.window)} input${choice.surcharge ? " · different rates" : ""}</option>`,
    )
    .join("");
  contextSize.value = String(contextView.window);
  const total =
    choices.find((choice) => choice.window === contextView!.window)?.totalWindow ??
    (choices.length ? undefined : contextView.totalWindow);
  contextDescription.textContent = `${contextView.known ? "Maximum input" : "Assumed input limit"}: ${shortTokens(contextView.window)} tokens${total && total > contextView.window ? ` · ${shortTokens(total)} total including output` : ""}. ${state.sessionId ? "Saved for this session." : "Used as the default for new sessions."}`;
  contextPercent.value = String(Number(contextView.percent.toFixed(2)));
  contextTokens.value = shortTokens((contextView.window * contextView.percent) / 100);
  contextError.textContent = "";
}

function tokensValue(value: string): number {
  const match = value
    .trim()
    .replace(/,/g, "")
    .match(/^(\d+(?:\.\d+)?)\s*([km])?$/i);
  if (!match) return NaN;
  return Number(match[1]) * (match[2]?.toLowerCase() === "m" ? 1_000_000 : match[2]?.toLowerCase() === "k" ? 1000 : 1);
}

function initContextControl(): void {
  contextButton.addEventListener("click", (event) => {
    event.stopPropagation();
    contextMenu.hidden = !contextMenu.hidden;
    contextButton.setAttribute("aria-expanded", String(!contextMenu.hidden));
    if (!contextMenu.hidden) {
      closeMenu(modelMenu);
      renderContext();
    }
  });
  document.addEventListener("click", (event) => {
    if (!(event.target as Element).closest(".context-control")) {
      contextMenu.hidden = true;
      contextButton.setAttribute("aria-expanded", "false");
    }
  });
  contextSize.addEventListener("change", () => {
    if (!contextView) return;
    contextView = {
      ...contextView,
      window: Number(contextSize.value),
      percent: Number(contextPercent.value) || contextView.percent,
    };
    renderContext();
  });
  contextPercent.addEventListener("input", () => {
    const pct = Number(contextPercent.value);
    contextTokens.value = contextView && Number.isFinite(pct) ? shortTokens((contextView.window * pct) / 100) : "";
    contextError.textContent = "";
  });
  contextTokens.addEventListener("input", () => {
    const tokens = tokensValue(contextTokens.value);
    contextPercent.value =
      contextView && Number.isFinite(tokens) ? String(Number(((tokens / contextView.window) * 100).toFixed(4))) : "";
    contextError.textContent = "";
  });
  contextSave.addEventListener("click", () => {
    void saveContext();
  });
  contextMenu.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      void saveContext();
    }
    if (event.key === "Escape") {
      contextMenu.hidden = true;
      contextButton.setAttribute("aria-expanded", "false");
      contextButton.focus();
    }
  });
}

async function saveContext(): Promise<void> {
  const pct = Number(contextPercent.value);
  if (!contextView || !contextPercent.value || !Number.isFinite(pct) || pct < 1 || pct > 100) {
    contextError.textContent = "Choose a threshold from 1% to 100%.";
    return;
  }
  const view = contextView;
  contextSave.disabled = true;
  try {
    const updated = await api.compactionSet(view.provider, view.model, pct, view.window, state.sessionId || undefined);
    if (contextView === view) {
      contextView = updated;
      paintContextButton();
      contextMenu.hidden = true;
      contextButton.setAttribute("aria-expanded", "false");
    }
  } catch (error) {
    contextError.textContent = `Could not save threshold: ${String(error)}`;
  } finally {
    contextSave.disabled = false;
  }
}

async function refreshPickerProvider(): Promise<void> {
  if (filter === "*" || !filter) {
    toast("Choose a provider to refresh", "error");
    return;
  }
  const button = document.querySelector<HTMLButtonElement>("#modelRefresh")!;
  button.disabled = true;
  try {
    const result = await api.refreshProvider(filter);
    if (result.error) {
      toast(result.error, "error");
      return;
    }
    state.providers = await api.listProviders();
    syncModelSelects();
    state.onProvidersChanged();
    void loadContext();
    toast(result.warning ?? `${result.count} models refreshed`);
  } catch (error) {
    toast(String(error), "error");
  } finally {
    button.disabled = false;
  }
}
