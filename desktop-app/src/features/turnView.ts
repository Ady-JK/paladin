// turnView.ts renders one agent turn from its event stream. It works
// whether or not its nodes are mounted, so a session that is not on
// screen keeps building its transcript and shows it intact on return.

import { renderMarkdown } from "../core/format";
import type { CompactionInfo, RetryInfo, ToolInfo, TurnEvent, TurnInfo } from "../core/types";
import { setStatus } from "../ui/status";
import { compactionText, createCompactionDivider, createRetryNotice, createTurn, followTail, retryText, setDividerLabel } from "./chatView";
import { ToolRun } from "./toolRun";

export interface TurnViewHooks {
  // visible reports whether this turn's session is on screen.
  visible(): boolean;
  // tool sees every tool event (ask/todo routing lives with the caller).
  tool(tool: ToolInfo): void;
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
      case "chat/tool":
        if (ev.tool) this.tool(ev.tool);
        break;
      case "chat/retry":
        if (ev.retry) this.retry(ev.retry);
        break;
      case "chat/compact":
        if (ev.compaction) this.compact(ev.compaction);
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
    if (this.paintQueued) {
      this.flushSegment();
    }
    if (turn.error) {
      if (!this.reply.trim() && this.tools.size === 0) {
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
