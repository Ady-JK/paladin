export function escapeHtml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

export function escapeAttr(text: string): string {
  return escapeHtml(text).replace(/"/g, "&quot;");
}

export function renderText(text: string): string {
  const parts = escapeHtml(text).split(/```/);
  if (parts.length === 1) return parts[0];
  let out = "";
  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 0) {
      out += `<span class="ws-pre">${parts[i]}</span>`;
    } else {
      const code = parts[i].replace(/^[a-z]*\n/, "");
      out += `<pre class="code">${code}</pre>`;
    }
  }
  return out;
}

const dayCache = new Map<number, string>();
export function dayOf(timestamp: number): string {
  const key = Math.floor(timestamp / 86400);
  const cached = dayCache.get(key);
  if (cached !== undefined) return cached;
  if (dayCache.size > 400) dayCache.clear();
  const date = new Date(timestamp * 1000);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  let out: string;
  if (sameDay(date, today)) out = "Today";
  else if (sameDay(date, yesterday)) out = "Yesterday";
  else out = date.toLocaleDateString([], { month: "short", day: "numeric" });
  dayCache.set(key, out);
  return out;
}

export function shortPath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : path || "workspace";
}

export function splitModel(id: string): { lab: string; short: string } {
  const slash = id.indexOf("/");
  if (slash < 0) return { lab: "other", short: id };
  return { lab: id.slice(0, slash), short: id.slice(slash + 1) };
}
