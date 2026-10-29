// format.ts holds pure string, date, and JSON helpers.
// It has no DOM or backend dependencies so any module can use it.

import type { ProviderInfo } from "./types";

// escapeHtml is a function in this module.
// It supports the desktop app frontend.
export function escapeHtml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// escapeAttr is a function in this module.
// It supports the desktop app frontend.
export function escapeAttr(text: string): string {
  return escapeHtml(text).replace(/"/g, "&quot;");
}

// renderMarkdown renders agent output as Markdown HTML: fenced code,
// headings, bold/italic/strikethrough, inline code, nested lists with
// task items, blockquotes, tables, rules, and links. The input is
// HTML-escaped first, so the tags below are the only markup in the
// output — agent text can never inject elements or attributes.
export function renderMarkdown(text: string): string {
  const parts = escapeHtml(text).split(/```/);
  let out = "";
  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 1) {
      out += renderFence(parts[i]);
      continue;
    }
    out += renderBlocks(parts[i]);
  }
  return out;
}

// renderFence renders one ``` block: an optional language tag on the
// first line becomes a label chip, the rest is literal code. An
// unclosed fence (still streaming) renders the same way.
function renderFence(block: string): string {
  const nl = block.indexOf("\n");
  const lang = (nl === -1 ? block : block.slice(0, nl))
    .trim()
    .replace(/[^a-z0-9+#-]/gi, "");
  const code = (nl === -1 ? "" : block.slice(nl + 1)).replace(/^\n/, "");
  const label = lang ? `<span class="md-lang">${lang}</span>` : "";
  return `<pre class="md-pre"><code>${code}</code>${label}</pre>`;
}

// renderBlocks parses block structure: headings, rules, quotes,
// tables, lists, and paragraphs. Operates on already-escaped text.
function renderBlocks(src: string): string {
  const lines = src.split("\n");
  let html = "";
  let para: string[] = [];
  const flush = (): void => {
    if (para.length > 0) {
      html += `<p class="md-p">${para.map(mdInline).join("<br>")}</p>`;
      para = [];
    }
  };

  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (/^\s*$/.test(line)) {
      flush();
      i++;
      continue;
    }
    const head = line.match(/^(#{1,6})\s+(.*?)\s*$/);
    if (head) {
      flush();
      const level = head[1].length;
      html += `<h${level} class="md-h${level}">${mdInline(head[2])}</h${level}>`;
      i++;
      continue;
    }
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      flush();
      html += `<hr class="md-hr">`;
      i++;
      continue;
    }
    if (/^\s*&gt;/.test(line)) {
      flush();
      const inner: string[] = [];
      while (i < lines.length && /^\s*&gt;/.test(lines[i])) {
        inner.push(lines[i].replace(/^\s*&gt; ?/, ""));
        i++;
      }
      html += `<blockquote class="md-quote">${renderBlocks(inner.join("\n"))}</blockquote>`;
      continue;
    }
    if (isTableRow(line) && i + 1 < lines.length && isTableDelim(lines[i + 1])) {
      flush();
      const headCells = splitRow(line);
      const aligns = splitRow(lines[i + 1]).map(alignOf);
      i += 2;
      let body = "";
      while (i < lines.length && isTableRow(lines[i])) {
        const cells = splitRow(lines[i]);
        body += `<tr>${headCells.map((_, c) => `<td class="md-td"${alignAttr(aligns[c])}>${mdInline(cells[c] ?? "")}</td>`).join("")}</tr>`;
        i++;
      }
      html += `<table class="md-table"><thead><tr>${headCells.map((c, k) => `<th class="md-th"${alignAttr(aligns[k])}>${mdInline(c)}</th>`).join("")}</tr></thead><tbody>${body}</tbody></table>`;
      continue;
    }
    if (/^\s*(?:[-*+]|\d+[.)])\s+/.test(line)) {
      flush();
      const taken: string[] = [];
      while (i < lines.length) {
        if (/^\s*$/.test(lines[i])) {
          const next = lines.slice(i + 1).find((l) => !/^\s*$/.test(l));
          if (next && /^\s*(?:[-*+]|\d+[.)])\s+/.test(next)) {
            i++;
            continue;
          }
          break;
        }
        if (!/^\s*(?:[-*+]|\d+[.)])\s+/.test(lines[i])) {
          break;
        }
        taken.push(lines[i]);
        i++;
      }
      html += renderList(taken);
      continue;
    }
    para.push(line);
    i++;
  }
  flush();
  return html;
}

// renderList emits nested <ul>/<ol> from consecutive item lines. Deeper
// indents open inside the parent <li>; a kind switch at one indent
// closes and reopens the list.
function renderList(taken: string[]): string {
  let html = "";
  const open: { indent: number; tag: "ul" | "ol" }[] = [];
  const liOpen: boolean[] = [];
  const closeLevel = (): void => {
    if (liOpen[liOpen.length - 1]) {
      html += "</li>";
      liOpen[liOpen.length - 1] = false;
    }
    const level = open.pop();
    liOpen.pop();
    html += `</${level!.tag}>`;
  };

  for (const raw of taken) {
    const m = raw.match(/^(\s*)([-*+]|\d+[.)])\s+(.*)$/);
    if (!m) {
      continue;
    }
    const indent = m[1].replace(/\t/g, "  ").length;
    const tag = /^\d/.test(m[2]) ? "ol" : "ul";
    let content = m[3];
    const task = content.match(/^\[([ xX])\]\s+(.*)$/);
    let box = "";
    if (task && tag === "ul") {
      const done = task[1].toLowerCase() === "x";
      box = `<span class="md-task${done ? " done" : ""}"></span>`;
      content = task[2];
    }
    while (open.length > 0 && indent < open[open.length - 1].indent) {
      closeLevel();
    }
    if (open.length === 0 || indent > open[open.length - 1].indent) {
      open.push({ indent, tag });
      liOpen.push(false);
      html += `<${tag} class="${tag === "ol" ? "md-ol" : "md-ul"}">`;
    } else if (open[open.length - 1].tag !== tag) {
      if (liOpen[liOpen.length - 1]) {
        html += "</li>";
        liOpen[liOpen.length - 1] = false;
      }
      open[open.length - 1].tag = tag;
      html += `</${tag === "ol" ? "ul" : "ol"}><${tag} class="${tag === "ol" ? "md-ol" : "md-ul"}">`;
    } else if (liOpen[liOpen.length - 1]) {
      html += "</li>";
      liOpen[liOpen.length - 1] = false;
    }
    html += `<li class="md-li">${box}${mdInline(content)}`;
    liOpen[liOpen.length - 1] = true;
  }
  while (open.length > 0) {
    closeLevel();
  }
  return html;
}

// isTableRow is a function in this module. Any line holding a pipe can
// open or extend a table; the delimiter line below confirms it.
function isTableRow(line: string): boolean {
  return line.includes("|");
}

// isTableDelim is a function in this module. It matches the GFM
// separator row (| --- | :---: | ---: |) under a table header.
function isTableDelim(line: string): boolean {
  const cells = splitRow(line);
  if (cells.length === 0) {
    return false;
  }
  return cells.every((c) => /^:?-{1,}:?$/.test(c.trim()));
}

// splitRow is a function in this module. It cuts a table row on pipes,
// dropping the empty edges from leading/trailing pipes.
function splitRow(line: string): string[] {
  let cells = line.split("|").map((c) => c.trim());
  if (cells.length > 0 && cells[0] === "") {
    cells = cells.slice(1);
  }
  if (cells.length > 0 && cells[cells.length - 1] === "") {
    cells = cells.slice(0, -1);
  }
  return cells;
}

// alignOf is a function in this module. It reads a delimiter cell.
function alignOf(cell: string): "left" | "center" | "right" | "" {
  const c = (cell || "").trim();
  if (c.startsWith(":") && c.endsWith(":") && c.length > 2) {
    return "center";
  }
  if (c.endsWith(":")) {
    return "right";
  }
  if (c.startsWith(":")) {
    return "left";
  }
  return "";
}

// alignAttr is a function in this module. It renders a delimiter cell
// as a style attribute, or nothing for default alignment.
function alignAttr(align: string | undefined): string {
  return align ? ` style="text-align:${align}"` : "";
}

// mdLink is a function in this module. Only http(s) and mailto targets
// become anchors; anything else renders as plain text, never as href.
// Anchors carry data-ext and are opened in the system browser by the
// chat view's delegated click handler.
function mdLink(text: string, rawUrl: string): string {
  const url = rawUrl
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .trim();
  if (!/^(https?:\/\/|mailto:)/i.test(url)) {
    return text;
  }
  return `<a class="md-link" href="#" data-ext="${escapeAttr(url)}">${text}</a>`;
}

// mdInline parses inline Markdown on already-escaped text: code spans
// and links are stashed first so inner markers stay literal, then
// bold/italic/strike, then bare URLs, then the stash is restored.
function mdInline(src: string): string {
  const chunks: string[] = [];
  const stash = (html: string): string => {
    chunks.push(html);
    return `${chunks.length - 1}`;
  };
  let s = src.replace(/``(.+?)``|`([^`\n]+?)`/g, (_m, a: string, b: string) =>
    stash(`<code class="md-code">${a ?? b}</code>`),
  );
  s = s.replace(/!\[([^\]]*?)\]\((\S+?)(?:\s+"[^"]*")?\)/g, (_m, alt: string, url: string) =>
    stash(mdLink(alt.trim() || "image", url)),
  );
  s = s.replace(/\[([^\]]+?)\]\((\S+?)(?:\s+"[^"]*")?\)/g, (_m, t: string, url: string) =>
    stash(mdLink(t, url)),
  );
  s = s.replace(/\*\*\*([^*\n]+?)\*\*\*/g, "<strong><em>$1</em></strong>");
  s = s.replace(/\*\*([^*\n]+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__([^_\n]+?)__/g, "<strong>$1</strong>");
  s = s.replace(/(^|[^\w*])\*([^*\n]+?)\*/g, "$1<em>$2</em>");
  s = s.replace(/(^|[^\w])_([^_\n]+?)_(?![\w])/g, "$1<em>$2</em>");
  s = s.replace(/~~([^~\n]+?)~~/g, "<del>$1</del>");
  s = s.replace(/&lt;(https?:[^&\s]+?)&gt;/g, (_m, url: string) => stash(mdLink(url, url)));
  s = s.replace(/(^|[\s(>])(https?:\/\/[^\s<]+)/g, (_m, pre: string, url: string) => {
    let clean = url.replace(/[.,;:!?]+$/, "");
    let opens = 0;
    let closes = 0;
    for (const ch of clean) {
      if (ch === "(") {
        opens++;
      } else if (ch === ")") {
        closes++;
      }
    }
    while (clean.endsWith(")") && closes > opens) {
      clean = clean.slice(0, -1);
      closes--;
    }
    return `${pre}${stash(mdLink(clean, clean))}${url.slice(clean.length)}`;
  });
  return s.replace(/(\d+)/g, (_m, n: string) => chunks[Number(n)] ?? "");
}

// renderText is a function in this module. It is the plain-text
// renderer for user bubbles and error rows: HTML-escaped with fenced
// code blocks highlighted, and no other Markdown interpretation.
export function renderText(text: string): string {
  const parts = escapeHtml(text).split(/```/);
  if (parts.length === 1) {
    return parts[0];
  }

  let out = "";
  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 0) {
      out += `<span class="whitespace-pre-wrap">${parts[i]}</span>`;
      continue;
    }

    const code = parts[i].replace(/^[a-z]*\n/, "");
    out += `<pre class="my-2 overflow-x-auto rounded-lg border border-slate-200 bg-slate-900 p-3 font-mono text-[11px] leading-relaxed text-slate-200 dark:border-slate-800"><code>${code}</code></pre>`;
  }
  return out;
}

