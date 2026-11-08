import * as api from "../../core/api";
import { escapeHtml } from "../../core/format";

type EditorFile = { cwd: string; path: string; content: string };
type Options = {
  current: () => EditorFile | undefined;
  openEditor: () => void;
  goTo: (line: number, column: number) => void;
};

export function initIDEProblems(options: Options): void {
  const button = document.querySelector<HTMLButtonElement>("#ideProblemsToggle")!;
  const panel = document.querySelector<HTMLElement>("#ideProblemsPanel")!;
  let open = false;
  let request = 0;
  let timer = 0;
  let diagnostics: api.IDEDiagnostics["diagnostics"] = [];
  const update = (): void => {
    panel.hidden = !open;
    const label = open ? "Hide problems" : "Show problems";
    button.setAttribute("aria-label", label);
    button.setAttribute("title", label);
    button.setAttribute("aria-pressed", String(open));
  };
  const check = async (): Promise<void> => {
    const file = options.current();
    const id = ++request;
    diagnostics = [];
    if (!open) return;
    if (!file) {
      panel.textContent = "Open a file to check for problems.";
      return;
    }
    const content = file.content;
    panel.textContent = "Checking this file…";
    try {
      const result = await api.ideDiagnose(file.cwd, file.path, content);
      if (id !== request || !open || options.current() !== file || file.content !== content) return;
      if (result.state !== "ready") {
        const labels = {
          unavailable: "No eligible language server is available for this file.",
          disabled: "Language-server diagnostics are disabled in settings.",
          timeout: "The language server did not respond in time.",
        };
        panel.textContent = labels[result.state] ?? "Diagnostics are unavailable.";
        return;
      }
      diagnostics = result.diagnostics;
      const header = `<div class="ide-problems-head">Problems in this file · ${result.errors}${result.server ? ` <small>${escapeHtml(result.server)}</small>` : ""}</div>`;
      panel.innerHTML =
        header +
        (diagnostics.length
          ? diagnostics
              .map(
                (item, index) =>
                  `<button type="button" data-problem="${index}"><span>${escapeHtml(item.message)}</span><small>Line ${item.line}, column ${item.column}</small></button>`,
              )
              .join("")
          : `<p>No errors reported for this file.</p>`);
    } catch (error) {
      if (id === request && open) panel.textContent = `Could not check this file: ${String(error)}`;
    }
  };
  const schedule = (): void => {
    request++;
    window.clearTimeout(timer);
    if (open) {
      diagnostics = [];
      panel.textContent = "Checking this file…";
      timer = window.setTimeout(() => void check(), 650);
    }
  };
  button.addEventListener("click", () => {
    open = !open;
    if (open) options.openEditor();
    update();
    if (open) void check();
    else {
      request++;
      window.clearTimeout(timer);
    }
  });
  panel.addEventListener("click", (event) => {
    const element = (event.target as HTMLElement).closest<HTMLElement>("[data-problem]");
    const problem = element ? diagnostics[Number(element.dataset.problem)] : undefined;
    if (problem) options.goTo(problem.line, problem.column);
  });
  window.addEventListener("paladin:ide-file-changed", schedule);
  window.addEventListener("paladin:ide-buffer-changed", schedule);
  update();
}
