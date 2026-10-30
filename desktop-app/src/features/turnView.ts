// turnView.ts renders one agent turn from its event stream. It works
// whether or not its nodes are mounted, so a session that is not on
// screen keeps building its transcript and shows it intact on return.

import { escapeHtml, renderMarkdown, renderText } from "../core/format";
import type {
  Approval,
  BudgetNotice,
  HookNote,
  CompactionInfo,
  RetryInfo,
  SteerInfo,
  ToolInfo,
  TurnEvent,
  TurnInfo,
} from "../core/types";
import { setStatus } from "../ui/status";
import {
  compactionText,
  createCompactionDivider,
  createRetryNotice,
  createTurn,
  followTail,
  retryText,
  setDividerLabel,
} from "./chatView";
import { ToolRun } from "./toolRun";
import { createApprovalCard, focusApproval, paintApproval } from "./approvals";
import { formatUsd } from "./usage";
import { icon } from "../icons";
import { thinkingLabel } from "./thinking";

export interface TurnViewHooks {
  // visible reports whether this turn's session is on screen.
  visible(): boolean;
  // tool sees every tool event (ask/todo routing lives with the caller).
  tool(tool: ToolInfo): void;
  // sessionId answers this turn's approval cards.
  sessionId: string;
}

const SEGMENT_CLASS = "md agent-text";

export class TurnView {
  readonly wrapper: HTMLElement;
  // lastSeq is the newest event applied; older or repeated events (a
  // re-attach replay overlapping the live stream) are ignored.
  lastSeq = 0;
  private readonly container: HTMLElement;
  private segEl: HTMLElement;
  private segment = "";
  private reply = "";
  private readonly tools = new Map<string, ToolRun>();
  // run is the open group of consecutive calls; text closes it.
  private run: ToolRun | null = null;
  private retryNotice: HTMLElement | null = null;
  private divider: HTMLElement | null = null;
  private paintQueued = false;
  private readonly approvals = new Map<string, HTMLElement>();
  private readonly approvalState = new Map<string, Approval>();
  // think is the open thinking block; answer text or a tool closes it.
  private think: { el: HTMLDetailsElement; text: string; started: number; queued: boolean } | null = null;

  constructor(private readonly hooks: TurnViewHooks) {
    const { wrapper, textEl } = createTurn("agent", "");
    this.wrapper = wrapper;
    this.container = textEl.parentElement!;
    this.segEl = textEl;
    textEl.innerHTML = `<span class="thinking-ph">thinking…</span>`;
  }

  // apply routes one chat/* event; false when it was stale.
  apply(ev: TurnEvent): boolean {
    if (ev.seq && ev.seq <= this.lastSeq) {
      return false;
    }
    if (ev.seq) {
      this.lastSeq = ev.seq;
    }
    switch (ev.event) {
      case "chat/delta":
        if (typeof ev.delta === "string") this.delta(ev.delta);
        break;
      case "chat/reasoning":
        if (typeof ev.reasoning === "string") this.reasoning(ev.reasoning);
        break;
      case "chat/thinkingDropped":
        if (ev.thinkingDropped) this.thinkingDropped(ev.thinkingDropped);
        break;
      case "chat/tool":
        if (ev.tool) this.tool(ev.tool);
        break;
      case "chat/retry":
        if (ev.retry) this.retry(ev.retry);
        break;
      case "chat/compact":
        if (ev.compaction) this.compact(ev.compaction);
        break;
      case "chat/approval":
        if (ev.approval) this.approval(ev.approval);
        break;
      case "chat/steer":
        if (ev.steer) this.steer(ev.steer);
        break;
      case "chat/budget":
        if (ev.budget) this.budget(ev.budget);
        break;
      case "chat/hook":
        if (ev.hook) this.hook(ev.hook);
        break;
      default:
        return false;
    }
    return true;
  }

  private status(text: string): void {
    if (this.hooks.visible()) {
      setStatus(text, "busy");
    }
  }

  private tail(): void {
    if (this.hooks.visible() && this.wrapper.isConnected) {
      followTail();
    }
  }

  // flushSegment paints the open text segment now, before a tool row or
  // divider starts a new one; a paint still queued for the next frame
  // would otherwise land in the new, empty segment and drop this text.
  private flushSegment(): void {
    this.paintQueued = false;
    this.segEl.innerHTML = renderMarkdown(this.segment);
  }

  private newSegment(after: Node): HTMLElement {
    const el = document.createElement("div");
    el.className = SEGMENT_CLASS;
    this.container.insertBefore(el, after.nextSibling);
    return el;
  }

  // A retry notice lives until the round makes progress or the turn ends.
  private dropRetry(): boolean {
    if (!this.retryNotice) {
      return false;
    }
    this.retryNotice.remove();
    this.retryNotice = null;
    return true;
  }

  private clearRetry(): void {
    if (this.dropRetry()) {
      this.status("thinking…");
    }
  }

