// splitResize owns the draggable border between the conversation and the
// side pane.
//
// The pane holds native browser views, which live in another process and
// are positioned by pixel bounds, so re-laying them out on every drag
// frame visibly lags behind the cursor (setBounds recreates the guest
// surface). Instead the drag only moves a preview line and writes a CSS
// custom property; the real width is committed on release, once, which is
// also when the pane's ResizeObserver repositions the native page.
//
// The width is stored per workspace: a wide site on one project should not
// follow the user to a repo where the conversation matters more.

import { loadText, saveText } from "../../core/storage";

const widthKey = "paladin.paneWidth";
// A web page below this is unusable, and the horizontal tab strip has no
// room left to be readable.
const MIN_PANE_WIDTH = 320;
// The conversation keeps at least this much, whatever the pane asks for.
const MIN_CHAT_WIDTH = 320;
const MAX_FRACTION = 0.8;

function clampPaneWidth(width: number, available: number): number {
  const upper = Math.max(MIN_PANE_WIDTH, Math.min(available * MAX_FRACTION, available - MIN_CHAT_WIDTH));
  return Math.round(Math.max(MIN_PANE_WIDTH, Math.min(upper, width)));
}

// savedPaneWidth is the stored width, or null when nothing was stored.
function savedPaneWidth(): number | null {
  const value = Number(loadText(widthKey));
  return Number.isFinite(value) && value > 0 ? value : null;
}

function rememberPaneWidth(width: number): void {
  saveText(widthKey, String(Math.round(width)));
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
  const currentWidth = (): number => pane.getBoundingClientRect().width;

  const apply = (width: number): void => {
    pane.style.flex = `0 0 ${width}px`;
    rememberPaneWidth(width);
    window.dispatchEvent(new Event("resize"));
  };

  let dragging = false;
  let startX = 0;
  let startWidth = 0;
  let pending = 0;

  const moveTo = (clientX: number): number => {
    const width = clampPaneWidth(startWidth - (clientX - startX), available());
    // Preview only: `right` is measured from the container's right edge.
    preview.style.right = `0`;
    preview.style.width = `${width}px`;
    pending = width;
    return width;
  };

  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    dragging = true;
    startX = event.clientX;
    startWidth = currentWidth();
    pending = startWidth;
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
    apply(pending);
  };
  handle.addEventListener("pointerup", end);
  handle.addEventListener("pointercancel", end);

  // Keyboard: the same 16px steps the other resize handles use.
  handle.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    const delta = event.key === "ArrowRight" ? -16 : 16;
    apply(clampPaneWidth(currentWidth() + delta, available()));
  });

  handle.addEventListener("dblclick", () => {
    apply(clampPaneWidth(available() / 2, available()));
  });

  const stored = savedPaneWidth();
  if (stored) pane.style.flex = `0 0 ${clampPaneWidth(stored, available())}px`;
  // A window resize can leave a stored width outside the new limits.
  window.addEventListener("resize", () => {
    if (dragging) return;
    const width = currentWidth();
    const clamped = clampPaneWidth(width, available());
    if (Math.abs(clamped - width) >= 1) pane.style.flex = `0 0 ${clamped}px`;
  });
}
