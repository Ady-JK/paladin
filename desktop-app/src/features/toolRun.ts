// toolRun.ts groups consecutive tool calls of one turn. A lone call is
// a plain row; from the second call on, the run folds behind a single
// summary line ("Ran 2 commands, read 1 file") that expands to the rows.

import type { ToolInfo, ToolView } from "../core/types";
import { icon } from "../icons";
import { createToolActivity, runLabel, toolLabel } from "./tools";

// setGlyph swaps the icon slot only when its kind changes, so a live
// spinner keeps rotating across updates instead of restarting.
function setGlyph(slot: HTMLElement, kind: string, html: () => string): void {
  if (slot.dataset.glyph === kind) {
    return;
  }
  slot.dataset.glyph = kind;
  slot.innerHTML = html();
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
      this.views.set(tool.id, view);
      this.inner.append(view.el);
    }
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
    const grouped = tools.length > 1;
    this.el.classList.toggle("is-group", grouped);
    this.el.classList.toggle("is-open", grouped && this.open);
    this.list.classList.toggle("is-folded", grouped && !this.open);
    this.list.setAttribute("aria-hidden", String(grouped && !this.open));
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
    setGlyph(this.ico, running ? "spin" : "tools", () =>
      running ? '<span class="tool-spin"></span>' : icon("tl-tools", "h-4 w-4"),
    );
    let now = "";
    if (running) {
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
