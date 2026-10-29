// toolRun.ts groups consecutive tool calls of one turn. A lone call is
// a plain row; from the second call on, the run folds behind a single
// summary line ("Ran 2 commands, read 1 file") that expands to the rows.

import type { ToolInfo, ToolView } from "../core/types";
import { icon } from "../icons";
import { createToolActivity, runLabel, toolLabel } from "./tools";

export class ToolRun {
  readonly el: HTMLElement;
  private readonly head: HTMLButtonElement;
  private readonly list: HTMLElement;
  private readonly views = new Map<string, ToolView>();
  private readonly infos = new Map<string, ToolInfo>();
  private open = false;

  constructor() {
    this.el = document.createElement("div");
    this.el.className = "tool-run";
    this.el.dataset.toolRun = "";

    this.head = document.createElement("button");
    this.head.type = "button";
    this.head.className = "tool-run-head";
    this.head.setAttribute("aria-expanded", "false");
    this.head.addEventListener("click", () => {
      this.open = !this.open;
      this.paint();
    });

    this.list = document.createElement("div");
    this.list.className = "tool-run-list";
    this.el.append(this.list);
  }

  has(id: string): boolean {
    return this.views.has(id);
  }

  // upsert adds a call or refreshes one already in the run.
  upsert(tool: ToolInfo): void {
    this.infos.set(tool.id, tool);
    const existing = this.views.get(tool.id);
    if (existing) {
      existing.update(tool);
    } else {
      const view = createToolActivity(tool);
      this.views.set(tool.id, view);
      this.list.append(view.el);
    }
    this.paint();
  }

  private paint(): void {
    const tools = [...this.infos.values()];
    const grouped = tools.length > 1;
    this.el.classList.toggle("is-group", grouped);
    this.el.classList.toggle("is-open", grouped && this.open);
    this.list.hidden = grouped && !this.open;
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
    const glyph = running ? '<span class="tool-spin"></span>' : icon("tl-tools", "h-4 w-4");
    let now = "";
    if (running) {
      const label = toolLabel(running);
      now = label.chip || label.detail;
    }
    this.head.innerHTML =
      `<span class="tool-ico">${glyph}</span><span class="tool-verb"></span>` +
      `<span class="tool-now"></span><span class="tool-fail"></span>` +
      `<span class="tool-caret">${icon("chevron-right", "h-3 w-3")}</span>`;
    this.head.querySelector(".tool-verb")!.textContent = runLabel(tools);
    const nowEl = this.head.querySelector<HTMLElement>(".tool-now")!;
    nowEl.textContent = now;
    nowEl.hidden = !now;
    const failEl = this.head.querySelector<HTMLElement>(".tool-fail")!;
    failEl.textContent = failed ? `${failed} failed` : "";
    failEl.hidden = !failed;
  }
}
