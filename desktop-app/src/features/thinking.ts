// thinking.ts owns how hard the picked model thinks. Two surfaces, one
// stored choice:
//
//   - an inline strip above the composer text, showing the model and a
//     pill per level it accepts, so the setting is visible where you
//     type and one click away;
//   - the card the old chip opened, kept for keyboard work: a slider
//     over the same stops, the model picker and a reset.
//
// The choice is stored app-wide per model (thinkingLevels in
// settings.json) and applied from the next model call, so it can be
// changed in the middle of a session. Stop 0 is the provider default
// (nothing is sent). A model with no adjustable thinking still shows the
// strip, saying so, instead of hiding the whole control.

import * as api from "../core/api";
import { modelSelectBtn } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { ThinkingView } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { claimPopover, registerPopover } from "../ui/popover";
import { onModelChange, openModelMenu } from "./models";

const LABELS: Record<string, string> = {
  none: "Off",
  minimal: "Minimal",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra High",
  max: "Max",
};

// HINTS says what each level means for the next turn, so the pills read
// as something more than jargon.
const HINTS: Record<string, string> = {
  "": "The provider's own default",
  none: "No thinking before the answer",
  minimal: "A moment of thought",
  low: "Light thinking",
  medium: "Balanced thinking",
  high: "Deep thinking — slower, costs more",
  xhigh: "Very deep thinking — slow, costs more",
  max: "The model's deepest thinking — slowest, costs most",
};

const SPARKLES = 14;

let strip: HTMLElement | null = null;
let chip: HTMLButtonElement | null = null;
let card: HTMLElement | null = null;
let view: ThinkingView | null = null;
let syncSeq = 0;
let saveSeq = 0;

// thinkingLabel names a level for people ("" is the provider default).
export function thinkingLabel(level: string): string {
  return level === "" ? "Default" : (LABELS[level] ?? level);
}

function stops(v: ThinkingView): string[] {
  return ["", ...v.levels];
}

function shortModel(model: string): string {
  return model.split("/").pop() || model;
}

function setExpanded(on: boolean): void {
  chip?.setAttribute("aria-expanded", on ? "true" : "false");
}

function openCard(): void {
  ensureChrome();
  if (!view) return;
  claimPopover("thinking");
  paintCard();
  card!.classList.remove("hidden");
  setExpanded(true);
  card!.querySelector<HTMLInputElement>('input[type="range"]')?.focus();
}

function closeCard(): void {
  card?.classList.add("hidden");
  setExpanded(false);
}

// ensureChrome builds the strip above the composer and the card it sits
// over. Both are created once and only repainted afterwards.
function ensureChrome(): void {
  if (strip && chip && card) return;

  const slot = document.getElementById("thinkingSlot");
  const wrap = document.createElement("div");
  wrap.className = "think-inline hidden";
  wrap.innerHTML =
    `<div class="think-inline-bar">` +
    `<span class="think-inline-icon" aria-hidden="true">${icon("zap-line", "h-3.5 w-3.5")}</span>` +
    `<span class="think-inline-label">Thinking</span>` +
    `<div id="thinkingLevels" class="think-inline-levels" role="group" aria-label="Thinking level"></div>` +
    `<button type="button" id="thinkingMore" class="think-inline-more" title="Thinking options" aria-haspopup="dialog" aria-expanded="false">More${icon("chevron-down", "h-3 w-3")}</button>` +
    `</div>` +
    `<div id="thinkingMenu" class="think-card hidden" role="dialog" aria-label="Thinking level"></div>`;
  (slot ?? modelSelectBtn.parentElement)?.appendChild(wrap);

  strip = wrap;
  chip = wrap.querySelector<HTMLButtonElement>("#thinkingMore")!;
  card = wrap.querySelector<HTMLElement>("#thinkingMenu")!;

  registerPopover("thinking", closeCard);
  chip.addEventListener("click", (event) => {
    event.stopPropagation();
    if (card!.classList.contains("hidden")) openCard();
    else closeCard();
  });
  // A pill picks its level outright: the common case is one click.
  strip.querySelector("#thinkingLevels")!.addEventListener("click", (event) => {
    event.stopPropagation();
    const pill = (event.target as HTMLElement).closest<HTMLElement>("[data-level]");
    if (pill?.dataset.level !== undefined) void save(pill.dataset.level);
  });
  card.addEventListener("click", (event) => {
    event.stopPropagation();
    const target = event.target as HTMLElement;
    if (target.closest(".think-reset")) {
      // Reset disables itself, which drops its focus. Move focus to the
      // slider first so Escape still reaches the card.
      card!.querySelector<HTMLInputElement>('input[type="range"]')?.focus();
      void save("");
    } else if (target.closest(".think-model")) {
      closeCard();
      openModelMenu();
    }
  });
  card.addEventListener("input", (event) => {
    const input = event.target as HTMLInputElement;
    if (view && input.type === "range") showStop(stops(view)[Number(input.value)] ?? "");
  });
  card.addEventListener("change", (event) => {
    const input = event.target as HTMLInputElement;
    if (view && input.type === "range") void save(stops(view)[Number(input.value)] ?? "");
  });
  card.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      closeCard();
      chip?.focus();
    }
  });
  // Escape still closes the card when focus has fallen out of it: the
  // reset button disables itself, and the card's own keydown handler
  // only sees keys typed while focus is inside the card.
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && card && !card.classList.contains("hidden")) closeCard();
  });
  document.addEventListener("click", (event) => {
    if (card && !card.classList.contains("hidden") && !wrap.contains(event.target as Node)) closeCard();
  });
}

