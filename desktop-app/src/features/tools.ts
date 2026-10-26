// tools.ts renders tool-call activity cards and file diffs.
// It is pure DOM building: no backend calls and no shared state.

import { escapeHtml, ordinal, prettyJson, splitLines, tryParseJson } from "../core/format";
import type { DiffRow, MatchSpan, ToolInfo, ToolView } from "../core/types";
import { icon } from "../icons";

// toolSummary is a function in this module.
// It supports the desktop app frontend.
export function toolSummary(name: string, raw: string): string {
  const short = (text: string) =>
    text.length > 120 ? `${text.slice(0, 120)}…` : text;

  try {
    const args = JSON.parse(raw) as Record<string, unknown>;
    if (name === "shell") {
      if (typeof args.cmd === "string" && args.cmd.trim()) {
        return short(args.cmd.trim());
      }
      const action =
        typeof args.action === "string" && args.action ? args.action : "run";
      const job =
        typeof args.job_id === "string" && args.job_id ? ` ${args.job_id}` : "";
      return `${action}${job}`;
    }
    if (typeof args.path === "string") {
      return args.path;
    }
    for (const value of Object.values(args)) {
      if (typeof value === "string" && value) {
        return value;
      }
    }
  } catch {
  }
  return short(raw);
}

// lineDiff diffs old lines against new lines with longest-common-subsequence.
// It returns context/del/add rows; huge inputs fall back to whole-block replace.
export function lineDiff(oldLines: string[], newLines: string[]): DiffRow[] {
  const n = oldLines.length;
  const m = newLines.length;
  if (n * m > 250000) {
    const rows: DiffRow[] = oldLines.map((text, i) => ({ kind: "del", oldNo: i + 1, newNo: null, text }));
    for (let j = 0; j < m; j++) {
      rows.push({ kind: "add", oldNo: null, newNo: j + 1, text: newLines[j] ?? "" });
    }
    return rows;
  }

  const widths = new Array<number>(m + 1).fill(0);
  const table: number[][] = [];
  for (let i = 0; i <= n; i++) {
    table.push([...widths]);
  }
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      const row = table[i];
      const next = table[i + 1];
      if (!row || !next) {
        continue;
      }
      row[j] = oldLines[i] === newLines[j] ? (next[j + 1] ?? 0) + 1 : Math.max(next[j] ?? 0, row[j + 1] ?? 0);
    }
  }

  const rows: DiffRow[] = [];
  let i = 0;
  let j = 0;
  let oldNo = 1;
  let newNo = 1;
  while (i < n && j < m) {
    if (oldLines[i] === newLines[j]) {
      rows.push({ kind: "context", oldNo, newNo, text: oldLines[i] ?? "" });
      i++;
      j++;
      oldNo++;
      newNo++;
    } else if ((table[i + 1]?.[j] ?? 0) >= (table[i]?.[j + 1] ?? 0)) {
      rows.push({ kind: "del", oldNo, newNo: null, text: oldLines[i] ?? "" });
      i++;
      oldNo++;
    } else {
      rows.push({ kind: "add", oldNo: null, newNo, text: newLines[j] ?? "" });
      j++;
      newNo++;
    }
  }
  while (i < n) {
    rows.push({ kind: "del", oldNo, newNo: null, text: oldLines[i] ?? "" });
    i++;
    oldNo++;
  }
  while (j < m) {
    rows.push({ kind: "add", oldNo: null, newNo, text: newLines[j] ?? "" });
    j++;
    newNo++;
  }
  return rows;
}

// diffCell renders one half of a side-by-side pair row.
// Empty halves render as blank gap cells to keep both panes aligned.
function diffCell(row: DiffRow | undefined, side: "old" | "new"): string {
  if (!row) {
    return `<div data-diffside="${side}" data-diffline="gap" class="min-w-0 px-2 py-px">&nbsp;</div>`;
  }
  const isDel = row.kind === "del";
  const cls = isDel
    ? "bg-red-500/10 text-red-700 dark:text-red-300"
    : "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300";
  const no = side === "old" ? row.oldNo : row.newNo;
  const sign = side === "old" ? "-" : "+";
  return (
    `<div data-diffside="${side}" data-diffline="${row.kind}" class="flex min-w-0 items-start ${cls}">` +
    `<span class="w-8 shrink-0 select-none pr-2 text-right opacity-60">${no === null ? "" : no}</span>` +
    `<span class="w-4 shrink-0 select-none opacity-70">${sign}</span>` +
    `<code class="min-w-0 flex-1 whitespace-pre-wrap break-all">${escapeHtml(row.text) || " "}</code></div>`
  );
}