// prettyJson is a function in this module.
// It supports the desktop app frontend.
export function prettyJson(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

// tryParseJson parses a JSON string into a record.
// It returns null when the text is not a JSON object.
export function tryParseJson(raw: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(raw);
    if (typeof value === "object" && value !== null) {
      return value as Record<string, unknown>;
    }
  } catch {
  }
  return null;
}

// dayCache avoids rebuilding "Today"/date strings per row per render.
const dayCache = new Map<number, string>();

// dayOf is a function in this module.
// It supports the desktop app frontend.
export function dayOf(timestamp: number): string {
  const key = Math.floor(timestamp / 86400);
  const cached = dayCache.get(key);
  if (cached !== undefined) {
    return cached;
  }
  if (dayCache.size > 400) {
    dayCache.clear();
  }
  const date = new Date(timestamp * 1000);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);

  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();

  let out: string;
  if (sameDay(date, today)) {
    out = "Today";
  } else if (sameDay(date, yesterday)) {
    out = "Yesterday";
  } else {
    out = date.toLocaleDateString([], { month: "short", day: "numeric" });
  }
  dayCache.set(key, out);
  return out;
}

// shortPath is a function in this module.
// It supports the desktop app frontend.
export function shortPath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : path || "workspace";
}

// fuzzyMatch is a function in this module.
// It supports the desktop app frontend.
export function fuzzyMatch(query: string, text: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) {
    return true;
  }

  const t = text.toLowerCase();
  let at = 0;
  for (const char of q) {
    at = t.indexOf(char, at);
    if (at === -1) {
      return false;
    }
    at++;
  }
  return true;
}