// paintStrip repaints the inline control: a pill per level, or a plain
// explanation when the model has nothing to adjust.
function paintStrip(): void {
  ensureChrome();
  if (!view) {
    strip!.classList.add("hidden");
    closeCard();
    return;
  }
  strip!.classList.remove("hidden");
  strip!.dataset.level = view.level || "default";

  const label = strip!.querySelector<HTMLElement>(".think-inline-label")!;
  label.textContent = "Thinking";
  label.title = `${shortModel(view.model)}: ${thinkingLabel(view.level)} — ${HINTS[view.level] ?? ""}`;

  const levels = strip!.querySelector<HTMLElement>("#thinkingLevels")!;
  if (view.levels.length === 0) {
    // Not adjustable: say so rather than showing an empty row, which
    // reads as broken.
    levels.innerHTML = `<span class="think-inline-none">${escapeHtml(shortModel(view.model))} has no thinking levels</span>`;
    chip!.classList.add("hidden");
    closeCard();
    return;
  }
  chip!.classList.remove("hidden");
  // Only the levels, not the provider default: the default is the
  // absence of a choice, and the card's reset covers it.
  levels.innerHTML = view.levels
    .map(
      (level) =>
        `<button type="button" class="think-pill${level === view!.level ? " is-on" : ""}" data-level="${escapeHtml(level)}"` +
        ` title="${escapeHtml(`${thinkingLabel(level)} — ${HINTS[level] ?? ""}`)}"` +
        ` aria-pressed="${level === view!.level ? "true" : "false"}">${escapeHtml(thinkingLabel(level))}</button>`,
    )
    .join("");
}

// intensity is how far along the slider a level sits, 0..1; it drives
// the fill and how lively the animation is.
function intensity(v: ThinkingView, level: string): number {
  const all = stops(v);
  return all.length > 1 ? Math.max(0, all.indexOf(level)) / (all.length - 1) : 0;
}

function paintCard(): void {
  if (!card || !view) return;
  const all = stops(view);
  const idx = Math.max(0, all.indexOf(view.level));
  const sparkles = Array.from({ length: SPARKLES }, (_, i) => {
    const x = ((i * 37) % 97) + 1;
    const y = 18 + ((i * 53) % 64);
    const delay = ((i * 0.37) % 3).toFixed(2);
    const size = 1 + (i % 3);
    return `<span style="left:${x}%;top:${y}%;width:${size}px;height:${size}px;animation-delay:-${delay}s"></span>`;
  }).join("");
  card.innerHTML =
    `<div class="think-head">` +
    `<span class="think-bolt" aria-hidden="true">${icon("zap-line", "h-5 w-5")}</span>` +
    `<div class="think-titles">` +
    `<div class="think-level" aria-live="polite"></div>` +
    `<button type="button" class="think-model" title="Change model">${escapeHtml(shortModel(view.model))}${icon("chevron-right", "h-3.5 w-3.5")}</button>` +
    `</div>` +
    `<button type="button" class="think-reset" title="Reset to the provider default" aria-label="Reset to the provider default">${icon("rotate-ccw", "h-4 w-4")}</button>` +
    `</div>` +
    `<div class="think-track">` +
    `<div class="think-fill"><div class="think-flow"></div><div class="think-sparkles" aria-hidden="true">${sparkles}</div></div>` +
    `<input type="range" min="0" max="${all.length - 1}" step="1" value="${idx}" aria-label="Thinking level" />` +
    `</div>` +
    `<div class="think-foot">Applies from the next message, in this session and every other. Higher levels think longer and cost more.</div>`;
  showStop(view.level);
}

