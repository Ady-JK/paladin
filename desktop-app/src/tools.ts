// Tool cards: static status dot, lazy body, 8KB DOM caps.
import { escapeHtml } from "./format";
import type { ToolActivity } from "./types";

function cap(text: string): string {
  return text.length > 8000 ? `${text.slice(0, 8000)}\n…(truncated, kept in history)` : text;
}

export function toolCard(tool: ToolActivity): { el: HTMLElement; update: (t: ToolActivity) => void } {
  const box = document.createElement("div");
  box.className = "tool-card";
  box.dataset.tool = tool.id;
  box.innerHTML = `
    <button class="tool-head">
      <span class="dot"></span>
      <span class="tname" style="font-weight:600"></span>
      <span class="tsum" style="color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap"></span>
    </button>
    <div class="tool-body" hidden><pre></pre></div>`;
  const dot = box.querySelector(".dot") as HTMLElement;
  const name = box.querySelector(".tname") as HTMLElement;
  const sum = box.querySelector(".tsum") as HTMLElement;
  const body = box.querySelector(".tool-body") as HTMLElement;
  const pre = box.querySelector("pre") as HTMLElement;
  const head = box.querySelector(".tool-head") as HTMLButtonElement;

  const paint = (t: ToolActivity): void => {
    name.textContent = t.name;
    dot.className = `dot ${t.status === "ok" ? "ok" : t.status === "failed" ? "failed" : ""}`;
    if (t.status === "running" && !dot.classList.contains("ok")) dot.classList.add("running");
    else dot.classList.remove("running");
    let short = "";
    try {
      const args = JSON.parse(t.arguments || "{}") as Record<string, unknown>;
      if (typeof args.cmd === "string") short = args.cmd.slice(0, 120);
      else if (typeof args.path === "string") short = args.path.slice(0, 120);
    } catch {
      short = "";
    }
    if (short) sum.textContent = short;
    if (!body.hidden) {
      const out = t.status === "running" ? "running…" : t.result || "(empty)";
      pre.textContent = cap(out);
    }
  };
  head.addEventListener("click", () => {
    body.hidden = !body.hidden;
    if (!body.hidden) paint(current);
  });
  let current = tool;
  paint(tool);
  return { el: box, update: (t) => { current = t; paint(t); } };
}
