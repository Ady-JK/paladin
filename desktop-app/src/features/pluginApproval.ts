// pluginApproval.ts shows a permission card for a plugin panel's tool
// call. The card is rendered by the host, never by the plugin frame, and
// answers the same broker the agent's approval cards use.

import type { Approval } from "../core/types";
import { createApprovalCard, focusApproval } from "./approvals";

let layer: HTMLElement | null = null;

// handlePluginApproval replaces any showing card: a panel can only have
// one call waiting, and a second prompt means the first is stale.
export function handlePluginApproval(sessionId: string, ap: Approval | undefined): void {
  if (!ap?.id) return;
  if (ap.state !== "pending") {
    layer?.remove();
    layer = null;
    return;
  }
  layer?.remove();
  const box = document.createElement("div");
  box.className = "plugin-approval-box";
  const label = document.createElement("div");
  label.className = "plugin-approval-label";
  label.textContent = "A plugin panel wants to run a tool";
  const card = createApprovalCard(sessionId, ap);
  box.append(label, card);
  const overlay = document.createElement("div");
  overlay.className = "plugin-approval-layer";
  overlay.append(box);
  overlay.addEventListener("click", (event) => {
    if (!(event.target as HTMLElement).closest("[data-approve]")) return;
    window.setTimeout(() => {
      overlay.remove();
      if (layer === overlay) layer = null;
    }, 900);
  });
  document.body.append(overlay);
  layer = overlay;
  focusApproval(card);
}