// sideBySideDiffHtml renders diff rows in old-left, new-right panes.
// Removed lines sit on the left in red and added lines on the right in
// green; context lines span the full width like a split diff editor.
function sideBySideDiffHtml(rows: DiffRow[]): string {
  let html = "";
  let i = 0;
  while (i < rows.length) {
    const row = rows[i];
    if (!row) {
      break;
    }
    if (row.kind === "context") {
      html +=
        `<div data-diffline="context" class="flex min-w-0 items-start text-slate-500 dark:text-slate-400">` +
        `<span class="w-8 shrink-0 select-none pr-2 text-right opacity-60">${row.oldNo ?? ""}</span>` +
        `<span class="w-8 shrink-0 select-none pr-2 text-right opacity-60">${row.newNo ?? ""}</span>` +
        `<span class="w-4 shrink-0 select-none opacity-70"> </span>` +
        `<code class="min-w-0 flex-1 whitespace-pre-wrap break-all">${escapeHtml(row.text) || " "}</code></div>`;
      i++;
      continue;
    }
    const dels: DiffRow[] = [];
    const adds: DiffRow[] = [];
    while (i < rows.length && rows[i]?.kind === "del") {
      dels.push(rows[i] as DiffRow);
      i++;
    }
    while (i < rows.length && rows[i]?.kind === "add") {
      adds.push(rows[i] as DiffRow);
      i++;
    }
    const pairs = Math.max(dels.length, adds.length);
    for (let k = 0; k < pairs; k++) {
      html +=
        `<div class="grid min-w-0 grid-cols-2 gap-px">` +
        diffCell(dels[k], "old") +
        diffCell(adds[k], "new") +
        `</div>`;
    }
  }
  return html;
}

// fileLinesHtml renders file content with absolute line numbers.
// It backs the write tool's created-file view.
function fileLinesHtml(lines: string[]): string {
  return lines
    .map(
      (text, i) =>
        `<div class="flex min-w-0 items-start">` +
        `<span class="w-10 shrink-0 select-none pr-3 text-right text-slate-300 dark:text-slate-600">${i + 1}</span>` +
        `<code class="min-w-0 flex-1 whitespace-pre-wrap break-all">${escapeHtml(text) || " "}</code></div>`,
    )
    .join("");
}

