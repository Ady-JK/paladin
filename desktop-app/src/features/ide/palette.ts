import * as api from "../../core/api";
import { escapeAttr, escapeHtml } from "../../core/format";
import { state } from "../../core/state";

type Item = { label: string; detail?: string; run: () => void };
type Options = { enabled: () => boolean; openFile: (path: string) => void; goToLine: (line: number) => void };

export function initIDEPalette(options: Options): void {
  const dialog = document.createElement("div");
  dialog.className = "ide-palette-backdrop";
  dialog.hidden = true;
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-label", "Quick open");
  dialog.setAttribute("aria-modal", "true");
  dialog.innerHTML = `<div class="ide-palette"><input aria-label="Search files or commands" autocomplete="off" spellcheck="false"><div class="ide-palette-results"></div></div>`;
  document.body.append(dialog);
  const input = dialog.querySelector<HTMLInputElement>("input")!;
  const results = dialog.querySelector<HTMLElement>(".ide-palette-results")!;
  let entries: Item[] = [];
  let selected = 0;
  let serial = 0;
  let timer = 0;
  let prior: HTMLElement | null = null;
  const commands: Item[] = [
    { label: "Toggle terminal", run: () => document.querySelector<HTMLElement>("#ideTerminalToggle")?.click() },
    { label: "Search workspace", run: () => window.dispatchEvent(new Event("paladin:ide-search-open")) },
    { label: "Show or hide changes", run: () => document.querySelector<HTMLElement>("#ideChangesToggle")?.click() },
    { label: "Switch session", run: () => document.querySelector<HTMLElement>("#ideShowSessions")?.click() },
    { label: "Change workspace", run: () => document.querySelector<HTMLElement>("#ideChooseFolder")?.click() },
    { label: "Toggle explorer", run: () => document.querySelector<HTMLElement>("#ideExplorerToggle")?.click() },
    { label: "Toggle agent", run: () => document.querySelector<HTMLElement>("#ideAgentToggle")?.click() },
  ];
  const paint = (): void => {
    results.innerHTML = entries.length
      ? entries
          .map(
            (item, index) =>
              `<button type="button" data-index="${index}" class="${index === selected ? "is-selected" : ""}" title="${escapeAttr(item.detail ?? item.label)}"><span>${escapeHtml(item.label)}</span>${item.detail ? `<small>${escapeHtml(item.detail)}</small>` : ""}</button>`,
          )
          .join("")
      : `<p>No results</p>`;
    results.querySelector<HTMLElement>(".is-selected")?.scrollIntoView({ block: "nearest" });
  };
  const close = (): void => {
    serial++;
    window.clearTimeout(timer);
    dialog.hidden = true;
    prior?.focus({ preventScroll: true });
  };
  const search = async (): Promise<void> => {
    const query = input.value.trim();
    const id = ++serial;
    selected = 0;
    if (query.startsWith(">")) {
      const term = query.slice(1).trim().toLowerCase();
      entries = commands.filter((command) => command.label.toLowerCase().includes(term));
      paint();
      return;
    }
    if (query.startsWith(":")) {
      const line = Number(query.slice(1));
      entries =
        Number.isSafeInteger(line) && line > 0
          ? [{ label: `Go to line ${line}`, run: () => options.goToLine(line) }]
          : [];
      paint();
      return;
    }
    try {
      const files = await api.fileComplete(query, state.cwd || null, 50);
      if (id !== serial || dialog.hidden) return;
      entries = files
        .filter((file) => !file.isDir && !file.outside)
        .map((file) => ({ label: file.path, run: () => options.openFile(file.resolvedPath) }));
      paint();
    } catch {
      if (id === serial) {
        entries = [];
        results.textContent = "Could not search files.";
      }
    }
  };
  const open = (mode: "files" | "commands" | "line"): void => {
    prior = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.hidden = false;
    input.value = mode === "commands" ? ">" : mode === "line" ? ":" : "";
    input.placeholder =
      mode === "commands"
        ? "Search commands"
        : mode === "line"
          ? "Go to line"
          : "Search workspace files (type > for commands)";
    entries = [];
    selected = 0;
    input.focus();
    void search();
  };
  const run = (index: number): void => {
    const item = entries[index];
    if (!item) return;
    close();
    item.run();
  };
  input.addEventListener("input", () => {
    serial++;
    window.clearTimeout(timer);
    entries = [];
    results.textContent = "Searching…";
    timer = window.setTimeout(() => void search(), 100);
  });
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Tab") {
      const targets = [input, ...Array.from(results.querySelectorAll<HTMLButtonElement>("button"))];
      const index = targets.indexOf(document.activeElement as HTMLInputElement);
      const next = (index + (event.shiftKey ? -1 : 1) + targets.length) % targets.length;
      event.preventDefault();
      targets[next].focus();
    } else if (event.key === "Escape") {
      event.preventDefault();
      close();
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (entries.length)
        selected = (selected + (event.key === "ArrowDown" ? 1 : -1) + entries.length) % entries.length;
      paint();
    } else if (event.key === "Enter") {
      event.preventDefault();
      window.clearTimeout(timer);
      const button = (event.target as HTMLElement).closest<HTMLElement>("[data-index]");
      if (button) run(Number(button.dataset.index));
      else if (entries.length) run(selected);
      else {
        const query = input.value;
        void search().then(() => {
          if (!dialog.hidden && input.value === query) run(selected);
        });
      }
    }
  });
  results.addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLElement>("[data-index]");
    if (button) run(Number(button.dataset.index));
  });
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) close();
  });
  document.addEventListener(
    "keydown",
    (event) => {
      if (
        options.enabled() &&
        event.ctrlKey &&
        !event.metaKey &&
        !event.altKey &&
        !event.shiftKey &&
        event.key.toLowerCase() === "g"
      ) {
        event.preventDefault();
        event.stopPropagation();
        if (!dialog.hidden) close();
        else open("line");
        return;
      }
      if (!options.enabled() || !(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== "p")
        return;
      event.preventDefault();
      if (!dialog.hidden) close();
      else open(event.shiftKey ? "commands" : "files");
    },
    true,
  );
}
