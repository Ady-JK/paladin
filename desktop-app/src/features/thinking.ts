// thinking.ts owns the composer's thinking chip: the picked model's
// thinking (reasoning effort) level, set app-wide per model from a
// slider card. Stop 0 is the provider default (nothing is sent); the
// rest are the levels the model accepts, weakest first. The chip hides
// for models without adjustable thinking.

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
  "": "Default",
  none: "Off",
  minimal: "Minimal",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra High",
  max: "Max",
};

const SPARKLES = 14;

let chip: HTMLButtonElement | null = null;
let card: HTMLElement | null = null;
let view: ThinkingView | null = null;
let syncSeq = 0;
let saveSeq = 0;

// thinkingLabel names a level for people ("" is the provider default).
export function thinkingLabel(level: string): string {
  return LABELS[level] ?? level;
}

function stops(v: ThinkingView): string[] {
  return ["", ...v.levels];
}

function shortModel(model: string): string {
  return model.split("/").pop() || model;
}

function ensureChip(): HTMLButtonElement {
  if (chip) return chip;
  const wrap = document.createElement("div");
  wrap.className = "think-wrap";
  wrap.innerHTML =
    `<button type="button" id="thinkingChip" class="think-chip hidden" aria-haspopup="dialog" aria-expanded="false"></button>` +
    `<div id="thinkingMenu" class="think-card hidden" role="dialog" aria-label="Thinking level"></div>`;
  modelSelectBtn.insertAdjacentElement("afterend", wrap);
  chip = wrap.querySelector<HTMLButtonElement>("#thinkingChip")!;
  card = wrap.querySelector<HTMLElement>("#thinkingMenu")!;
  registerPopover("thinking", closeCard);
  chip.addEventListener("click", (event) => {
    event.stopPropagation();
    if (card!.classList.contains("hidden")) openCard();
    else closeCard();
  });
  card.addEventListener("click", (event) => {
    event.stopPropagation();
    const target = event.target as HTMLElement;
    if (target.closest(".think-reset")) {
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
  document.addEventListener("click", (event) => {
    if (card && !card.classList.contains("hidden") && !wrap.contains(event.target as Node)) closeCard();
  });
  return chip;
}

function paintChip(): void {
  const el = ensureChip();
  const has = !!view && view.levels.length > 0;
  el.classList.toggle("hidden", !has);
  if (!has) {
    closeCard();
    return;
  }
  const level = view!.level;
  el.dataset.level = level || "default";
  el.innerHTML = `${icon("zap-line", "h-3.5 w-3.5")}<span>${escapeHtml(thinkingLabel(level))}</span>`;
  el.title = `Thinking: ${thinkingLabel(level)}${level ? "" : " (the provider's own)"} — click to change`;
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
    `<div class="think-foot">Saved for this model in every session. Higher levels think longer and cost more.</div>`;
  showStop(view.level);
}

// showStop paints a level without rebuilding the card, so the slider
// keeps focus while it is dragged or stepped.
function showStop(level: string): void {
  if (!card || !view) return;
  const label = thinkingLabel(level);
  card.dataset.level = level || "default";
  card.style.setProperty("--think-i", intensity(view, level).toFixed(3));
  card.querySelector(".think-level")!.textContent = label;
  const input = card.querySelector<HTMLInputElement>('input[type="range"]');
  if (input) {
    input.value = String(Math.max(0, stops(view).indexOf(level)));
    input.setAttribute("aria-valuetext", label);
  }
  card.querySelector<HTMLButtonElement>(".think-reset")!.disabled = level === "";
}

function openCard(): void {
  ensureChip();
  if (!view || view.levels.length === 0) return;
  claimPopover("thinking");
  paintCard();
  card!.classList.remove("hidden");
  chip!.setAttribute("aria-expanded", "true");
  card!.querySelector<HTMLInputElement>('input[type="range"]')?.focus();
}

function closeCard(): void {
  card?.classList.add("hidden");
  chip?.setAttribute("aria-expanded", "false");
}

// save stores the level for the shown model, painting it at once and
// rolling back if the sidecar refuses.
async function save(level: string): Promise<void> {
  if (!view) return;
  const before = view;
  const seq = ++saveSeq;
  view = { ...view, level };
  paintChip();
  showStop(level);
  try {
    const next = await api.thinkingSet(before.provider, before.model, level);
    if (seq !== saveSeq || !view || view.model !== next.model || view.provider !== next.provider) return;
    view = next;
    paintChip();
    showStop(next.level);
  } catch (error) {
    if (seq !== saveSeq) return;
    view = before;
    paintChip();
    showStop(before.level);
    toast(`Thinking level not saved: ${String(error)}`, "error");
  }
}

// syncThinking reloads the picked model's levels and stored choice.
export function syncThinking(): Promise<void> {
  ensureChip();
  const seq = ++syncSeq;
  return api.thinkingGet(state.selectedProvider || null, state.selectedModel || null).then(
    (v) => {
      if (seq !== syncSeq) return;
      const sameModel = view?.provider === v.provider && view?.model === v.model;
      view = v;
      paintChip();
      if (card && !card.classList.contains("hidden")) {
        if (sameModel) showStop(v.level);
        else paintCard();
      }
    },
    () => {
      if (seq !== syncSeq) return;
      view = null;
      paintChip();
    },
  );
}

export function initThinking(): void {
  ensureChip();
  onModelChange(() => void syncThinking());
}
