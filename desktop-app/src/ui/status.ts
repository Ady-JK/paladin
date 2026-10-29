// status.ts paints the sidecar connection pill in the sidebar.

import { statusDotEl, statusLabelEl } from "../core/dom";

export function setStatus(text: string, kind: "ok" | "error" | "busy"): void {
  statusLabelEl.textContent = text;

  statusDotEl.className =
    kind === "error"
      ? "h-2 w-2 flex-shrink-0 rounded-full bg-agent-rose"
      : kind === "busy"
        ? "h-2 w-2 flex-shrink-0 rounded-full bg-agent-amber"
        : "h-2 w-2 flex-shrink-0 rounded-full bg-agent-emerald";
}
