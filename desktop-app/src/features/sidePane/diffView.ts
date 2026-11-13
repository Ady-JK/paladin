import { classHighlighter, highlightTree } from "@lezer/highlight";
import { escapeHtml } from "../../core/format";
import { language } from "../ide/editor";

type DiffLine = {
  kind: "context" | "add" | "remove" | "note";
  text: string;
  oldNumber?: number;
  newNumber?: number;
  oldHtml?: string;
  newHtml?: string;
};
type Hunk = { header: string; lines: DiffLine[] };
type Mark = { start: number; end: number } | null;

function parsePatch(patch: string): { hunks: Hunk[]; truncated: boolean; fallback: string } {
  const lines = patch.split("\n");
  const hunks: Hunk[] = [];
  let current: Hunk | undefined;
  let oldNumber = 0;
  let newNumber = 0;
  for (const line of lines.slice(0, 5000)) {
    const header = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (header) {
      current = { header: line, lines: [] };
      hunks.push(current);
      oldNumber = Number(header[1]);
      newNumber = Number(header[2]);
    } else if (current && line.startsWith(" ")) {
      current.lines.push({ kind: "context", text: line.slice(1), oldNumber: oldNumber++, newNumber: newNumber++ });
    } else if (current && line.startsWith("-")) {
      current.lines.push({ kind: "remove", text: line.slice(1), oldNumber: oldNumber++ });
    } else if (current && line.startsWith("+")) {
      current.lines.push({ kind: "add", text: line.slice(1), newNumber: newNumber++ });
    } else if (current && line.startsWith("\\")) {
      current.lines.push({ kind: "note", text: line });
    }
  }
  return { hunks, truncated: lines.length > 5000, fallback: lines.slice(0, 5000).join("\n") };
}

// Parse each side as one fragment so multiline syntax is highlighted consistently.
function highlightLines(lines: DiffLine[], path: string, side: "old" | "new"): void {
  const selected = lines.filter((line) => line.kind === "context" || line.kind === (side === "old" ? "remove" : "add"));
  const source = selected.map((line) => line.text).join("\n");
  const support = source.length <= 200_000 ? language(path) : null;
  if (!support || !selected.length) return;
  const offsets: number[] = [];
  let offset = 0;
  for (const line of selected) {
    offsets.push(offset);
    offset += line.text.length + 1;
  }
  const html = selected.map(() => "");
  const positions = selected.map(() => 0);
  let index = 0;
  highlightTree(support.language.parser.parse(source), classHighlighter, (from, to, classes) => {
    while (index < selected.length && offsets[index] + selected[index].text.length < from) index++;
    let i = index;
    while (i < selected.length && offsets[i] < to) {
      const line = selected[i].text;
      const start = Math.max(0, from - offsets[i]);
      const end = Math.min(line.length, to - offsets[i]);
      if (end > start) {
        html[i] += escapeHtml(line.slice(positions[i], start));
        html[i] += `<span class="${classes}">${escapeHtml(line.slice(start, end))}</span>`;
        positions[i] = end;
      }
      i++;
    }
  });
  selected.forEach((line, i) => {
    const content = html[i] + escapeHtml(line.text.slice(positions[i]));
    if (side === "old") line.oldHtml = content;
    else line.newHtml = content;
  });
}

function marked(line: DiffLine | undefined, mark: Mark): boolean {
  return !!line?.newNumber && !!mark && line.newNumber >= mark.start && line.newNumber <= mark.end;
}

function cell(line: DiffLine | undefined, side: "old" | "new", mark: Mark): string {
  if (!line) return '<div class="diff-cell diff-gap" aria-hidden="true"></div>';
  const number = side === "old" ? line.oldNumber : line.newNumber;
  const code = (side === "old" ? line.oldHtml : line.newHtml) ?? escapeHtml(line.text);
  const sign = line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " ";
  return `<div class="diff-cell diff-${line.kind}${side === "new" && marked(line, mark) ? " change-mark" : ""}" data-line="${number ?? ""}"><span class="diff-number">${number ?? ""}</span><span class="diff-sign">${sign}</span><code>${code || " "}</code></div>`;
}

function splitRows(lines: DiffLine[], mark: Mark): string {
  let result = "";
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (line.kind === "note") {
      result += `<div class="diff-note">${escapeHtml(line.text)}</div>`;
      index++;
      continue;
    }
    if (line.kind === "context") {
      result += `<div class="diff-pair">${cell(line, "old", mark)}${cell(line, "new", mark)}</div>`;
      index++;
      continue;
    }
    const removed: DiffLine[] = [];
    const added: DiffLine[] = [];
    while (index < lines.length && (lines[index].kind === "remove" || lines[index].kind === "add")) {
      const item = lines[index++];
      (item.kind === "remove" ? removed : added).push(item);
    }
    for (let i = 0; i < Math.max(removed.length, added.length); i++) {
      result += `<div class="diff-pair">${cell(removed[i], "old", mark)}${cell(added[i], "new", mark)}</div>`;
    }
  }
  return result;
}

function unifiedRows(lines: DiffLine[], mark: Mark): string {
  return lines
    .map((line) => {
      if (line.kind === "note") return `<div class="diff-note">${escapeHtml(line.text)}</div>`;
      const code = (line.kind === "remove" ? line.oldHtml : line.newHtml) ?? escapeHtml(line.text);
      const sign = line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " ";
      return `<div class="diff-unified-row diff-${line.kind}${marked(line, mark) ? " change-mark" : ""}"><span class="diff-number">${line.oldNumber ?? ""}</span><span class="diff-number">${line.newNumber ?? ""}</span><span class="diff-sign">${sign}</span><code>${code || " "}</code></div>`;
    })
    .join("");
}

export function renderDiff(patch: string, path: string, mark: Mark, truncated: boolean): string {
  const parsed = parsePatch(patch);
  if (!parsed.hunks.length)
    return `<pre class="diff-fallback">${escapeHtml(parsed.fallback)}</pre>${truncated || parsed.truncated ? '<div class="diff-truncated">Diff preview truncated.</div>' : ""}`;
  const all = parsed.hunks.flatMap((hunk) => hunk.lines);
  highlightLines(all, path, "old");
  highlightLines(all, path, "new");
  const header = (hunk: Hunk) => `<div class="diff-hunk">${escapeHtml(hunk.header)}</div>`;
  const split = parsed.hunks.map((hunk) => header(hunk) + splitRows(hunk.lines, mark)).join("");
  const unified = parsed.hunks.map((hunk) => header(hunk) + unifiedRows(hunk.lines, mark)).join("");
  return `<div class="diff-view"><div class="diff-split"><div class="diff-column-head"><span>Original</span><span>Modified</span></div>${split}</div><div class="diff-unified">${unified}</div>${truncated || parsed.truncated ? '<div class="diff-truncated">Diff preview truncated.</div>' : ""}</div>`;
}
