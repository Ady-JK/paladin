import { renderMarkdown } from "../../core/format";
import { state } from "../../core/state";
import type { MessageInfo, ToolInfo } from "../../core/types";
import { compactionText, createCompactionDivider, createTurn, mountLinkCards } from "../chatView";
import { createTurnStats } from "../turnStats";
import { createThinkingBlock } from "../turnView";
import { createFindingsCard } from "../findings";
import { createVisualCard } from "../visuals";
import { ToolRun } from "../toolRun";
import { mountImage, mountUserImage, registerImage } from "../mediaGallery";
import { paintProtection } from "../snapshotProtection";
import { createPluginCard, hasPluginCard } from "../pluginCards";

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

  const openAgent = (text: string): void => {
    const { wrapper, textEl } = createTurn("agent", text);
    container.append(wrapper);
    agentBody = textEl.parentElement!;
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
    if (hasPluginCard(tool.name)) {
      agentBody!.append(createPluginCard(sessionId, tool));
      run = null;
      return;
    }
    if (tool.name === "report_findings" || tool.name === "visual") {
      agentBody!.append(tool.name === "visual" ? createVisualCard(tool) : createFindingsCard(sessionId, tool));
      run = null;
      return;
    }
    // Cap the live DOM copy: the full result stays in the transcript.
    if (tool.result && tool.result.length > 8000) {
      tool = {
        ...tool,
        result: `${tool.result.slice(0, 8000)}\n…(truncated, kept in history)`,
      };
    }
    if (!run) {
      run = new ToolRun();
      agentBody!.append(run.el);
    }
    run.upsert(tool);
  };

  for (const [index, message] of history.entries()) {
    // A turn's summary closes it, so a pending one is flushed as soon as
    // the next turn begins: a user row starts one, and a row that
    // carries its own stats is the last row of one (see the core's
    // HistoryViewStats, which anchors the summary on the turn's end).
    if (pendingStats && (message.role === "user" || message.stats)) {
      container.append(pendingStats);
      pendingStats = null;
    }
    if (message.role === "user") {
      agentBody = null;
      run = null;
      const turn = createTurn("user", message.content, {
        index: message.index,
        revertable: revertable && message.index !== undefined,
      });
      if (message.index !== undefined) turn.wrapper.dataset.turnIndex = String(message.index);
      if (message.protection) paintProtection(turn.wrapper, message.protection);
      for (const attachment of message.attachments ?? []) {
        if (attachment.image) mountUserImage(turn.wrapper, sessionId, attachment.image.path, attachment.name);
        else {
          const file = document.createElement("div");
          file.className = "session-file-card";
          file.textContent = `${attachment.name} · ${attachment.path}`;
          turn.textEl.append(file);
        }
      }
      container.append(turn.wrapper);
    } else if (message.role === "assistant" && message.kind === "thinking") {
      if (!agentBody) {
        openAgent("");
      }
      run = null;
      agentBody!.append(createThinkingBlock(message.content, false));
    } else if (message.role === "assistant") {
      pushAgentText(message.content);
    } else if (message.role === "system" && message.kind === "compaction") {
      // Divider where older context was summarized; later agent text
      // and tools append below it.
      if (!agentBody) {
        openAgent("");
      }
      run = null;
      const label = message.compaction ? compactionText({ phase: "done", ...message.compaction }) : "Context compacted";
      agentBody!.append(createCompactionDivider(label, message.content));
    } else if (message.role === "tool") {
      if (message.image) registerImage(sessionId, message.image.path);
      pushAgentTool({
        id: `h${index}`,
        name: message.name || "tool",
        arguments: message.arguments || "{}",
        result: message.content,
        status: message.status === "failed" ? "failed" : "ok",
      });
      if (message.image && agentBody) mountImage(agentBody, sessionId, message.image.path, "Browser screenshot");
    }
    if (message.stats) pendingStats = createTurnStats(message.stats);
  }
  if (pendingStats) container.append(pendingStats);
}
