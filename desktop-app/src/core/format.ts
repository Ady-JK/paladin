// format.ts holds pure string, date, and JSON helpers.
// It has no DOM or backend dependencies so any module can use it.

import katex from "katex";
import type { ProviderInfo, TurnStats } from "./types";

// formatTurnStats renders a finished turn's cost: "10m 9s · 103.9
// tok/s". The duration is the turn's wall time; the rate is generation
// speed — generated (completion) tokens over the time spent inside the
// model calls themselves, which is the figure a provider dashboard
// reports. Turns recorded before that model time was stored have no
// rate to show, so they get the duration alone rather than a rate
// computed over tool runs and approvals.
export function formatTurnStats(stats: TurnStats): string {
  const duration = formatTurnDuration(stats.durationMs);
  if (!stats.genMs || stats.genMs <= 0) return duration;
  const rate = stats.tokens / (stats.genMs / 1000);
  return `${duration} · ${rate.toFixed(1)} tok/s`;
}

export function formatTurnDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

// escapeHtml makes text safe in element content and in quoted
// attributes: a quote left as is would close the attribute and let the
// text add its own (an event handler, say).
export function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// revealHidden makes invisible and direction-changing characters (zero-
// width spaces, bidi overrides, control characters) visible in text that
// was already escaped, so an approval card shows exactly what will run.
export function revealHidden(html: string): string {
  return html.replace(/[\p{Cf}\p{Cc}]/gu, (ch) =>
    ch === "\t" || ch === "\n" || ch === "\r"
      ? ch
      : `<span class="hidden-char" title="Invisible character">⟨U+${ch.codePointAt(0)!.toString(16).toUpperCase().padStart(4, "0")}⟩</span>`,
  );
}

export function escapeAttr(text: string): string {
  return escapeHtml(text);
}

// Math uses Pandoc's delimiters: $...$ is inline math when the opening $
// is followed by a non-space and the closing $ is preceded by one and not
// followed by a digit, so "$5 and $10" stays text. $$...$$ is display
// math, and \$ is a literal dollar sign. Code spans and fences are matched
// first and returned unchanged, so a $ inside code is never math.
const MATH_TOKEN =
  /```[\s\S]*?(?:```|$)|``.+?``|`[^`\n]+?`|\\\$|\$\$([\s\S]+?)\$\$|(?<![\\$])\$(?=[^\s$])([^$\n]*?[^\s$\\])\$(?!\d)/g;

// Held values are replaced by a run of private-use markers around a
// letters-only index (digits would collide with the inline stash).
const HELD_OPEN = "\uE000";
const HELD_CLOSE = "\uE001";
const HELD_DIGITS = "abcdefghij";
const HELD_TOKEN = /\uE000([a-j]+)\uE001/g;

// protectMath renders math with KaTeX and swaps it, and \$, for held
// placeholders before the text is escaped and parsed. LaTeX keeps its
// raw characters (<, &, |, *, _) and never meets the Markdown rules.
// KaTeX's own output is the only HTML that bypasses escapeHtml, and
// KaTeX keeps it trusted (no \href or \includegraphics by default).
function protectMath(text: string, held: string[]): string {
  return text.replace(MATH_TOKEN, (match: string, display?: string, inline?: string) => {
    if (match === "\\$") {
      return hold(held, "$");
    }
    if (display === undefined && inline === undefined) {
      return match;
    }
    const tex = (display ?? inline ?? "").trim();
    return hold(held, renderTex(tex, display !== undefined));
  });
}

// renderTex renders one formula, remembering recent results: a streaming
// reply is re-rendered every few tokens and its earlier formulas have not
// changed.
const TEX_CACHE_MAX = 200;
const texCache = new Map<string, string>();
function renderTex(tex: string, displayMode: boolean): string {
  const key = (displayMode ? "d" : "i") + tex;
  const hit = texCache.get(key);
  if (hit !== undefined) {
    texCache.delete(key); // re-insert: most recently used last
    texCache.set(key, hit);
    return hit;
  }
  const html = katex.renderToString(tex, { displayMode, throwOnError: false });
  if (texCache.size >= TEX_CACHE_MAX) {
    texCache.delete(texCache.keys().next().value as string);
  }
  texCache.set(key, html);
  return html;
}

function hold(held: string[], html: string): string {
  held.push(html);
  const index = [...String(held.length - 1)].map((d) => HELD_DIGITS[Number(d)]).join("");
  return HELD_OPEN + index + HELD_CLOSE;
}

// renderMarkdown renders agent output as Markdown HTML: fenced code,
// headings, bold/italic/strikethrough, inline code, nested lists with
// task items, blockquotes, tables, rules, links, and LaTeX math. The input
// is HTML-escaped first, so the tags below are the only markup in the
// output — agent text can never inject elements or attributes.
export function renderMarkdown(text: string): string {
  const held: string[] = [];
  const protectedText = protectMath(text.replace(/[\uE000\uE001]/g, ""), held);
  const parts = escapeHtml(protectedText).split(/```/);
  let out = "";
  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 1) {
      out += renderFence(parts[i]);
      continue;
    }
    out += renderBlocks(parts[i]);
  }
  return out.replace(HELD_TOKEN, (_m, index: string) => {
    const at = Number([...index].map((c) => HELD_DIGITS.indexOf(c)).join(""));
    return held[at] ?? "";
  });
}