// showStop paints a level without rebuilding the card, so the slider
// keeps focus while it is dragged or stepped. The card may never have
// been painted (the inline pills are the common path, and the card is
// built lazily), so a missing card is not an error.
function showStop(level: string): void {
  if (!card || !view) return;
  const label = thinkingLabel(level);
  card.dataset.level = level || "default";
  card.style.setProperty("--think-i", intensity(view, level).toFixed(3));
  const title = card.querySelector(".think-level");
  if (title) title.textContent = label;
  const input = card.querySelector<HTMLInputElement>('input[type="range"]');
  if (input) {
    input.value = String(Math.max(0, stops(view).indexOf(level)));
    input.setAttribute("aria-valuetext", label);
  }
  const reset = card.querySelector<HTMLButtonElement>(".think-reset");
  if (reset) reset.disabled = level === "";
}

// save stores the level for the shown model, painting it at once and
// rolling back if the sidecar refuses.
async function save(level: string): Promise<void> {
  if (!view) return;
  const before = view;
  const seq = ++saveSeq;
  view = { ...view, level };
  paintStrip();
  showStop(level);
  try {
    const next = await api.thinkingSet(before.provider, before.model, level);
    if (seq !== saveSeq || !view || view.model !== next.model || view.provider !== next.provider) return;
    view = next;
    paintStrip();
    showStop(next.level);
  } catch (error) {
    if (seq !== saveSeq) return;
    view = before;
    paintStrip();
    showStop(before.level);
    toast(`Thinking level not saved: ${String(error)}`, "error");
  }
}

// syncThinking reloads the picked model's levels and stored choice.
export function syncThinking(): Promise<void> {
  ensureChrome();
  const seq = ++syncSeq;
  return api.thinkingGet(state.selectedProvider || null, state.selectedModel || null).then(
    (v) => {
      if (seq !== syncSeq) return;
      // A changed level list (a catalog refresh taught us this model's
      // levels) must repaint the card too, not just its selected stop.
      const sameModel = view?.provider === v.provider && view?.model === v.model;
      const sameLevels = sameModel && (view?.levels ?? []).join(",") === v.levels.join(",");
      view = v;
      paintStrip();
      if (card && !card.classList.contains("hidden")) {
        if (sameLevels) showStop(v.level);
        else paintCard();
      }
    },
    () => {
      if (seq !== syncSeq) return;
      view = null;
      paintStrip();
    },
  );
}

// refreshThinkingOptions re-reads the picked model's levels without a
// model change. A catalog refresh supplies levels for models that had
// none, which is exactly the moment the control must appear.
export function refreshThinkingOptions(): void {
  void syncThinking();
}

// openThinkingCard is `/thinking` with no argument: show the slider.
export function openThinkingCard(): void {
  ensureChrome();
  if (view) {
    openCard();
    return;
  }
  void syncThinking().then(() => {
    if (view) openCard();
  });
}

// usageHint names what the picked model takes, so a mistyped
// `/thinking` says what is possible instead of a generic list.
function usageHint(): string {
  if (!view) return "/thinking <level> — or /thinking to see the options";
  if (view.levels.length === 0) return `${shortModel(view.model)} has no thinking levels`;
  return `${shortModel(view.model)}: ${view.levels.map(thinkingLabel).join(", ")} — or default`;
}

// setThinkingLevel is the `/thinking <level>` form: pick a level by name,
// or `default` to clear it, and say what happened.
export async function setThinkingLevel(level: string): Promise<void> {
  const names = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];
  const asked = level.trim().toLowerCase();
  const want = asked === "default" ? "" : asked;
  if (want !== "" && !names.includes(want)) {
    toast(`Unknown thinking level "${level}". ${usageHint()}`, "error");
    return;
  }
  if (!view) await syncThinking();
  if (!view) {
    toast("No model picked yet", "error");
    return;
  }
  const model = shortModel(view.model);
  if (want !== "" && !view.levels.includes(want)) {
    toast(usageHint(), "error");
    return;
  }
  await save(want);
  toast(want === "" ? `${model}: thinking back to the provider default` : `${model}: thinking ${thinkingLabel(want)}`);
}

export function initThinking(): void {
  ensureChrome();
  onModelChange(() => void syncThinking());
}
