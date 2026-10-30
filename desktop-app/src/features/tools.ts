// tools.ts renders tool-call activity cards and file diffs.
// It is pure DOM building: no backend calls and no shared state.

import { escapeHtml, ordinal, splitLines, tryParseJson } from "../core/format";
import type { DiffRow, MatchSpan, ToolInfo, ToolView } from "../core/types";
import { icon, type IconName } from "../icons";
import { relPath, toolBodyHtml } from "./toolBodies";

function toolSummary(name: string, raw: string): string {
  const short = (text: string) => (text.length > 120 ? `${text.slice(0, 120)}…` : text);

  const args = tryParseJson(raw);
  if (args) {
    if (name === "ask") {
      const questions = args.questions;
      if (Array.isArray(questions) && questions.length > 0) {
        const first = questions[0] as Record<string, unknown> | null;
        const text = typeof first?.question === "string" ? first.question : "";
        const extra = questions.length > 1 ? ` (+${questions.length - 1} more)` : "";
        return short(text + extra) || `ask${extra}`;
      }
      return "ask";
    }
    if (name === "shell") {
      if (typeof args.cmd === "string" && args.cmd.trim()) {
        return short(args.cmd.trim());
      }
      const action = typeof args.action === "string" && args.action ? args.action : "run";
      const job = typeof args.job_id === "string" && args.job_id ? ` ${args.job_id}` : "";
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
  }
  return short(raw);
}

// lineDiff diffs old lines against new lines with longest-common-subsequence.
// Big inputs skip the O(n*m) table and fall back to whole-block replace:
// a collapsed-by-default card never needs a perfect diff at 60Hz.
function lineDiff(oldLines: string[], newLines: string[]): DiffRow[] {
  const n = oldLines.length;
  const m = newLines.length;
  if (n > 400 || m > 400 || n * m > 250000) {
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
function richWriteHtml(tool: ToolInfo): string | null {
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
    `</div><div class="max-h-96 overflow-auto bg-white p-2 font-mono text-[10px] leading-relaxed text-slate-600 dark:bg-slate-900/60 dark:text-slate-300">${body}</div></div>`
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
function richEditHtml(tool: ToolInfo): string | null {
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
    `</div><div class="max-h-96 overflow-auto bg-white p-1 font-mono text-[10px] leading-relaxed dark:bg-slate-900/60">${sections}</div>${extra}</div>`
  );
}

// ToolLabel is the one-line reading of a call: a verb, a muted detail,
// and an optional inline code chip (the command for shell).
export type ToolLabel = { verb: string; detail: string; chip: string; server?: string };

// mcpName splits an exposed MCP tool name (mcp__server__tool).
function mcpName(name: string): { server: string; tool: string } | null {
  if (!name.startsWith("mcp__")) return null;
  const rest = name.slice(5);
  const at = rest.indexOf("__");
  return at > 0 && at < rest.length - 2 ? { server: rest.slice(0, at), tool: rest.slice(at + 2) } : null;
}

type ToolKind = {
  icon: IconName;
  past: string;
  now: string;
  noun: (n: number) => string;
};

const plural = (one: string, many: string) => (n: number) => `${n} ${n === 1 ? one : many}`;

const KINDS: Record<string, ToolKind> = {
  shell: { icon: "tl-term", past: "Ran", now: "Running", noun: plural("command", "commands") },
  read: { icon: "tl-file", past: "Read", now: "Reading", noun: plural("file", "files") },
  write: { icon: "tl-file-plus", past: "Wrote", now: "Writing", noun: plural("file", "files") },
  edit: { icon: "tl-pencil", past: "Edited", now: "Editing", noun: plural("file", "files") },
  ask: { icon: "tl-ask", past: "Asked", now: "Asking", noun: plural("question", "questions") },
  todo: {
    icon: "tl-list",
    past: "Updated",
    now: "Updating",
    noun: (n) => (n === 1 ? "the plan" : `the plan ${n} times`),
  },
};

const OTHER: ToolKind = { icon: "tl-tools", past: "Called", now: "Calling", noun: plural("tool", "tools") };
const MCP: ToolKind = { icon: "sl-plug", past: "Called", now: "Calling", noun: plural("MCP tool", "MCP tools") };

function kindOf(name: string): ToolKind {
  return KINDS[name] ?? (mcpName(name) ? MCP : OTHER);
}

function parseArgs(raw: string): Record<string, unknown> {
  const parsed = tryParseJson(raw);
  return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
}

// toolLabel reads one call the way a person would narrate it.
export function toolLabel(tool: ToolInfo): ToolLabel {
  const kind = kindOf(tool.name);
  const verb = tool.status === "running" ? kind.now : kind.past;
  const args = parseArgs(tool.arguments);
  const summary = toolSummary(tool.name, tool.arguments);
  switch (tool.name) {
    case "shell":
      return { verb, detail: "", chip: summary };
    case "read":
    case "edit":
      return { verb, detail: relPath(summary), chip: "" };
    case "write": {
      const result = parseArgs(tool.result);
      const created = tool.status === "ok" && result.created === true;
      return { verb: created ? "Created" : verb, detail: relPath(summary), chip: "" };
    }
    case "ask":
      return { verb, detail: summary, chip: "" };
    case "todo": {
      const todos = Array.isArray(args.todos) ? args.todos.length : 0;
      return { verb, detail: todos ? `the plan · ${todos} ${todos === 1 ? "item" : "items"}` : "the plan", chip: "" };
    }
    default: {
      const mcp = mcpName(tool.name);
      if (mcp) {
        return {
          verb,
          detail: mcp.tool,
          chip: summary && summary !== tool.arguments ? summary : "",
          server: mcp.server,
        };
      }
      return { verb, detail: tool.name, chip: summary && summary !== tool.name ? summary : "" };
    }
  }
}

// runLabel summarizes a run of calls: "Ran 2 commands, read 1 file".
// A kind with a call still in flight reads in the present tense.
export function runLabel(tools: ToolInfo[]): string {
  const order: string[] = [];
  const groups = new Map<string, { kind: ToolKind; n: number; running: boolean }>();
  for (const tool of tools) {
    const kind = kindOf(tool.name);
    const key = KINDS[tool.name] ? tool.name : mcpName(tool.name) ? "mcp" : "*";
    let g = groups.get(key);
    if (!g) {
      g = { kind, n: 0, running: false };
      groups.set(key, g);
      order.push(key);
    }
    g.n += 1;
    g.running ||= tool.status === "running";
  }
  return order
    .map((key, i) => {
      const g = groups.get(key)!;
      const verb = g.running ? g.kind.now : g.kind.past;
      return `${i === 0 ? verb : verb.toLowerCase()} ${g.kind.noun(g.n)}`;
    })
    .join(", ");
}

// toolIcon is the glyph for a kind of call.
function toolIcon(name: string): IconName {
  return kindOf(name).icon;
}

// paintToolHead redraws the one-line head of a call row in place.
function paintToolHead(row: HTMLElement, head: HTMLElement, tool: ToolInfo): void {
  const label = toolLabel(tool);
  row.dataset.status = tool.status;
  const ico = head.querySelector<HTMLElement>(".tool-ico")!;
  const kind = tool.status === "running" ? "spin" : toolIcon(tool.name);
  if (ico.dataset.glyph !== kind) {
    ico.dataset.glyph = kind;
    ico.innerHTML = tool.status === "running" ? '<span class="tool-spin"></span>' : icon(kind, "h-4 w-4");
  }
  head.querySelector(".tool-verb")!.textContent = label.verb;
  const server = head.querySelector<HTMLElement>(".tool-server")!;
  server.textContent = label.server ?? "";
  server.hidden = !label.server;
  server.title = label.server ? `MCP server ${label.server}` : "";
  const detail = head.querySelector<HTMLElement>(".tool-detail")!;
  detail.textContent = label.detail;
  detail.hidden = !label.detail;
  detail.title = label.detail ? toolSummary(tool.name, tool.arguments) : "";
  const chip = head.querySelector<HTMLElement>(".tool-chip")!;
  chip.textContent = label.chip;
  chip.hidden = !label.chip;
  chip.title = label.chip;
  const fail = head.querySelector<HTMLElement>(".tool-fail")!;
  fail.textContent = tool.status === "failed" ? "failed" : "";
  fail.hidden = tool.status !== "failed";
}

// createToolActivity builds one call row: icon, verb, detail and chip on
// a single line; clicking it reveals the input/output or the file view.
export function createToolActivity(tool: ToolInfo): ToolView {
  let current = tool;

  const box = document.createElement("div");
  box.className = "tool-row";
  box.setAttribute("data-tool", current.name);

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "tool-row-head";
  toggle.setAttribute("aria-expanded", "false");
  toggle.innerHTML =
    '<span class="tool-ico"></span><span class="tool-verb"></span><span class="tool-server"></span><span class="tool-detail"></span>' +
    '<code class="tool-chip"></code><span class="tool-fail"></span>';
  paintToolHead(box, toggle, current);

  const details = document.createElement("div");
  details.className = "tool-body hidden";

  // render rebuilds the body from the latest state, only while open:
  // a collapsed row has nothing to paint.
  const render = () => {
    if (details.classList.contains("hidden")) {
      return;
    }
    const rich =
      current.name === "write" ? richWriteHtml(current) : current.name === "edit" ? richEditHtml(current) : null;
    details.innerHTML = toolBodyHtml(current, rich);
  };

  toggle.addEventListener("click", () => {
    const open = !details.classList.toggle("hidden");
    toggle.setAttribute("aria-expanded", String(open));
    render();
  });

  box.append(toggle, details);

  const update = (next: ToolInfo) => {
    current = next;
    paintToolHead(box, toggle, current);
    render();
  };

  return { el: box, update };
}