// dedent strips the indentation every line shares (a fence nested in a
// list item keeps its list indent) and the blank lines around the code,
// so a one-line block is one tight line.
function dedent(code: string): string {
  const lines = code
    .replace(/\s+$/, "")
    .replace(/^(?:[ \t]*\n)+/, "")
    .split("\n");
  let indent = Infinity;
  for (const line of lines) {
    if (line.trim()) {
      indent = Math.min(indent, /^[ \t]*/.exec(line)![0].length);
    }
  }
  return indent && Number.isFinite(indent) ? lines.map((line) => line.slice(indent)).join("\n") : lines.join("\n");
}

// renderFence renders one ``` block. The language tag on the first line
// is not shown; a copy button takes its corner instead. An unclosed
// fence (still streaming) renders the same way.
function renderFence(block: string): string {
  const nl = block.indexOf("\n");
  const code = dedent(nl === -1 ? "" : block.slice(nl + 1));
  const copy = `<button type="button" class="md-copy" data-copy-code title="Copy" aria-label="Copy code"></button>`;
  const one = code.includes("\n") ? "" : " is-oneline";
  return `<pre class="md-pre${one}"><code>${code}</code>${copy}</pre>`;
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
      let rows = 0;
      while (i < lines.length && isTableRow(lines[i])) {
        const cells = splitRow(lines[i]);
        body += `<tr>${headCells.map((_, c) => `<td class="md-td"${alignAttr(aligns[c])}>${mdInline(cells[c] ?? "")}</td>`).join("")}</tr>`;
        rows++;
        i++;
      }
      // More than ten rows become a scroll box so a long table cannot
      // take over the transcript; wide headers scroll sideways instead
      // of squeezing every column.
      const tall = rows > 10 ? " is-tall" : "";
      const table = `<table class="md-table"><thead><tr>${headCells.map((c, k) => `<th class="md-th"${alignAttr(aligns[k])}>${mdInline(c)}</th>`).join("")}</tr></thead><tbody>${body}</tbody></table>`;
      html += `<div class="md-table-wrap${tall}"${rows > 10 ? ` data-rows="${rows}"` : ""}>${table}</div>`;
      continue;
    }
    if (/^\s*(?:[-*+]|\d+[.)])\s+/.test(line)) {
      flush();
      const taken: string[] = [];
      while (i < lines.length) {
        if (/^\s*$/.test(lines[i])) {
          let at = i + 1;
          while (at < lines.length && /^\s*$/.test(lines[at])) {
            at++;
          }
          if (at < lines.length && /^\s*(?:[-*+]|\d+[.)])\s+/.test(lines[at])) {
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

// isTableRow: any line holding a pipe can
// open or extend a table; the delimiter line below confirms it.
function isTableRow(line: string): boolean {
  return line.includes("|");
}

// isTableDelim matches the GFM
// separator row (| --- | :---: | ---: |) under a table header.
function isTableDelim(line: string): boolean {
  const cells = splitRow(line);
  if (cells.length === 0) {
    return false;
  }
  return cells.every((c) => /^:?-{1,}:?$/.test(c.trim()));
}

// splitRow cuts a table row on pipes,
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

// alignOf reads a delimiter cell.
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

// alignAttr renders a delimiter cell
// as a style attribute, or nothing for default alignment.
function alignAttr(align: string | undefined): string {
  return align ? ` style="text-align:${align}"` : "";
}

// mdLink: only http(s) and mailto targets
// become anchors; anything else renders as plain text, never as href.
// Anchors carry data-ext and are opened in the system browser by the
// chat view's delegated click handler.
function mdLink(text: string, rawUrl: string): string {
  const url = rawUrl
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, "&")
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
  s = s.replace(/!\[([^\]]*?)\]\((\S+?)(?:\s+&quot;.*?&quot;)?\)/g, (_m, alt: string, url: string) =>
    stash(mdLink(alt.trim() || "image", url)),
  );
  s = s.replace(/\[([^\]]+?)\]\((\S+?)(?:\s+&quot;.*?&quot;)?\)/g, (_m, t: string, url: string) =>
    stash(mdLink(t, url)),
  );
  s = s.replace(/\*\*\*([^*\n]+?)\*\*\*/g, "<strong><em>$1</em></strong>");
  s = s.replace(/\*\*([^*\n]+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__([^_\n]+?)__/g, "<strong>$1</strong>");
  s = s.replace(/(^|[^\w*])\*([^*\n]+?)\*/g, "$1<em>$2</em>");
  s = s.replace(/(^|[^\w])_([^_\n]+?)_(?![\w])/g, "$1<em>$2</em>");
  s = s.replace(/~~([^~\n]+?)~~/g, "<del>$1</del>");
  s = s.replace(/&lt;(https?:[^&\s]+?)&gt;/g, (_m, url: string) => stash(mdLink(url, url)));
  s = s.replace(/(^|[\s(>])(https?:\/\/[^\s<]+)/g, (_m, pre: string, url: string) => {
    let clean = url.replace(/(?:[.,;:!?]|&quot;|&#39;)+$/, "");
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
  // A stashed chunk can hold an earlier marker (code inside a link's text),
  // so restore until none is left.
  for (let pass = 0; pass < 4 && s.includes(""); pass++) {
    s = s.replace(/(\d+)/g, (_m, n: string) => chunks[Number(n)] ?? "");
  }
  return s;
}

// renderText is the plain-text
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

// tryParseJson parses a JSON string into a record.
// It returns null when the text is not a JSON object. The big strings (a
// write's file content, a command's output) are remembered: one tool row
// asks for the same arguments from half a dozen places on every update, and
// callers only read what comes back.
const PARSE_MEMO_MIN = 512;
const PARSE_MEMO_MAX = 64;
const parseMemo = new Map<string, Record<string, unknown> | null>();
export function tryParseJson(raw: string): Record<string, unknown> | null {
  const memo = raw.length >= PARSE_MEMO_MIN;
  if (memo && parseMemo.has(raw)) {
    return parseMemo.get(raw) ?? null;
  }
  let out: Record<string, unknown> | null = null;
  try {
    const value: unknown = JSON.parse(raw);
    if (typeof value === "object" && value !== null) {
      out = value as Record<string, unknown>;
    }
  } catch {
    // Not JSON: the caller falls back to the raw text.
  }
  if (memo) {
    if (parseMemo.size >= PARSE_MEMO_MAX) {
      parseMemo.delete(parseMemo.keys().next().value as string);
    }
    parseMemo.set(raw, out);
  }
  return out;
}

export function shortPath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : path || "workspace";
}

export function bare(text: string): string {
  return text.replace(/^~+/, "");
}

export function splitModel(id: string): { lab: string; short: string } {
  const at = id.indexOf("/");
  if (at === -1) {
    return { lab: "other", short: id };
  }
  return { lab: id.slice(0, at) || "other", short: id.slice(at + 1) };
}

export function modelsOf(provider: ProviderInfo | undefined): string[] {
  if (!provider) {
    return [];
  }
  if (provider.models.length > 0) {
    return provider.models;
  }
  return provider.model ? [provider.model] : [];
}

// splitLines splits text into display lines on LF.
// It strips a trailing CR so CRLF files render cleanly, and drops the
// phantom empty line a trailing newline would otherwise produce.
export function splitLines(text: string): string[] {
  const lines = text.split("\n").map((line) => (line.endsWith("\r") ? line.slice(0, -1) : line));
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

// approxTokens renders a token count in whole thousands: 950, 31k
// or 1.2M. usage.ts compactTokens keeps one decimal for dashboards.
export function approxTokens(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (n >= 1000) {
    return `${Math.round(n / 1000)}k`;
  }
  return String(Math.max(0, Math.round(n)));
}
