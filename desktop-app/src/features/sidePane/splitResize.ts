// splitResize owns the draggable border between the conversation and the
// side pane.
//
// The width is stored as a *fraction* of the split, not as pixels: a pane
// dragged to 900px on a 2560px screen is a different split from the same
// 900px on a 1280px screen, and a pixel width silently stops being 50%
// forever after the first drag.
//
// Two rules keep the 50% default honest:
//
//   1. Only the user changes the fraction. A window resize re-clamps what
//      is stored; it never measures the pane. Measuring was the bug: a
//      hidden pane is 0px wide, so any resize while it was closed (the
//      sessions drawer dispatches one) recorded "0" and the next open
//      came in at the minimum width instead of half.
//   2. Only the split-mode CSS rule reads the fraction. Writing inline
//      `flex` on the pane is what used to pin it to its old pixel width in
//      tab view, where the pane must fill the split instead.
//
// The drag itself only moves a preview line: a native browser page lives
// in another process and is positioned by pixel bounds, so re-laying it out
// on every frame visibly lags behind the cursor. The width is committed
// once, on release.

import { loadText, saveText } from "../../core/storage";

// Stored as a fraction so the split keeps its proportion across window
// sizes. The key carries the storage format in its name: older builds
// stored pixels ("paladin.paneWidth") and then a polluted fraction
// ("paladin.paneRatio"), and neither can be trusted to mean half.
const ratioKey = "paladin.paneSplit";
const DEFAULT_PANE_RATIO = 0.5;
// A web page below this is unusable, and the horizontal tab strip has no
// room left to be readable.
const MIN_PANE_WIDTH = 320;
// The conversation keeps at least this much, whatever the pane asks for.
const MIN_CHAT_WIDTH = 320;
const MAX_FRACTION = 0.8;

function clampRatio(ratio: number, available: number): number {
  if (!(available > 0)) return DEFAULT_PANE_RATIO;
  const minRatio = Math.min(MAX_FRACTION, MIN_PANE_WIDTH / available);
  const maxRatio = Math.max(minRatio, Math.min(MAX_FRACTION, (available - MIN_CHAT_WIDTH) / available));
  return Math.min(maxRatio, Math.max(minRatio, ratio));
}

function savedRatio(): number {
  const value = Number(loadText(ratioKey));
  return Number.isFinite(value) && value > 0 && value < 1 ? value : DEFAULT_PANE_RATIO;
}

function rememberRatio(ratio: number): void {
  saveText(ratioKey, String(Math.round(ratio * 1000) / 1000));
}

// wireSplitResize makes `handle` drag the width of `pane` inside `container`.
// A double-click returns the split to half, so an extreme drag is never a
// dead end.
export function wireSplitResize(handle: HTMLElement, container: HTMLElement, pane: HTMLElement): void {
  // The line that follows the cursor while dragging, so the native page
  // never renders at an intermediate width.
  const preview = document.createElement("div");
  preview.className = "pane-split-preview";
  preview.hidden = true;
  container.append(preview);

  const available = (): number => container.getBoundingClientRect().width;

  // paint sets the split proportion as a custom property without touching
  // what is stored: it also runs for derived values (init, window resize).
  // Everything that measures the layout (native page bounds, the Web menu,
  // the IDE columns) hangs off the resize event, so emitting it here is what
  // moves them. It is only emitted when the width actually changed, because
  // this event is also an input to paint(): emitting unconditionally would
  // loop forever on the resize listener below.
  let painted = "";
  const paint = (ratio: number): void => {
    const total = available();
    if (!(total > 0)) return;
    const clamped = clampRatio(ratio, total);
    const next = `${(clamped * 100).toFixed(3)}%`;
    if (next === painted) return;
    painted = next;
    container.style.setProperty("--side-pane-width", next);
    window.dispatchEvent(new Event("resize"));
  };

  // commit is paint plus remember: only a deliberate user change gets here.
  const commit = (ratio: number): void => {
    const total = available();
    if (!(total > 0)) return;
    const clamped = clampRatio(ratio, total);
    rememberRatio(clamped);
    paint(clamped);
  };

  let dragging = false;
  let startX = 0;
  let startWidth = 0;
  let pending = DEFAULT_PANE_RATIO;

  const moveTo = (clientX: number): void => {
    const total = available();
    if (!(total > 0)) return;
    const width = Math.max(0, startWidth - (clientX - startX));
    const ratio = clampRatio(width / total, total);
    // Preview only: `right` is measured from the container's right edge.
    preview.style.right = `0`;
    preview.style.width = `${Math.round(ratio * total)}px`;
    pending = ratio;
  };

  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    const total = available();
    const width = pane.getBoundingClientRect().width;
    // A closed or stacked pane has no usable width to drag from.
    if (!(total > 0) || !(width > 0)) return;
    dragging = true;
    startX = event.clientX;
    startWidth = width;
    pending = clampRatio(width / total, total);
    handle.setPointerCapture(event.pointerId);
    document.body.classList.add("is-splitting");
    preview.hidden = false;
    moveTo(event.clientX);
    event.preventDefault();
  });

  handle.addEventListener("pointermove", (event) => {
    if (!dragging || !handle.hasPointerCapture(event.pointerId)) return;
    moveTo(event.clientX);
  });

  const end = (event: PointerEvent): void => {
    if (!dragging) return;
    dragging = false;
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
    preview.hidden = true;
    document.body.classList.remove("is-splitting");
    commit(pending);
  };
  handle.addEventListener("pointerup", end);
  handle.addEventListener("pointercancel", end);

  // Keyboard: the same 16px steps the other resize handles use.
  handle.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    const total = available();
    const width = pane.getBoundingClientRect().width;
    if (!(total > 0) || !(width > 0)) return;
    const step = 16 / total;
    const delta = event.key === "ArrowRight" ? -step : step;
    commit(width / total + delta);
  });

  handle.addEventListener("dblclick", () => {
    commit(DEFAULT_PANE_RATIO);
  });

  paint(savedRatio());
  // A window resize changes what a given fraction is worth in pixels, so
  // re-clamp the stored proportion: one that was legal at 2560px can
  // starve the conversation at 900px.
  window.addEventListener("resize", () => {
    if (dragging) return;
    paint(savedRatio());
  });
}
