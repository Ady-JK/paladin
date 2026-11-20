// toolRun.ts groups consecutive tool calls of one turn. Commands and
// multiple calls fold behind a summary that expands to the detailed rows.

import type { ToolInfo, ToolView } from "../core/types";
import { tryParseJson } from "../core/format";
import { icon } from "../icons";
import { createToolActivity, runLabel, toolIcon, toolLabel, toolReason } from "./tools";

// setGlyph swaps the icon slot only when its kind changes, so a live
// spinner keeps rotating across updates instead of restarting.
function setGlyph(slot: HTMLElement, kind: string, html: () => string): void {
  if (slot.dataset.glyph === kind) {
    return;
  }
  slot.dataset.glyph = kind;
  slot.innerHTML = html();
}

// Step is one call inside an expanded group: a head showing the model's
// reason for the call (tool icon, one line, chevron) over the full tool
// row, which unfolds when the head is clicked. A call with no reason has
// no head and shows its tool row directly (see .tool-step in chat.css).
type Step = { el: HTMLElement; head: HTMLButtonElement; ico: HTMLElement; text: HTMLElement; fail: HTMLElement };

function createStep(view: ToolView): Step {
  const el = document.createElement("div");
  el.className = "tool-step";
  const head = document.createElement("button");
  head.type = "button";
  head.className = "tool-step-head";
  head.setAttribute("aria-expanded", "false");
  head.innerHTML =
    `<span class="tool-ico"></span><span class="tool-step-text"></span><span class="tool-fail" hidden></span>` +
    `<span class="tool-step-caret">${icon("chevron-right", "h-3.5 w-3.5")}</span>`;
  const body = document.createElement("div");
  body.className = "tool-step-body";
  body.append(view.el);
  head.addEventListener("click", () => {
    const open = !el.classList.contains("is-open");
    el.classList.toggle("is-open", open);
    head.setAttribute("aria-expanded", String(open));
  });
  el.append(head, body);
  return {
    el,
    head,
    ico: head.querySelector<HTMLElement>(".tool-ico")!,
    text: head.querySelector<HTMLElement>(".tool-step-text")!,
    fail: head.querySelector<HTMLElement>(".tool-fail")!,
  };
}

function paintStep(step: Step, tool: ToolInfo): void {
  const reason = toolReason(tool);
  step.el.classList.toggle("has-reason", reason !== "");
  step.el.dataset.status = tool.status;
  setText(step.text, reason);
  step.head.title = reason;
  const glyph = tool.status === "running" ? "spin" : toolIcon(tool.name);
  setGlyph(step.ico, glyph, () =>
    tool.status === "running" ? '<span class="tool-spin"></span>' : icon(toolIcon(tool.name), "h-4 w-4"),
  );
  const failed = tool.status === "failed";
  setText(step.fail, failed ? "failed" : "");
  step.fail.hidden = !failed;
}

export class ToolRun {
  readonly el: HTMLElement;
  private readonly head: HTMLButtonElement;
  private readonly list: HTMLElement;
  private readonly inner: HTMLElement;
  private readonly ico: HTMLElement;
  private readonly verb: HTMLElement;
  private readonly now: HTMLElement;
  private readonly fail: HTMLElement;
  private readonly views = new Map<string, ToolView>();
  private readonly steps = new Map<string, Step>();
  private readonly infos = new Map<string, ToolInfo>();
  private open = false;
  private queued = false;

  constructor() {
    this.el = document.createElement("div");
    this.el.className = "tool-run";
    this.el.dataset.toolRun = "";

    this.head = document.createElement("button");
    this.head.type = "button";
    this.head.className = "tool-run-head";
    this.head.setAttribute("aria-expanded", "false");
    this.head.innerHTML =
      `<span class="tool-ico"></span><span class="tool-verb"></span>` +
      `<span class="tool-now"></span><span class="tool-fail"></span>` +
      `<span class="tool-caret">${icon("chevron-right", "h-3 w-3")}</span>`;
    this.ico = this.head.querySelector<HTMLElement>(".tool-ico")!;
    this.verb = this.head.querySelector<HTMLElement>(".tool-verb")!;
    this.now = this.head.querySelector<HTMLElement>(".tool-now")!;
    this.fail = this.head.querySelector<HTMLElement>(".tool-fail")!;
    this.head.addEventListener("click", () => {
      this.open = !this.open;
      this.paint();
    });

    // list is a 0fr/1fr grid so folding animates its height; inner clips.
    this.list = document.createElement("div");
    this.list.className = "tool-run-list";
    this.inner = document.createElement("div");
    this.inner.className = "tool-run-inner";
    this.list.append(this.inner);
    this.el.append(this.list);
  }