// richWriteHtml builds the created-file view for a write tool call.
// It returns null when the arguments do not carry file content.
function richWriteHtml(tool: ToolInfo): string | null {
  const args = tryParseJson(tool.arguments);
  if (!args || typeof args.content !== "string" || typeof args.path !== "string") {
    return null;
  }
  const result = tool.status === "running" ? null : tryParseJson(tool.result);
  const chips: string[] = [];
  if (tool.status === "failed") {
    const message =
      result && typeof result.error === "string" ? result.error : "write failed";
    return (
      `<div data-fileview="write" class="overflow-hidden rounded-md border border-red-200/70 dark:border-red-800/50">` +
      `<div class="border-b border-red-200/70 bg-red-50 px-2 py-1.5 font-mono text-[10px] text-red-700 dark:border-red-800/50 dark:bg-red-950/40 dark:text-red-300">${escapeHtml(message)}</div></div>`
    );
  }
  if (result) {
    if (result.created === true) {
      chips.push("created");
    }
    if (result.overwritten === true) {
      chips.push("overwritten");
    }
    if (typeof result.lines === "number" && typeof result.bytes === "number") {
      chips.push(`${result.lines} lines · ${result.bytes} bytes`);
    }
    if (typeof result.scope === "string" && result.scope === "outside") {
      chips.push("outside workspace");
    }
  } else if (tool.status === "running") {
    chips.push("writing…");
  }
  const content = args.content as string;
  const body = content
    ? fileLinesHtml(splitLines(content))
    : '<div class="px-2 py-1 text-slate-400">(empty file)</div>';
  return (
    `<div data-fileview="write" class="overflow-hidden rounded-md border border-slate-200/70 dark:border-slate-700/70">` +
    `<div class="flex flex-wrap items-center gap-2 border-b border-slate-200/70 bg-white/80 px-2 py-1.5 dark:border-slate-700/70 dark:bg-slate-900/60">` +
    `<span class="min-w-0 flex-1 truncate font-mono text-[10px] font-semibold text-slate-600 dark:text-slate-300">${escapeHtml(args.path as string)}</span>` +
    chips
      .map(
        (chip) =>
          `<span class="shrink-0 rounded-full bg-slate-100 px-1.5 py-0.5 text-[9px] font-medium text-slate-500 dark:bg-slate-800 dark:text-slate-400">${escapeHtml(chip)}</span>`,
      )
      .join("") +
    `</div><div class="max-h-96 overflow-auto bg-white/80 p-2 font-mono text-[10px] leading-relaxed text-slate-600 dark:bg-slate-900/60 dark:text-slate-300">${body}</div></div>`
  );
}

// offsetDiffRows shifts hunk-relative line numbers to file line numbers.
// Null sides on add/del rows stay null.
function offsetDiffRows(rows: DiffRow[], by: number): DiffRow[] {
  if (by <= 0) {
    return rows;
  }
  return rows.map((row) => ({
    kind: row.kind,
    oldNo: row.oldNo === null ? null : row.oldNo + by,
    newNo: row.newNo === null ? null : row.newNo + by,
    text: row.text,
  }));
}

// matchSpans reads applied replacement spans from an edit result.
// It falls back to the single match start, then to hunk-relative numbering.
function matchSpans(result: Record<string, unknown> | null): MatchSpan[] {
  if (result && Array.isArray(result.matches)) {
    const spans: MatchSpan[] = [];
    for (const item of result.matches as unknown[]) {
      if (typeof item !== "object" || item === null) {
        continue;
      }
      const span = item as Record<string, unknown>;
      if (typeof span.start_line === "number") {
        spans.push({
          start: span.start_line,
          end: typeof span.end_line === "number" ? (span.end_line as number) : span.start_line,
        });
      }
    }
    if (spans.length > 0) {
      return spans;
    }
  }
  if (result && typeof result.match_start_line === "number") {
    const start = result.match_start_line as number;
    const end =
      typeof result.match_end_line === "number" ? (result.match_end_line as number) : start;
    return [{ start, end }];
  }
  return [{ start: 1, end: 1 }];
}

