import { renderMarkdown } from "../../core/format";
import { state } from "../../core/state";
import type { MessageInfo, ToolInfo, TurnStats } from "../../core/types";
import { compactionText, createCompactionDivider, createTurn, mountLinkCards } from "../chatView";
import { createTurnStats } from "../turnStats";
import { foldWork } from "../workedFold";
import { createThinkingBlock } from "../turnView";
import { createFindingsCard } from "../findings";
import { createVisualCard } from "../visuals";
import { ToolRun } from "../toolRun";
import { mountImage, mountUserAttachments, registerImage } from "../mediaGallery";
import { paintProtection } from "../snapshotProtection";
import { createQuestionCard } from "../../adapters/agentQuestion";

// buildHistory appends a persisted transcript's rows to container.
// Rows of a session the user cannot rewind here get no revert button.
export function buildHistory(
  container: HTMLElement,
  history: MessageInfo[],
  revertable = true,
  sessionId = state.sessionId,
): void {
  let agentBody: HTMLElement | null = null;
  // run is the open group of consecutive calls; any text closes it.
  let run: ToolRun | null = null;
  // pendingStats closes the previous user turn: its summary row goes
  // just before the next user row (or at the end of the last turn).
  let pendingStats: HTMLElement | null = null;
  let agentStats: TurnStats | undefined;
  // A steer is stored as a user row inside the same turn. Its outcome is
  // known only at the final stats row: mark the entire persisted span before
  // drawing so pre-steer segments of a stopped turn never fold prematurely.
  const incompleteRows = new Set<number>();
  let spanStart = 0;
  for (const [index, message] of history.entries()) {
    if (!message.stats) continue;
    if (message.stats.incomplete) {
      for (let i = spanStart; i <= index; i++) incompleteRows.add(i);
    }
    spanStart = index + 1;
  }
  let rowIncomplete = false;
  let agentIncomplete = false;
  const finishAgent = (): void => {
    if (agentBody && !agentIncomplete) foldWork(agentBody, agentStats);
    agentBody = null;
    agentStats = undefined;
    agentIncomplete = false;
    run = null;
  };

  const openAgent = (text: string): void => {
    const { wrapper, textEl } = createTurn("agent", text);
    container.append(wrapper);
    agentBody = textEl.parentElement!;
    agentIncomplete = rowIncomplete;
  };

  const pushAgentText = (text: string): void => {
    if (text.trim()) {
      run = null;
    }
    if (!agentBody) {
      openAgent(text);
      return;
    }
    const el = document.createElement("div");
    el.className = "md agent-text";
    el.innerHTML = renderMarkdown(text);
    agentBody.append(el);
    mountLinkCards(agentBody, text);
  };

  const pushAgentTool = (tool: ToolInfo): void => {
    if (!agentBody) {
      openAgent("");
    }
    if (tool.name === "agent_question") {
      agentBody!.append(createQuestionCard(sessionId, tool));
      run = null;
      return;
    }
    if (tool.name === "report_findings" || tool.name === "visual") {
      agentBody!.append(tool.name === "visual" ? createVisualCard(tool) : createFindingsCard(sessionId, tool));
      run = null;
      return;
    }
    if (!run) {
      run = new ToolRun();
      agentBody!.append(run.el);
    }
    run.upsert(tool);
  };

  for (const [index, message] of history.entries()) {
    rowIncomplete = message.incomplete === true || incompleteRows.has(index);
    // Stats are anchored on a turn's last row. Close its agent body before
    // the next row, even when a continuation has no new user message.
    // Legacy rows without stats are still separated by their user messages.
    const compaction = message.role === "system" && message.kind === "compaction";
    const boundary = message.role === "user" || compaction;
    const endedTurn = pendingStats !== null && boundary;
    if (pendingStats) {
      finishAgent();
      container.append(pendingStats);
      pendingStats = null;
    }
    if (message.role === "user") {
      finishAgent();
      const turn = createTurn("user", message.content, {
        index: message.index,
        revertable: revertable && message.index !== undefined,
      });
      if (message.index !== undefined) turn.wrapper.dataset.turnIndex = String(message.index);
      if (message.protection) paintProtection(turn.wrapper, message.protection);
      mountUserAttachments(turn.wrapper, sessionId, message.attachments);
      container.append(turn.wrapper);
    } else if (message.role === "assistant" && message.kind === "thinking") {
      if (!agentBody) {
        openAgent("");
      }
      run = null;
      agentBody!.append(createThinkingBlock(message.content, false));
    } else if (message.role === "assistant") {
      pushAgentText(message.content);
    } else if (compaction) {
      // Divider where older context was summarized; later agent text
      // and tools append below it.
      run = null;
      const label = message.compaction ? compactionText({ phase: "done", ...message.compaction }) : "Context compacted";
      const divider = createCompactionDivider(label, message.content);
      if (endedTurn) {
        // A compaction after a finished turn is a boundary between turns,
        // not part of that turn: it sits after the turn's footer (stats
        // and change card), so the card never ends up below the summary.
        agentBody = null;
        container.append(divider);
      } else {
        if (!agentBody) {
          openAgent("");
        }
        agentBody!.append(divider);
      }
    } else if (message.role === "tool") {
      if (message.image) registerImage(sessionId, message.image.path);
      pushAgentTool({
        // A question card is answered by its call id, so it keeps the real one.
        id: message.name === "agent_question" && message.toolCallId ? message.toolCallId : `h${index}`,
        name: message.name || "tool",
        arguments: message.arguments || "{}",
        result: message.content,
        status: message.status === "failed" ? "failed" : "ok",
      });
      if (message.image && agentBody) mountImage(agentBody, sessionId, message.image.path, "Browser screenshot");
    }
    if (message.stats) {
      pendingStats = createTurnStats(message.stats);
      agentStats = message.stats;
    }
  }
  finishAgent();
  if (pendingStats) container.append(pendingStats);
}
