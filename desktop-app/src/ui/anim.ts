// anim.ts owns open/close animation for overlays and menus.
// All show/hide pairs route through here so open and close both animate.
// Close keeps the element mounted for one frame-out (~160ms) then hides it.

const CLOSE_MS = 160;
const MENU_CLOSE_MS = 130;

import { motionMs } from "./motion";

const timers = new WeakMap<HTMLElement, number>();
// returnFocus remembers what had focus when a modal opened, so closing puts
// the keyboard back where it was.
const returnFocus = new WeakMap<HTMLElement, HTMLElement>();
const trapped = new WeakSet<HTMLElement>();

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

function focusables(el: HTMLElement): HTMLElement[] {
  return Array.from(el.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((node) => node.offsetParent !== null);
}

// trapTab keeps Tab and Shift+Tab inside the modal while it is open.
function trapTab(el: HTMLElement): void {
  if (trapped.has(el)) return;
  trapped.add(el);
  el.addEventListener("keydown", (event) => {
    if (event.key !== "Tab" || el.classList.contains("hidden")) return;
    const nodes = focusables(el);
    if (nodes.length === 0) {
      event.preventDefault();
      return;
    }
    const first = nodes[0];
    const last = nodes[nodes.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || !el.contains(active))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || !el.contains(active))) {
      event.preventDefault();
      first.focus();
    }
  });
}

function clearTimer(el: HTMLElement): void {
  const pending = timers.get(el);
  if (pending !== undefined) {
    window.clearTimeout(pending);
    timers.delete(el);
  }
}

// openModal shows a fixed overlay (settings, session, site, confirm).
export function openModal(el: HTMLElement): void {
  clearTimer(el);
  // A modal is a dialog to assistive technology, and keyboard focus belongs
  // inside it until it closes.
  if (!el.hasAttribute("role")) el.setAttribute("role", "dialog");
  el.setAttribute("aria-modal", "true");
  const opener = document.activeElement;
  if (opener instanceof HTMLElement && !el.contains(opener)) returnFocus.set(el, opener);
  trapTab(el);
  el.classList.remove("hidden");
  el.classList.add("flex");
  el.classList.remove("is-closing");
  // Next frame so the overlay fades and the card rises instead of popping.
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      el.classList.add("is-open");
      // Move focus in unless the opener already put it on a control inside.
      if (!el.contains(document.activeElement)) (focusables(el)[0] ?? el).focus({ preventScroll: true });
    });
  });
}

// closeModal plays the fade/scale-out then hides the overlay.
export function closeModal(el: HTMLElement): void {
  if (el.classList.contains("hidden")) {
    return;
  }
  clearTimer(el);
  el.classList.remove("is-open");
  const back = returnFocus.get(el);
  returnFocus.delete(el);
  if (back?.isConnected && el.contains(document.activeElement)) back.focus({ preventScroll: true });
  const ms = motionMs(CLOSE_MS);
  if (ms === 0) {
    el.classList.add("hidden");
    el.classList.remove("flex", "is-closing");
    return;
  }
  el.classList.add("is-closing");
  timers.set(
    el,
    window.setTimeout(() => {
      el.classList.add("hidden");
      el.classList.remove("flex", "is-closing");
      timers.delete(el);
    }, ms),
  );
}

// isModalOpen reports visibility including the close fade-out.
export function isModalOpen(el: HTMLElement): boolean {
  return !el.classList.contains("hidden");
}

// openMenu pops a dropdown/picker into view.
function openMenu(el: HTMLElement): void {
  clearTimer(el);
  el.classList.remove("hidden");
  requestAnimationFrame(() => {
    requestAnimationFrame(() => el.classList.add("is-open"));
  });
}

// closeMenu plays the pop-out then hides the dropdown.
export function closeMenu(el: HTMLElement): void {
  if (el.classList.contains("hidden")) {
    return;
  }
  clearTimer(el);
  el.classList.remove("is-open");
  const ms = motionMs(MENU_CLOSE_MS);
  if (ms === 0) {
    el.classList.add("hidden");
    return;
  }
  timers.set(
    el,
    window.setTimeout(() => {
      el.classList.add("hidden");
      timers.delete(el);
    }, ms),
  );
}

// toggleMenu flips a dropdown and reports the new visibility.
export function toggleMenu(el: HTMLElement): boolean {
  if (el.classList.contains("hidden")) {
    openMenu(el);
    return true;
  }
  closeMenu(el);
  return false;
}

// isMenuOpen reports dropdown visibility.
export function isMenuOpen(el: HTMLElement): boolean {
  return !el.classList.contains("hidden");
}
