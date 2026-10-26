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

// renderText is a function in this module.
// It supports the desktop app frontend.
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

// dayOf is a function in this module.
// It supports the desktop app frontend.
export function dayOf(timestamp: number): string {
  const date = new Date(timestamp * 1000);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);

  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();

  if (sameDay(date, today)) {
    return "Today";
  }
  if (sameDay(date, yesterday)) {
    return "Yesterday";
  }
  return date.toLocaleDateString([], { month: "short", day: "numeric" });
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
