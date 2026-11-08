// File tool cards and their diagnostics.

import { escapeHtml, ordinal, splitLines, tryParseJson } from "../../core/format";
import type { DiffRow, MatchSpan, ToolInfo } from "../../core/types";
import type { LineDiffer } from "./lineDiff";

// errorsChip names the language server errors a write or edit got.
function errorsChip(result: Record<string, unknown> | null): string {
  const n = result && typeof result.errors === "number" ? result.errors : 0;
  if (!n) return "";
  const from = typeof result?.diagnostics_from === "string" ? ` · ${result.diagnostics_from}` : "";
  return `${n} ${n === 1 ? "error" : "errors"}${from}`;
}

// diagnosticsHtml lists the language server errors under the file head.
function diagnosticsHtml(result: Record<string, unknown> | null): string {
  const list = result && Array.isArray(result.diagnostics) ? (result.diagnostics as Record<string, unknown>[]) : [];
  if (!list.length) return "";
  const rows = list
    .map((d) => {
      const at = `${Number(d.line) || 0}:${Number(d.column) || 0}`;
      return `<li class="diag-row"><span class="diag-at">${escapeHtml(at)}</span><span class="diag-msg">${escapeHtml(String(d.message ?? ""))}</span></li>`;
    })
    .join("");
  return `<ul class="diag-list" data-diagnostics>${rows}</ul>`;
}

export function changedLines(tool: ToolInfo, lineDiff: LineDiffer): { added: number; removed: number } | null {
  if (tool.status !== "ok" || (tool.name !== "write" && tool.name !== "edit")) return null;
  const args = tryParseJson(tool.arguments);
  const result = tryParseJson(tool.result);
  if (!args || !result) return null;
  if (tool.name === "write") {
    if (typeof result.lines !== "number") return null;
    return { added: result.lines, removed: result.overwritten === true ? Number(result.old_lines ?? 0) : 0 };
  }
  if (typeof args.old_string !== "string" || typeof args.new_string !== "string") return null;
  const lines = (text: string) => (text ? splitLines(text) : []);
  const rows = lineDiff(lines(args.old_string), lines(args.new_string));
  const count = typeof result.replacements === "number" ? result.replacements : 1;
  return {
    added: rows.filter((row) => row.kind === "add").length * count,
    removed: rows.filter((row) => row.kind === "del").length * count,
  };
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
      dels.push(rows[i]);
      i++;
    }
    while (i < rows.length && rows[i]?.kind === "add") {
      adds.push(rows[i]);
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
export function richWriteHtml(tool: ToolInfo): string | null {
  const args = tryParseJson(tool.arguments);
  if (!args || typeof args.content !== "string" || typeof args.path !== "string") {
    return null;
  }
  const result = tool.status === "running" ? null : tryParseJson(tool.result);
  const chips: string[] = [];
  if (tool.status === "failed") {
    const message = result && typeof result.error === "string" ? result.error : "write failed";
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
    if (typeof result.formatted === "string" && result.formatted) {
      chips.push(`formatted · ${result.formatted}`);
    }
    const diagChip = errorsChip(result);
    if (diagChip) chips.push(diagChip);
  } else if (tool.status === "running") {
    chips.push("writing…");
  }
  const content = args.content;
  const body = content
    ? fileLinesHtml(splitLines(content))
    : '<div class="px-2 py-1 text-slate-400">(empty file)</div>';
  return (
    `<div data-fileview="write" class="overflow-hidden rounded-md border border-slate-200/70 dark:border-slate-700/70">` +
    `<div class="flex flex-wrap items-center gap-2 border-b border-slate-200/70 bg-white px-2 py-1.5 dark:border-slate-700/70 dark:bg-slate-900/60">` +
    `<span class="min-w-0 flex-1 truncate font-mono text-[10px] font-semibold text-slate-600 dark:text-slate-300">${escapeHtml(args.path)}</span>` +
    chips
      .map(
        (chip) =>
          `<span class="shrink-0 rounded-full bg-slate-100 px-1.5 py-0.5 text-[9px] font-medium text-slate-500 dark:bg-slate-800 dark:text-slate-400">${escapeHtml(chip)}</span>`,
      )
      .join("") +
    `</div>${diagnosticsHtml(result)}<div class="max-h-96 overflow-auto bg-white p-2 font-mono text-[10px] leading-relaxed text-slate-600 dark:bg-slate-900/60 dark:text-slate-300">${body}</div></div>`
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
          end: typeof span.end_line === "number" ? span.end_line : span.start_line,
        });
      }
    }
    if (spans.length > 0) {
      return spans;
    }
  }
  if (result && typeof result.match_start_line === "number") {
    const start = result.match_start_line;
    const end = typeof result.match_end_line === "number" ? result.match_end_line : start;
    return [{ start, end }];
  }
  return [{ start: 1, end: 1 }];
}

