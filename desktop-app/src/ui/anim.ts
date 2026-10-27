// anim.ts owns open/close animation for overlays and menus.
// All show/hide pairs route through here so open and close both animate.
// Close keeps the element mounted for one frame-out (~160ms) then hides it.

const CLOSE_MS = 160;
const MENU_CLOSE_MS = 130;

const timers = new WeakMap<HTMLElement, number>();

function clearTimer(el: HTMLElement): void {
  const pending = timers.get(el);
  if (pending !== undefined) {
    window.clearTimeout(pending);
    timers.delete(el);
  }
}

// openModal shows a fixed overlay (settings, session, site, confirm).
// It supports the desktop app frontend.
export function openModal(el: HTMLElement): void {
  clearTimer(el);
  el.classList.remove("hidden");
  el.classList.add("flex");
  el.classList.remove("is-closing");
  // Next frame so the overlay fades and the card rises instead of popping.
  requestAnimationFrame(() => {
    requestAnimationFrame(() => el.classList.add("is-open"));
  });
}

// closeModal plays the fade/scale-out then hides the overlay.
// It supports the desktop app frontend.
export function closeModal(el: HTMLElement): void {
  if (el.classList.contains("hidden")) {
    return;
  }
  clearTimer(el);
  el.classList.remove("is-open");
  el.classList.add("is-closing");
  timers.set(
    el,
    window.setTimeout(() => {
      el.classList.add("hidden");
      el.classList.remove("flex", "is-closing");
      timers.delete(el);
    }, CLOSE_MS),
  );
}

// isModalOpen reports visibility including the close fade-out.
// It supports the desktop app frontend.
export function isModalOpen(el: HTMLElement): boolean {
  return !el.classList.contains("hidden");
}

// openMenu pops a dropdown/picker into view.
// It supports the desktop app frontend.
export function openMenu(el: HTMLElement): void {
  clearTimer(el);
  el.classList.remove("hidden");
  requestAnimationFrame(() => {
    requestAnimationFrame(() => el.classList.add("is-open"));
  });
}

// closeMenu plays the pop-out then hides the dropdown.
// It supports the desktop app frontend.
export function closeMenu(el: HTMLElement): void {
  if (el.classList.contains("hidden")) {
    return;
  }
  clearTimer(el);
  el.classList.remove("is-open");
  timers.set(
    el,
    window.setTimeout(() => {
      el.classList.add("hidden");
      timers.delete(el);
    }, MENU_CLOSE_MS),
  );
}

// toggleMenu flips a dropdown and reports the new visibility.
// It supports the desktop app frontend.
export function toggleMenu(el: HTMLElement): boolean {
  if (el.classList.contains("hidden")) {
    openMenu(el);
    return true;
  }
  closeMenu(el);
  return false;
}

// isMenuOpen reports dropdown visibility.
// It supports the desktop app frontend.
export function isMenuOpen(el: HTMLElement): boolean {
  return !el.classList.contains("hidden");
}
