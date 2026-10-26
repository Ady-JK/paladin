// toast.ts shows transient frontend notices.
// It supports the desktop app frontend.

import { toastEl, toastIconEl, toastTextEl } from "../core/dom";
import { icon } from "../icons";

// toastTimer is a let in this module.
// It supports the desktop app frontend.
let toastTimer = 0;

// toast is a function in this module.
// It supports the desktop app frontend.
export function toast(message: string, kind: "ok" | "error" = "ok"): void {
  toastTextEl.textContent = message;

  toastEl.className = `pointer-events-none fixed left-1/2 top-20 z-[60] flex max-w-lg -translate-x-1/2 items-center gap-2 rounded-lg border px-4 py-2.5 text-xs ${
    kind === "error"
      ? "border-agent-rose/50 bg-white text-agent-rose dark:bg-slate-900"
      : "border-slate-200 bg-white text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"
  }`;

  toastIconEl.innerHTML = icon(
    kind === "error" ? "xmark" : "check",
    "h-3 w-3 flex-shrink-0",
  );

  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    toastEl.classList.add("hidden");
  }, 4000);
}