// richEditHtml builds the git-style diff view for an edit tool call.
// It diffs old_string against new_string and lists ambiguous matches on failure.
function richEditHtml(tool: ToolInfo): string | null {
  const args = tryParseJson(tool.arguments);
  if (!args || typeof args.old_string !== "string" || typeof args.path !== "string") {
    return null;
  }
  const oldText = args.old_string as string;
  const newText = typeof args.new_string === "string" ? (args.new_string as string) : "";
  const result = tool.status === "running" ? null : tryParseJson(tool.result);
  const chips: string[] = [];
  if (result && typeof result.replacements === "number") {
    chips.push(
      result.replacements === 1 ? "1 replacement" : `${result.replacements} replacements`,
    );
  }
  if (args.replace_all === true) {
    chips.push("replace all");
  }
  if (tool.status === "failed") {
    chips.push("failed");
  } else if (tool.status === "running") {
    chips.push("editing…");
  }

  let extra = "";
  if (result && Array.isArray(result.occurrences)) {
    const matchLines = splitLines(oldText);
    const items = (result.occurrences as unknown[])
      .map((item, index) => {
        if (typeof item !== "object" || item === null) {
          return "";
        }
        const occ = item as Record<string, unknown>;
        const before = occ.before as Record<string, unknown> | null;
        const after = occ.after as Record<string, unknown> | null;
        const plain = (text: string) =>
          `<div class="px-2 py-px">${escapeHtml(text) || " "}</div>`;
        let block =
          `<div class="px-2 pb-0.5 pt-2 font-semibold">${ordinal(index + 1)} match</div>`;
        if (before && typeof before.text === "string") {
          block += plain(before.text as string);
        }
        for (const line of matchLines) {
          block +=
            `<div class="mx-1 rounded bg-emerald-500/15 px-1 py-px text-emerald-800 dark:text-emerald-200">${escapeHtml(line) || " "}</div>`;
        }
        if (after && typeof after.text === "string") {
          block += plain(after.text as string);
        }
        return `<div class="pb-1">${block}</div>`;
      })
      .join("");
    if (items) {
      const count =
        typeof result.count === "number" ? result.count : result.occurrences.length;
      extra =
        `<div class="border-t border-amber-200/70 bg-amber-50/60 px-1 py-1 text-amber-700 dark:border-amber-800/50 dark:bg-amber-950/30 dark:text-amber-300">` +
        `<div class="px-2 py-0.5 font-semibold">${count} matches — retry with more context</div>${items}</div>`;
    }
  }
  const spans = matchSpans(result);
  const base = lineDiff(splitLines(oldText), splitLines(newText));
  const sections = spans
    .map((span) => {
      const label =
        spans.length > 1
          ? `<div class="px-2 py-1 text-[9px] font-semibold uppercase tracking-wide text-slate-400">lines ${span.start}–${span.end}</div>`
          : "";
      return (
        label +
        sideBySideDiffHtml(offsetDiffRows(base, span.start - 1))
      );
    })
    .join("");
  return (
    `<div data-filediff="edit" class="overflow-hidden rounded-md border border-slate-200/70 dark:border-slate-700/70">` +
    `<div class="flex flex-wrap items-center gap-2 border-b border-slate-200/70 bg-white/80 px-2 py-1.5 dark:border-slate-700/70 dark:bg-slate-900/60">` +
    `<span class="min-w-0 flex-1 truncate font-mono text-[10px] font-semibold text-slate-600 dark:text-slate-300">${escapeHtml(args.path as string)}</span>` +
    chips
      .map(
        (chip) =>
          `<span class="shrink-0 rounded-full bg-slate-100 px-1.5 py-0.5 text-[9px] font-medium text-slate-500 dark:bg-slate-800 dark:text-slate-400">${escapeHtml(chip)}</span>`,
      )
      .join("") +
    `</div><div class="max-h-96 overflow-auto bg-white/80 p-1 font-mono text-[10px] leading-relaxed dark:bg-slate-900/60">${sections}</div>${extra}</div>`
  );
}

// paintToolStatus is a function in this module.
// It supports the desktop app frontend.
export function paintToolStatus(statusEl: HTMLElement, status: string): void {
  if (status === "running") {
    statusEl.className =
      "ml-auto flex shrink-0 items-center gap-1.5 text-[10px] font-medium text-amber-600 dark:text-amber-400";
    statusEl.innerHTML =
      '<span class="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400 shadow-[0_0_6px_2px_rgba(251,191,36,0.6)]"></span>running';
    return;
  }

  if (status === "failed") {
    statusEl.className =
      "ml-auto flex shrink-0 items-center gap-1.5 text-[10px] font-medium text-red-600 dark:text-red-400";
    statusEl.innerHTML =
      '<span class="h-1.5 w-1.5 rounded-full bg-red-500 shadow-[0_0_5px_1px_rgba(239,68,68,0.5)]"></span>failed';
    return;
  }

  statusEl.className =
    "ml-auto flex shrink-0 items-center gap-1.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400";
  statusEl.innerHTML =
    '<span class="h-1.5 w-1.5 rounded-full bg-emerald-500 shadow-[0_0_5px_1px_rgba(16,185,129,0.45)]"></span>done';
}