// richEditHtml builds the git-style diff view for an edit tool call.
// It diffs old_string against new_string and lists ambiguous matches on failure.
export function richEditHtml(tool: ToolInfo, lineDiff: LineDiffer): string | null {
  const args = tryParseJson(tool.arguments);
  if (!args || typeof args.old_string !== "string" || typeof args.path !== "string") {
    return null;
  }
  const oldText = args.old_string;
  const newText = typeof args.new_string === "string" ? args.new_string : "";
  const result = tool.status === "running" ? null : tryParseJson(tool.result);
  const chips: string[] = [];
  if (result && typeof result.replacements === "number") {
    chips.push(result.replacements === 1 ? "1 replacement" : `${result.replacements} replacements`);
  }
  if (args.replace_all === true) {
    chips.push("replace all");
  }
  // A non-exact match (CRLF, whitespace or indentation drift) still
  // applied; say how, so the diff isn't taken as byte-exact.
  if (result && typeof result.match_strategy === "string" && result.match_strategy !== "exact") {
    chips.push(`matched ignoring ${String(result.match_strategy).replace(/_/g, " ")}`);
  }
  if (result && typeof result.formatted === "string" && result.formatted) {
    chips.push(`formatted · ${result.formatted}`);
  }
  const diagChip = errorsChip(result);
  if (diagChip) chips.push(diagChip);
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
        const plain = (text: string) => `<div class="px-2 py-px">${escapeHtml(text) || " "}</div>`;
        let block = `<div class="px-2 pb-0.5 pt-2 font-semibold">${ordinal(index + 1)} match</div>`;
        if (before && typeof before.text === "string") {
          block += plain(before.text);
        }
        for (const line of matchLines) {
          block += `<div class="mx-1 rounded bg-emerald-500/15 px-1 py-px text-emerald-800 dark:text-emerald-200">${escapeHtml(line) || " "}</div>`;
        }
        if (after && typeof after.text === "string") {
          block += plain(after.text);
        }
        return `<div class="pb-1">${block}</div>`;
      })
      .join("");
    if (items) {
      const count = typeof result.count === "number" ? result.count : result.occurrences.length;
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
      return label + sideBySideDiffHtml(offsetDiffRows(base, span.start - 1));
    })
    .join("");
  return (
    `<div data-filediff="edit" class="overflow-hidden rounded-md border border-slate-200/70 dark:border-slate-700/70">` +
    `<div class="flex flex-wrap items-center gap-2 border-b border-slate-200/70 bg-white px-2 py-1.5 dark:border-slate-700/70 dark:bg-slate-900/60">` +
    `<span class="min-w-0 flex-1 truncate font-mono text-[10px] font-semibold text-slate-600 dark:text-slate-300">${escapeHtml(args.path)}</span>` +
    chips
      .map(
        (chip) =>
          `<span class="shrink-0 rounded-full bg-slate-100 px-1.5 py-0.5 text-[9px] font-medium text-slate-500 dark:bg-slate-800 dark:text-slate-400">${escapeHtml(chip)}</span>`,
      )
      .join("") +
    `</div>${diagnosticsHtml(result)}<div class="max-h-96 overflow-auto bg-white p-1 font-mono text-[10px] leading-relaxed dark:bg-slate-900/60">${sections}</div>${extra}</div>`
  );
}
