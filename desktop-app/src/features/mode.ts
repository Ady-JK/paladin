// mode.ts is the composer's turn mode. Plan mode keeps the agent
// read-only (no edits, read-only shell) so it explores and proposes;
// "Implement" flips back to build and asks it to carry the plan out.
// Shift+Tab toggles, like Codex.

import { attachBtnEl } from "../core/dom";
import { state } from "../core/state";
import { icon } from "../icons";

let chip: HTMLElement | null = null;
let implement: () => void = () => {};

function ensureChip(): HTMLElement {
  if (chip) return chip;
  const el = document.createElement("div");
  el.id = "modeChip";
  el.className = "mode-chip hidden";
  el.innerHTML =
    `<span class="mode-chip-label" title="Plan mode: read-only — the agent explores and proposes a plan (Shift+Tab to toggle)">` +
    `${icon("sl-plan", "h-3.5 w-3.5")}<span>Plan</span></span>` +
    `<button type="button" id="modeImplementBtn" class="mode-chip-go" title="Switch to build mode and implement the plan">Implement</button>` +
    `<button type="button" class="mode-chip-x" aria-label="Leave plan mode" title="Leave plan mode">${icon("xmark", "h-3 w-3")}</button>`;
  el.querySelector("#modeImplementBtn")?.addEventListener("click", () => implement());
  el.querySelector(".mode-chip-x")?.addEventListener("click", () => setMode("build"));
  attachBtnEl.after(el);
  chip = el;
  return el;
}

export function setMode(mode: "build" | "plan"): void {
  state.mode = mode;
  const el = ensureChip();
  el.classList.toggle("hidden", mode !== "plan");
  document.body.dataset.mode = mode;
}

export function toggleMode(): void {
  setMode(state.mode === "plan" ? "build" : "plan");
}

// initMode wires the chip; onImplement sends the implement turn.
export function initMode(onImplement: () => void): void {
  implement = onImplement;
  setMode(state.mode);
}
