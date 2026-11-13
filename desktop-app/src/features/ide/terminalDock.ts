import { escapeAttr, escapeHtml } from "../../core/format";
import { inputEl } from "../../core/dom";
import { icon } from "../../icons";
import { toast } from "../../ui/toast";
import { activateTerminal } from "../sidePane/terminal";
import type { TerminalTab } from "../sidePane/types";

type DockOptions = {
  terminals: () => TerminalTab[];
  workspace: () => string;
  create: () => TerminalTab;
  close: (key: string) => void;
};

export class TerminalDock {
  private readonly root: HTMLElement;
  private readonly tabs: HTMLElement;
  private readonly body: HTMLElement;
  private readonly selected = new Map<string, string>();
  private readonly split = new Map<string, [string, string]>();
  private readonly lastSplit = new Map<string, [string, string]>();
  private enabled = false;
  private open = false;

  constructor(private readonly options: DockOptions) {
    const root = document.createElement("section");
    root.id = "ideTerminalDock";
    root.className = "ide-terminal-dock";
    root.hidden = true;
    root.setAttribute("aria-label", "Terminal panel");
    root.innerHTML = `<div class="ide-terminal-resize" role="separator" tabindex="0" aria-orientation="horizontal" aria-label="Resize terminal"></div><div class="ide-terminal-head"><strong>Terminal</strong><div class="ide-terminal-tabs" role="tablist"></div><input id="ideTerminalName" aria-label="Terminal name" maxlength="80" hidden><button type="button" id="ideTerminalSend" aria-label="Add selected output to agent" title="Add selected output to agent">To agent</button><button type="button" id="ideTerminalRename" aria-label="Rename terminal" title="Rename terminal">Rename</button><button type="button" id="ideTerminalSplit" aria-label="Split terminal" title="Split terminal">Split</button><button type="button" id="ideTerminalNew" aria-label="New terminal" title="New terminal">${icon("plus", "h-3.5 w-3.5")}</button><button type="button" id="ideTerminalClose" aria-label="Close terminal" title="Close terminal">${icon("trash", "h-3.5 w-3.5")}</button><button type="button" id="ideTerminalHide" aria-label="Hide terminal panel" title="Hide panel">×</button></div><div class="ide-terminal-body"></div>`;
    document.querySelector(".ide-editor-area")!.append(root);
    this.root = root;
    this.tabs = root.querySelector<HTMLElement>(".ide-terminal-tabs")!;
    this.body = root.querySelector<HTMLElement>(".ide-terminal-body")!;
    root.querySelector("#ideTerminalNew")!.addEventListener("click", () => options.create());
    root.querySelector("#ideTerminalSend")!.addEventListener("click", () => {
      const selected = this.active()?.term?.getSelection();
      if (!selected) {
        toast("Select terminal output first.", "error");
        return;
      }
      const limit = 16_000;
      const text = selected.slice(0, limit);
      const quoted = text
        .split("\n")
        .map((line) => `    ${line}`)
        .join("\n");
      const suffix = selected.length > limit ? "\n    [Selection truncated]" : "";
      inputEl.value = `${inputEl.value}${inputEl.value ? "\n\n" : ""}Terminal output:\n\n${quoted}${suffix}`;
      inputEl.dispatchEvent(new Event("input", { bubbles: true }));
      window.dispatchEvent(new Event("paladin:ide-show-agent"));
      inputEl.focus();
    });
    const name = root.querySelector<HTMLInputElement>("#ideTerminalName")!;
    const finishRename = (save: boolean): void => {
      const tab = this.active();
      if (save && tab && name.value.trim()) tab.title = name.value.trim().slice(0, 80);
      name.hidden = true;
      this.sync();
    };
    root.querySelector("#ideTerminalRename")!.addEventListener("click", () => {
      const tab = this.active();
      if (!tab) return;
      name.value = tab.title;
      name.hidden = false;
      name.focus();
      name.select();
    });
    name.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === "Escape") {
        event.preventDefault();
        finishRename(event.key === "Enter");
      }
    });
    name.addEventListener("blur", () => {
      if (!name.hidden) finishRename(true);
    });
    root.querySelector("#ideTerminalSplit")!.addEventListener("click", () => {
      const workspace = options.workspace();
      const pair = this.split.get(workspace);
      if (pair) {
        this.lastSplit.set(workspace, pair);
        this.split.delete(workspace);
        this.sync();
        return;
      }
      const first = this.active();
      if (!first) return;
      const previous = this.lastSplit.get(workspace);
      if (
        previous &&
        previous.includes(first.key) &&
        previous.every((key) => options.terminals().some((tab) => tab.key === key))
      ) {
        this.split.set(workspace, previous);
        this.sync();
        return;
      }
      const second = options.create();
      this.split.set(workspace, [first.key, second.key]);
      this.sync();
    });
    root.querySelector("#ideTerminalClose")!.addEventListener("click", () => {
      const tab = this.active();
      if (tab) options.close(tab.key);
    });
    root.querySelector("#ideTerminalHide")!.addEventListener("click", () => {
      this.open = false;
      this.sync();
    });
    this.tabs.addEventListener("click", (event) => {
      const key = (event.target as HTMLElement).closest<HTMLElement>("[data-ide-terminal]")?.dataset.ideTerminal;
      if (!key) return;
      this.selected.set(options.workspace(), key);
      const pair = this.split.get(options.workspace());
      if (pair && !pair.includes(key)) {
        this.lastSplit.set(options.workspace(), pair);
        this.split.delete(options.workspace());
      }
      this.sync();
    });
    this.body.addEventListener("focusin", (event) => {
      const target = event.target as Node;
      const tab = options.terminals().find((item) => item.body.contains(target));
      if (!tab || this.active() === tab) return;
      this.selected.set(options.workspace(), tab.key);
      for (const button of Array.from(this.tabs.querySelectorAll<HTMLElement>("[data-ide-terminal]"))) {
        button.setAttribute("aria-selected", String(button.dataset.ideTerminal === tab.key));
      }
    });
    const resize = root.querySelector<HTMLElement>(".ide-terminal-resize")!;
    const setHeight = (height: number): void => {
      const area = root.parentElement?.getBoundingClientRect().height ?? 500;
      root.style.height = `${Math.max(100, Math.min(Math.max(100, area - 100), height))}px`;
      window.dispatchEvent(new Event("paladin:ide-layout-changed"));
    };
    resize.addEventListener("pointerdown", (event) => {
      if (event.button === 0) resize.setPointerCapture(event.pointerId);
    });
    resize.addEventListener("pointermove", (event) => {
      if (!resize.hasPointerCapture(event.pointerId)) return;
      setHeight(root.parentElement!.getBoundingClientRect().bottom - event.clientY);
    });
    resize.addEventListener("keydown", (event) => {
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
      event.preventDefault();
      setHeight(root.getBoundingClientRect().height + (event.key === "ArrowUp" ? 16 : -16));
    });
    new ResizeObserver(() => {
      for (const tab of this.options.terminals()) {
        if (this.enabled && this.open && tab.term && !tab.body.hidden) tab.fit?.fit();
      }
    }).observe(this.body);
  }

  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
    this.sync();
  }

  show(key?: string): void {
    this.open = true;
    if (key) {
      const workspace = this.options.workspace();
      const pair = this.split.get(workspace);
      if (pair && !pair.includes(key)) {
        this.lastSplit.set(workspace, pair);
        this.split.delete(workspace);
      }
      this.selected.set(workspace, key);
    }
    this.sync();
  }

  toggle(): void {
    if (!this.options.terminals().length) {
      this.options.create();
      return;
    }
    this.open = !this.open;
    this.sync();
  }

  isOpen(): boolean {
    return this.open;
  }

  active(): TerminalTab | undefined {
    const terminals = this.options.terminals();
    return terminals.find((tab) => tab.key === this.selected.get(this.options.workspace())) ?? terminals[0];
  }

  sync(): void {
    const toggle = document.querySelector<HTMLButtonElement>("#ideTerminalToggle")!;
    toggle.setAttribute("aria-pressed", String(this.enabled && this.open));
    toggle.setAttribute("aria-label", this.open ? "Hide terminal" : "Show terminal");
    toggle.title = this.open ? "Hide terminal" : "Show terminal";
    if (!this.enabled) {
      this.root.hidden = true;
      return;
    }
    const terminals = this.options.terminals();
    const active = this.active();
    const workspace = this.options.workspace();
    let pair = this.split.get(workspace);
    if (pair && !pair.every((key) => terminals.some((tab) => tab.key === key))) {
      this.split.delete(workspace);
      pair = undefined;
    }
    if (!terminals.length) this.open = false;
    toggle.setAttribute("aria-pressed", String(this.open));
    toggle.setAttribute("aria-label", this.open ? "Hide terminal" : "Show terminal");
    toggle.title = this.open ? "Hide terminal" : "Show terminal";
    this.root.hidden = !this.open;
    this.tabs.innerHTML = terminals
      .map(
        (tab, index) =>
          `<button type="button" role="tab" aria-selected="${tab === active}" data-ide-terminal="${escapeAttr(tab.key)}" title="${escapeAttr(tab.cwd)}">${escapeHtml(tab.title === "Terminal" ? `Terminal ${index + 1}` : tab.title)}</button>`,
      )
      .join("");
    const children = Array.from(this.body.children);
    if (children.length !== terminals.length || terminals.some((tab, index) => children[index] !== tab.body)) {
      this.body.replaceChildren(...terminals.map((tab) => tab.body));
    }
    this.body.classList.toggle("is-split", !!pair);
    for (const tab of terminals) tab.body.hidden = !this.open || (pair ? !pair.includes(tab.key) : tab !== active);
    if (this.open && active)
      requestAnimationFrame(() => {
        if (!this.enabled || !this.open) return;
        for (const tab of this.options.terminals()) if (!tab.body.hidden) activateTerminal(tab);
      });
  }
}