  has(id: string): boolean {
    return this.views.has(id);
  }

  // jobIDs lists the shell calls in this run that reported a job id and a
  // running status: the ones whose badge can go stale when the job exits.
  // A call's own result is the snapshot taken when the command detached,
  // so it never learns that the process is gone.
  runningJobIDs(): string[] {
    const out: string[] = [];
    for (const tool of this.infos.values()) {
      if (tool.name !== "shell" || tool.status === "running") continue;
      const result = tryParseJson(tool.result);
      const res = result && typeof result === "object" ? result : null;
      const id = typeof res?.job_id === "string" ? res.job_id : "";
      if (id && res?.status === "running") out.push(id);
    }
    return out;
  }

  // markJobFinished repaints a call whose job is no longer running. The
  // row keeps its command and log path; only the live status is replaced,
  // so the terminal card reads as finished instead of still running.
  markJobFinished(jobID: string): void {
    for (const [id, tool] of this.infos) {
      if (tool.name !== "shell") continue;
      const result = tryParseJson(tool.result);
      const res = result && typeof result === "object" ? result : null;
      if (typeof res?.job_id !== "string" || res.job_id !== jobID || res.status !== "running") continue;
      // `finished` is what the badge reads: the job left the running list
      // but its exit code was never observed here, so it must not claim
      // "exit 0".
      const next: ToolInfo = { ...tool, result: JSON.stringify({ ...res, status: "done", finished: true }) };
      this.infos.set(id, next);
      this.views.get(id)?.update(next);
    }
    this.schedule();
  }

  // upsert adds a call or refreshes one already in the run. Rows are
  // appended in arrival order and never moved; the summary repaints at
  // most once per frame when parallel calls report in bursts.
  upsert(tool: ToolInfo): void {
    this.infos.set(tool.id, tool);
    const existing = this.views.get(tool.id);
    if (existing) {
      existing.update(tool);
    } else {
      const view = createToolActivity(tool);
      const step = createStep(view);
      this.views.set(tool.id, view);
      this.steps.set(tool.id, step);
      this.inner.append(step.el);
    }
    paintStep(this.steps.get(tool.id)!, tool);
    this.schedule();
  }

  private schedule(): void {
    // Structural paints (a lone row, the moment it becomes a group) are
    // synchronous so the run never flashes unstyled; later refreshes batch.
    if (!this.el.isConnected || !this.head.isConnected) {
      this.paint();
      return;
    }
    if (this.queued) {
      return;
    }
    this.queued = true;
    requestAnimationFrame(() => {
      if (this.queued) {
        this.paint();
      }
    });
  }

  private paint(): void {
    this.queued = false;
    const tools = [...this.infos.values()];
    const grouped = tools.length > 1 || (tools.length === 1 && tools[0].name === "shell");
    this.el.classList.toggle("is-group", grouped);
    this.el.classList.toggle("is-open", grouped && this.open);
    this.el.classList.toggle(
      "has-reasons",
      tools.some((tool) => toolReason(tool) !== ""),
    );
    this.list.classList.toggle("is-folded", grouped && !this.open);
    this.list.setAttribute("aria-hidden", String(grouped && !this.open));
    this.list.inert = grouped && !this.open;
    if (!grouped) {
      this.head.remove();
      return;
    }
    if (!this.head.isConnected) {
      this.el.prepend(this.head);
    }
    this.head.setAttribute("aria-expanded", String(this.open));

    const running = [...tools].reverse().find((t) => t.status === "running");
    const failed = tools.filter((t) => t.status === "failed").length;
    const glyph = tools.every((tool) => tool.name === "shell") ? "tl-term" : "tl-tools";
    setGlyph(this.ico, running ? "spin" : glyph, () =>
      running ? '<span class="tool-spin"></span>' : icon(glyph, "h-4 w-4"),
    );
    let now = "";
    if (running && running.name !== "shell") {
      const label = toolLabel(running);
      now = label.chip || label.detail;
    }
    setText(this.verb, runLabel(tools));
    setText(this.now, now);
    this.now.hidden = !now;
    setText(this.fail, failed ? `${failed} failed` : "");
    this.fail.hidden = !failed;
  }
}

function setText(el: HTMLElement, text: string): void {
  if (el.textContent !== text) {
    el.textContent = text;
  }
}