// bare is a function in this module.
// It supports the desktop app frontend.
export function bare(text: string): string {
  return text.replace(/^~+/, "");
}

// splitModel is a function in this module.
// It supports the desktop app frontend.
export function splitModel(id: string): { lab: string; short: string } {
  const at = id.indexOf("/");
  if (at === -1) {
    return { lab: "other", short: id };
  }
  return { lab: id.slice(0, at) || "other", short: id.slice(at + 1) };
}

// modelsOf is a function in this module.
// It supports the desktop app frontend.
export function modelsOf(provider: ProviderInfo | undefined): string[] {
  if (!provider) {
    return [];
  }
  if (provider.models.length > 0) {
    return provider.models;
  }
  return provider.model ? [provider.model] : [];
}

// modelsInLab is a function in this module.
// It supports the desktop app frontend.
export function modelsInLab(provider: ProviderInfo | undefined, lab: string): string[] {
  if (!provider) {
    return [];
  }
  return modelsOf(provider)
    .filter((m) => (lab ? splitModel(m).lab === lab : true))
    .sort((a, b) => a.localeCompare(b));
}

// labKey is a function in this module.
// It supports the desktop app frontend.
export function labKey(provider: string, lab: string): string {
  return `${provider}	${lab}`;
}

// splitLines splits text into display lines on LF.
// It strips a trailing CR so CRLF files render cleanly, and drops the
// phantom empty line a trailing newline would otherwise produce.
export function splitLines(text: string): string[] {
  const lines = text
    .split("\n")
    .map((line) => (line.endsWith("\r") ? line.slice(0, -1) : line));
  if (lines.length > 1 && lines[lines.length - 1] === "") {
    lines.pop();
  }
  return lines;
}

// ordinal renders 1 as 1st, 2 as 2nd, 3 as 3rd.
// It backs the ambiguous-match headings in the edit view.
export function ordinal(n: number): string {
  const teen = n % 100;
  if (teen >= 11 && teen <= 13) {
    return `${n}th`;
  }
  switch (n % 10) {
    case 1:
      return `${n}st`;
    case 2:
      return `${n}nd`;
    case 3:
      return `${n}rd`;
    default:
      return `${n}th`;
  }
}

// compactTokens renders a token count as 950, 31k or 1.2M.
export function compactTokens(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (n >= 1000) {
    return `${Math.round(n / 1000)}k`;
  }
  return String(Math.max(0, Math.round(n)));
}
