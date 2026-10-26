// status.ts paints the daemon connection pill in the sidebar.
// It supports the desktop app frontend.

import { statusDotEl, statusLabelEl } from "../core/dom";

// setStatus is a function in this module.
// It supports the desktop app frontend.
export function setStatus(text: string, kind: "ok" | "error" | "busy"): void {
  statusLabelEl.textContent = text;

  statusDotEl.className =
    kind === "error"
      ? "h-2 w-2 flex-shrink-0 rounded-full bg-agent-rose"
      : kind === "busy"
        ? "h-2 w-2 flex-shrink-0 animate-pulse rounded-full bg-agent-amber"
        : "h-2 w-2 flex-shrink-0 rounded-full bg-agent-emerald";
}