  private delta(text: string): void {
    this.clearRetry();
    if (text.trim()) {
      this.endThinking();
    }
    this.reply += text;
    this.segment += text;
    if (this.segment.trim()) {
      this.run = null;
    }
    if (!this.wrapper.isConnected) {
      // Detached: no frame to wait for, and no layout cost.
      this.segEl.innerHTML = renderMarkdown(this.segment);
      return;
    }
    // Deltas arrive in bursts; one paint per frame keeps it O(n).
    if (this.paintQueued) {
      return;
    }
    this.paintQueued = true;
    requestAnimationFrame(() => {
      if (!this.paintQueued) {
        return;
      }
      this.paintQueued = false;
      this.segEl.innerHTML = renderMarkdown(this.segment);
      this.tail();
    });
  }

  private tool(tool: ToolInfo): void {
    this.clearRetry();
    this.endThinking();
    this.hooks.tool(tool);
    const owner = this.tools.get(tool.id);
    if (owner) {
      owner.upsert(tool);
    } else if (this.run) {
      this.run.upsert(tool);
      this.tools.set(tool.id, this.run);
    } else {
      this.flushSegment();
      const run = new ToolRun();
      run.upsert(tool);
      this.container.insertBefore(run.el, this.segEl.nextSibling);
      this.tools.set(tool.id, run);
      this.run = run;
      this.segment = "";
      this.segEl = this.newSegment(run.el);
    }
    this.tail();
  }

  // approval places the card right under the call it gates (or at the
  // end of the text so far) and later updates it in place.
  private approval(ap: Approval): void {
    this.clearRetry();
    this.endThinking();
    this.approvalState.set(ap.id, ap);
    let card = this.approvals.get(ap.id);
    if (card) {
      paintApproval(card, ap);
    } else {
      card = createApprovalCard(this.hooks.sessionId, ap);
      this.approvals.set(ap.id, card);
      const owner = this.tools.get(ap.id);
      if (owner) {
        this.container.insertBefore(card, owner.el.nextSibling);
      } else {
        this.flushSegment();
        this.container.insertBefore(card, this.segEl.nextSibling);
        this.segment = "";
        this.segEl = this.newSegment(card);
        this.run = null;
      }
    }
    if (ap.state === "pending" && this.hooks.visible() && this.wrapper.isConnected) {
      focusApproval(card);
    }
    this.tail();
  }

  // insertBlock puts a block after the text so far; later text and tool
  // rows stream below it.
  private insertBlock(el: HTMLElement): void {
    this.clearRetry();
    this.endThinking();
    this.flushSegment();
    this.run = null;
    this.container.insertBefore(el, this.segEl.nextSibling);
    this.segment = "";
    this.segEl = this.newSegment(el);
    this.tail();
  }

  // reasoning streams the model's thinking into an open block; the first
  // piece after answer text or a tool starts a new one.
  private reasoning(text: string): void {
    if (!this.think) {
      const el = createThinkingBlock("", true);
      this.insertBlock(el);
      this.think = { el, text: "", started: Date.now(), queued: false };
    }
    const think = this.think;
    think.text += text;
    if (!this.wrapper.isConnected) {
      paintThinking(think.el, think.text);
      return;
    }
    if (think.queued) return;
    think.queued = true;
    requestAnimationFrame(() => {
      think.queued = false;
      if (this.think === think) {
        paintThinking(think.el, think.text);
        this.tail();
      }
    });
  }

  // endThinking folds the open block away once the model moves on.
  private endThinking(): void {
    const think = this.think;
    if (!think) return;
    this.think = null;
    settleThinking(think.el, think.text, Date.now() - think.started);
  }

  private thinkingDropped(level: string): void {
    const el = document.createElement("div");
    el.className = "chat-notice think-notice";
    el.dataset.notice = "thinking";
    el.setAttribute("role", "status");
    el.textContent = `The provider rejected the "${thinkingLabel(level)}" thinking level, so this model runs with its default thinking. Pick another level to try again.`;
    this.insertBlock(el);
  }

  // A steer is the user's own message joining the running turn.
  private steer(info: SteerInfo): void {
    const el = document.createElement("div");
    el.className = "steer-bubble";
    el.innerHTML = `<span class="steer-label">Steered</span><div class="steer-text">${renderText(info.display || info.message)}</div>`;
    this.insertBlock(el);
  }

  private budget(info: BudgetNotice): void {
    const el = document.createElement("div");
    el.className = "chat-notice budget-notice";
    el.dataset.notice = "budget";
    el.dataset.state = info.state;
    el.setAttribute("role", "status");
    el.innerHTML = escapeHtml(budgetText(info));
    this.insertBlock(el);
  }

