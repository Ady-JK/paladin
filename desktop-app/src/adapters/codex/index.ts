// The Codex adapter's UI: everything specific to running a session on the Codex
// CLI lives in this folder, and everything it draws uses the app's own cards and
// colours. It is the one place that knows what is Codex about the interface:
//
//   harness.ts  Codex mode (its model list, composer hint, permissions) and
//               whether the CLI is installed;
//   skills.ts   the `$` skill menu.
//
// The questions Codex asks and the commands it runs reach the conversation as the
// shared cards in ../ (agentQuestion.ts, the shell card), translated by the core's
// codex adapter, so another agent needs no new card to be drawn the same way.
// See docs/AGENT-ADAPTERS.md.

import { initCodexSkills } from "./skills";
import { refreshCodexAvailability } from "./harness";

// initCodexAdapter wires the adapter's UI once at boot.
export function initCodexAdapter(): void {
  void refreshCodexAvailability();
  initCodexSkills();
}
