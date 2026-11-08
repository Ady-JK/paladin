import type { DiffRow } from "../../core/types";

export type LineDiffer = (oldLines: string[], newLines: string[]) => DiffRow[];

// Each tool row retains only its latest diff. Repaints and expanding the
// same edit can reuse the LCS without keeping a global cache of file text.
export function createLineDiffer(): LineDiffer {
  let previous: { oldLines: string[]; newLines: string[]; rows: DiffRow[] } | null = null;
  const equal = (a: string[], b: string[]) => a.length === b.length && a.every((line, index) => line === b[index]);
  return (oldLines, newLines) => {
    if (previous && equal(previous.oldLines, oldLines) && equal(previous.newLines, newLines)) return previous.rows;
    const rows = lineDiff(oldLines, newLines);
    previous = { oldLines, newLines, rows };
    return rows;
  };
}

// lineDiff diffs old lines against new lines with longest-common-subsequence.
// Big inputs skip the O(n*m) table and fall back to whole-block replace:
// a collapsed-by-default card never needs a perfect diff at 60Hz.
function lineDiff(oldLines: string[], newLines: string[]): DiffRow[] {
  const n = oldLines.length;
  const m = newLines.length;
  if (n > 400 || m > 400) {
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
