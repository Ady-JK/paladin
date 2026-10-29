// toast.ts shows transient frontend notices.

import { toastEl, toastIconEl, toastTextEl } from "../core/dom";
import { icon } from "../icons";

let toastTimer = 0;

export function toast(message: string, kind: "ok" | "error" = "ok"): void {
  toastTextEl.textContent = message;

  toastEl.className = `pointer-events-none fixed left-1/2 top-20 z-[60] flex max-w-lg -translate-x-1/2 items-center gap-2 rounded-lg border px-4 py-2.5 text-xs backdrop-blur-md ${
    kind === "error"
      ? "border-agent-rose/50 bg-white/85 text-agent-rose dark:bg-slate-900/85"
      : "border-slate-200/70 bg-white/85 text-slate-700 dark:border-slate-700/70 dark:bg-slate-900/85 dark:text-slate-200"
  }`;
  // Restart the spring each time the toast fires.
  toastEl.classList.remove("toast-in");
  void toastEl.offsetWidth;
  toastEl.classList.add("toast-in");

  toastIconEl.innerHTML = icon(
    kind === "error" ? "xmark" : "check",
    "h-3 w-3 flex-shrink-0",
  );

  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    toastEl.classList.add("hidden");
  }, 4000);
}