// createToolActivity is a function in this module.
// It supports the desktop app frontend.
export function createToolActivity(tool: ToolInfo): ToolView {
  let current = tool;

  const box = document.createElement("div");
  box.className =
    "overflow-hidden rounded-lg border border-slate-200/70 bg-slate-50/70 text-[11px] dark:border-slate-700/70 dark:bg-slate-800/40";
  box.setAttribute("data-tool", current.name);

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className =
    "flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-slate-500 transition-colors hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-700/50";
  toggle.setAttribute("aria-expanded", "false");

  const chevron = document.createElement("span");
  chevron.className = "shrink-0 transition-transform duration-150";
  chevron.innerHTML = icon("chevron-right", "h-3 w-3");

  const name = document.createElement("span");
  name.className = "shrink-0 font-semibold text-slate-600 dark:text-slate-300";
  name.textContent = current.name;

  const separator = document.createElement("span");
  separator.className = "shrink-0 text-slate-300 dark:text-slate-600";
  separator.textContent = "›";

  const summary = document.createElement("span");
  summary.className = "min-w-0 flex-1 truncate font-mono text-slate-400 dark:text-slate-500";
  summary.textContent = toolSummary(current.name, current.arguments);

  const statusEl = document.createElement("span");
  paintToolStatus(statusEl, current.status);

  toggle.append(chevron, name, separator, summary, statusEl);

  const details = document.createElement("div");
  details.className = "hidden border-t border-slate-200/70 px-2.5 py-2 dark:border-slate-700/70";

  let filled = false;
  let richWrap: HTMLElement | null = null;
  // renderRich rebuilds the file view or diff from the latest tool state.
  // It runs on first expand and on every update while expanded.
  const renderRich = () => {
    if (!richWrap) {
      return;
    }
    const html =
      current.name === "write"
        ? richWriteHtml(current)
        : current.name === "edit"
          ? richEditHtml(current)
          : null;
    richWrap.innerHTML = html ?? "";
    richWrap.classList.toggle("hidden", html === null);
  };
  let inputPre: HTMLPreElement | null = null;
  let outputPre: HTMLPreElement | null = null;

  const outputText = () =>
    current.status === "running" ? "running…" : prettyJson(current.result) || "(empty)";

  const fill = () => {
    if (filled) {
      return;
    }
    filled = true;

    const section = (label: string) => {
      const wrapper = document.createElement("div");

      const heading = document.createElement("div");
      heading.className =
        "mb-1 text-[10px] font-semibold uppercase tracking-wide text-slate-400";
      heading.textContent = label;

      const pre = document.createElement("pre");
      pre.className =
        "overflow-auto rounded bg-white/80 p-2 font-mono text-[10px] leading-relaxed text-slate-600 dark:bg-slate-900/60 dark:text-slate-300";

      wrapper.append(heading, pre);
      return { wrapper, pre };
    };

    const input = section("Input");
    input.wrapper.classList.add("mb-2");
    input.pre.classList.add("max-h-56");
    input.pre.textContent = prettyJson(current.arguments) || "(none)";
    inputPre = input.pre;

    const output = section("Output");
    output.pre.classList.add("max-h-72");
    output.pre.textContent = outputText();
    outputPre = output.pre;

    details.append(input.wrapper, output.wrapper);
    if (current.name === "write" || current.name === "edit") {
      input.wrapper.remove();
      output.wrapper.remove();
    }
    richWrap = document.createElement("div");
    richWrap.className = "mb-2";
    details.prepend(richWrap);
    renderRich();
  };

  toggle.addEventListener("click", () => {
    fill();
    const open = !details.classList.toggle("hidden");
    toggle.setAttribute("aria-expanded", String(open));
    chevron.classList.toggle("rotate-90", open);
  });

  box.append(toggle, details);

  const update = (next: ToolInfo) => {
    current = next;

    name.textContent = current.name;
    const short = toolSummary(current.name, current.arguments);
    if (short) {
      summary.textContent = short;
    }
    paintToolStatus(statusEl, current.status);

    if (inputPre) {
      inputPre.textContent = prettyJson(current.arguments) || "(none)";
    }
    if (outputPre) {
      outputPre.textContent = outputText();
    }
    renderRich();
  };

  return { el: box, update };
}