  // A hook row is a quiet one-liner; the command is in the tooltip.
  private hook(note: HookNote): void {
    const el = document.createElement("div");
    el.className = "hook-row";
    el.dataset.outcome = note.outcome;
    el.title = note.command;
    el.innerHTML =
      `<span class="hook-ico">${icon("zap", "h-3 w-3")}</span>` +
      `<span class="hook-label">${escapeHtml(note.event)} hook ${escapeHtml(HOOK_VERB[note.outcome] ?? note.outcome)}</span>` +
      (note.message ? `<span class="hook-msg">${escapeHtml(note.message)}</span>` : "");
    this.insertBlock(el);
  }

  private retry(info: RetryInfo): void {
    if (!this.retryNotice) {
      this.retryNotice = createRetryNotice();
      this.container.insertBefore(this.retryNotice, this.segEl.nextSibling);
    }
    this.retryNotice.textContent = retryText(info);
    this.status(`retrying (attempt ${info.attempt}/${info.max})…`);
    this.tail();
  }

  // Compaction gets a divider in the turn; later text streams below it.
  private compact(info: CompactionInfo): void {
    this.clearRetry();
    if (info.phase === "start" || !this.divider) {
      this.flushSegment();
      this.run = null;
      this.divider = createCompactionDivider(compactionText(info));
      this.container.insertBefore(this.divider, this.segEl.nextSibling);
      this.segment = "";
      this.segEl = this.newSegment(this.divider);
    } else {
      setDividerLabel(this.divider, compactionText(info));
    }
    if (info.phase === "start") {
      this.status("compacting context…");
    } else {
      this.status("thinking…");
      this.divider = null;
    }
    this.tail();
  }

  // finish settles the turn. A failed turn returns the reason so the
  // caller can place an error row after it.
  finish(turn: TurnInfo): string | null {
    this.dropRetry();
    this.endThinking();
    // A finished turn cannot still be waiting on the user.
    for (const [id, ap] of this.approvalState) {
      const card = this.approvals.get(id);
      if (card && ap.state === "pending") paintApproval(card, { ...ap, state: "denied", decision: "interrupted" });
    }
    if (this.paintQueued) {
      this.flushSegment();
    }
    if (turn.error) {
      if (!this.reply.trim() && this.tools.size === 0 && this.approvals.size === 0) {
        this.wrapper.remove();
      } else if (!this.reply.trim()) {
        this.segEl.innerHTML = `<span class="turn-muted">interrupted — the tool calls above already ran</span>`;
      }
      return turn.error;
    }
    if (!this.reply) {
      this.reply = turn.reply || "";
      this.segment = this.reply;
      this.segEl.innerHTML = renderMarkdown(this.segment);
    }
    this.tail();
    return null;
  }
}

// createThinkingBlock builds a "Thinking" disclosure: open while it
// streams, folded once settled (history rows start settled).
export function createThinkingBlock(text: string, streaming: boolean): HTMLDetailsElement {
  const el = document.createElement("details");
  el.className = "think-block";
  el.innerHTML =
    `<summary><span class="think-block-ico">${icon("zap-line", "h-3 w-3")}</span>` +
    `<span class="think-title"></span><span class="think-time"></span>` +
    `<span class="think-caret">${icon("chevron-right", "h-3 w-3")}</span></summary>` +
    `<div class="think-body"></div>`;
  if (streaming) {
    el.open = true;
    el.dataset.state = "streaming";
    el.querySelector(".think-title")!.textContent = "Thinking";
    paintThinking(el, text);
  } else {
    settleThinking(el, text, 0);
  }
  return el;
}

function paintThinking(el: HTMLElement, text: string): void {
  el.querySelector(".think-body")!.textContent = text;
}

function settleThinking(el: HTMLDetailsElement, text: string, ms: number): void {
  el.dataset.state = "done";
  el.open = false;
  el.querySelector(".think-title")!.textContent = "Thought";
  const secs = Math.round(ms / 1000);
  el.querySelector(".think-time")!.textContent = secs >= 1 ? `for ${secs}s` : "";
  el.querySelector(".think-body")!.innerHTML = renderMarkdown(text);
}

const HOOK_VERB: Record<string, string> = {
  blocked: "blocked",
  context: "added context",
  error: "failed",
  stopped: "stopped the turn",
  message: "says",
};

// budgetText words a chat/budget notice.
function budgetText(b: BudgetNotice): string {
  if (b.state === "unknownCost") {
    return `The ${formatUsd(b.limit)} limit counts tracked cost only, and this provider reports none, so it isn't enforced; the agent keeps going.`;
  }
  if (b.state === "spendIncomplete") {
    return `This session's usage record was damaged, so tracked spend is at least ${formatUsd(b.spent)} of ${formatUsd(b.limit)}; earlier calls may be missing. The limit is checked against that lower bound, so real spend may be higher.`;
  }
  if (b.ended) {
    return `Budget reached: ${formatUsd(b.spent)} of ${formatUsd(b.limit)} tracked spend, so this message was not sent to the model. Raise the budget to continue.`;
  }
  return `Budget reached: ${formatUsd(b.spent)} of ${formatUsd(b.limit)} tracked spend. Tools are off; the agent is writing a summary of what's done and what's left.`;
}
