// tools.ts renders tool-call activity cards and file diffs.
// It is pure DOM building: no backend calls and no shared state.

import { tryParseJson } from "../core/format";
import type { ToolInfo, ToolView } from "../core/types";
import { icon, type IconName } from "../icons";
import { relPath, toolBodyHtml } from "./toolBodies";
import { changedLines, richEditHtml, richWriteHtml } from "./toolCards/file";
import { createLineDiffer, type LineDiffer } from "./toolCards/lineDiff";

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
      const command = typeof args.command === "string" ? args.command : typeof args.cmd === "string" ? args.cmd : "";
      if (command.trim()) {
        return short(command.trim());
      }
      const action = typeof args.action === "string" && args.action ? args.action : "run";
      const job = typeof args.job_id === "string" && args.job_id ? ` ${args.job_id}` : "";
      return `${action}${job}`;
    }
    if ((name === "grep" || name === "glob") && typeof args.pattern === "string") {
      return short(args.pattern);
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

// The subagent control tools; "agent" is a finished child's notice.
const isAgentTool = (name: string): boolean => Object.prototype.hasOwnProperty.call(AGENT_KINDS, name);

const AGENT_KINDS: Record<string, ToolKind> = {
  spawn_agent: { icon: "rl-agent", past: "Spawned", now: "Spawning", noun: plural("subagent", "subagents") },
  wait_agent: { icon: "rl-agent", past: "Waited for", now: "Waiting for", noun: plural("subagent", "subagents") },
  send_message: { icon: "rl-agent", past: "Messaged", now: "Messaging", noun: plural("subagent", "subagents") },
  followup_task: { icon: "rl-agent", past: "Followed up", now: "Following up", noun: plural("subagent", "subagents") },
  interrupt_agent: { icon: "rl-agent", past: "Stopped", now: "Stopping", noun: plural("subagent", "subagents") },
  list_agents: { icon: "rl-agent", past: "Listed", now: "Listing", noun: plural("subagent list", "subagent lists") },
  agent: { icon: "rl-agent", past: "Heard from", now: "Hearing from", noun: plural("subagent", "subagents") },
};

const KINDS: Record<string, ToolKind> = {
  shell: { icon: "tl-term", past: "Ran", now: "Running", noun: plural("command", "commands") },
  read: { icon: "tl-file", past: "Read", now: "Reading", noun: plural("file", "files") },
  grep: { icon: "tl-search", past: "Searched", now: "Searching", noun: plural("pattern", "patterns") },
  glob: { icon: "tl-search", past: "Matched", now: "Matching", noun: plural("file pattern", "file patterns") },
  list: { icon: "tl-folder", past: "Listed", now: "Listing", noun: plural("folder", "folders") },
  write: { icon: "tl-file-plus", past: "Wrote", now: "Writing", noun: plural("file", "files") },
  edit: { icon: "tl-pencil", past: "Edited", now: "Editing", noun: plural("file", "files") },
  ask: { icon: "tl-ask", past: "Asked", now: "Asking", noun: plural("question", "questions") },
  web_fetch: { icon: "globe", past: "Fetched", now: "Fetching", noun: plural("page", "pages") },
  todo: {
    icon: "tl-list",
    past: "Updated",
    now: "Updating",
    noun: (n) => (n === 1 ? "the plan" : `the plan ${n} times`),
  },
  ...AGENT_KINDS,
};

// agentDetail names the subagent an agent tool call is about.
function agentDetail(tool: ToolInfo, args: Record<string, unknown>): ToolLabel | null {
  if (!isAgentTool(tool.name)) return null;
  const kind = AGENT_KINDS[tool.name];
  const verb = tool.status === "running" ? kind.now : kind.past;
  const result = parseArgs(tool.result);
  const str = (v: unknown) => (typeof v === "string" ? v : "");
  const name = str(result.name) || str(args.name) || str(args.target);
  switch (tool.name) {
    case "spawn_agent":
      return { verb, detail: name || str(args.agent_type) || "a subagent", chip: str(args.agent_type) };
    case "wait_agent": {
      const targets = Array.isArray(args.targets) ? args.targets.map(String) : [];
      return { verb, detail: targets.length ? targets.join(", ") : "subagents", chip: "" };
    }
    case "list_agents":
      return { verb, detail: "subagents", chip: "" };
    case "agent":
      return { verb, detail: name || "a subagent", chip: str(result.status) };
    default:
      return { verb, detail: name || "a subagent", chip: "" };
  }
}

// agentIdOf is the child session an agent tool row can open.
function agentIdOf(tool: ToolInfo): string {
  if (!isAgentTool(tool.name) || tool.name === "list_agents" || tool.name === "wait_agent") return "";
  const id = parseArgs(tool.result).agent_id ?? parseArgs(tool.arguments).agent_id;
  return typeof id === "string" ? id : "";
}

const OTHER: ToolKind = { icon: "tl-tools", past: "Called", now: "Calling", noun: plural("tool", "tools") };
const MCP: ToolKind = { icon: "sl-plug", past: "Called", now: "Calling", noun: plural("MCP tool", "MCP tools") };

function kindOf(name: string): ToolKind {
  return KINDS[name] ?? (mcpName(name) ? MCP : OTHER);
}

function parseArgs(raw: string): Record<string, unknown> {
  const parsed = tryParseJson(raw);
  return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
}

// siteOf splits a URL into its host and the rest, for a row's label.
function siteOf(raw: string): { host: string; rest: string } {
  try {
    const u = new URL(raw);
    const rest = u.pathname === "/" && !u.search ? "" : `${u.pathname}${u.search}`;
    return { host: u.host, rest: rest.length > 60 ? `${rest.slice(0, 60)}…` : rest };
  } catch {
    return { host: "", rest: "" };
  }
}

// toolLabel reads one call the way a person would narrate it.
export function toolLabel(tool: ToolInfo): ToolLabel {
  const kind = kindOf(tool.name);
  const verb = tool.status === "running" ? kind.now : kind.past;
  const args = parseArgs(tool.arguments);
  const summary = toolSummary(tool.name, tool.arguments);
  const agent = agentDetail(tool, args);
  if (agent) return agent;
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
    case "web_fetch": {
      const site = siteOf(typeof args.url === "string" ? args.url : "");
      return { verb, detail: site.host || summary, chip: site.rest };
    }
    case "grep":
    case "glob":
      return { verb, detail: summary, chip: typeof args.path === "string" ? relPath(args.path) : "" };
    case "list":
      return { verb, detail: typeof args.path === "string" ? relPath(args.path) : "the workspace", chip: "" };
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
function paintToolHead(row: HTMLElement, head: HTMLElement, tool: ToolInfo, lineDiff: LineDiffer): void {
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
  const counts = changedLines(tool, lineDiff);
  const add = head.querySelector<HTMLElement>(".tool-add")!;
  const del = head.querySelector<HTMLElement>(".tool-del")!;
  add.textContent = counts ? `+${counts.added}` : "";
  del.textContent = counts ? `-${counts.removed}` : "";
  add.hidden = !counts;
  del.hidden = !counts;
  const fail = head.querySelector<HTMLElement>(".tool-fail")!;
  fail.textContent = tool.status === "failed" ? "failed" : "";
  fail.hidden = tool.status !== "failed";
}

// createToolActivity builds one call row: icon, verb, detail and chip on
// a single line; clicking it reveals the input/output or the file view.
export function createToolActivity(tool: ToolInfo): ToolView {
  let current = tool;
  const lineDiff = createLineDiffer();

  const box = document.createElement("div");
  box.className = "tool-row";
  box.setAttribute("data-tool", current.name);

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "tool-row-head";
  toggle.setAttribute("aria-expanded", "false");
  toggle.innerHTML =
    '<span class="tool-ico"></span><span class="tool-verb"></span><span class="tool-server"></span><span class="tool-detail"></span>' +
    '<code class="tool-chip"></code><span class="tool-add"></span><span class="tool-del"></span><span class="tool-fail"></span>';
  paintToolHead(box, toggle, current, lineDiff);

  const details = document.createElement("div");
  details.className = "tool-body hidden";
  let lastBody: string | null = null;

  // render rebuilds the body from the latest state, only while open:
  // a collapsed row has nothing to paint.
  const render = () => {
    if (details.classList.contains("hidden")) {
      return;
    }
    const rich =
      current.name === "write"
        ? richWriteHtml(current)
        : current.name === "edit"
          ? richEditHtml(current, lineDiff)
          : null;
    const html = toolBodyHtml(current, rich);
    if (html !== lastBody) {
      details.innerHTML = html;
      lastBody = html;
    }
  };

  toggle.addEventListener("click", () => {
    const open = !details.classList.toggle("hidden");
    toggle.setAttribute("aria-expanded", String(open));
    render();
  });

  box.append(toggle, details);

  // A subagent's row opens it in the side pane; outside the toggle, as
  // buttons cannot nest.
  const paintOpen = () => {
    const id = agentIdOf(current);
    let open = box.querySelector<HTMLButtonElement>(":scope > .tool-open-agent");
    if (!id) {
      open?.remove();
      return;
    }
    if (!open) {
      open = document.createElement("button");
      open.type = "button";
      open.className = "tool-open-agent";
      open.title = "Show this subagent beside the chat";
      open.innerHTML = `${icon("external", "h-3 w-3")}<span>Open</span>`;
      toggle.after(open);
    }
    open.dataset.openAgent = id;
  };
  paintOpen();

  const update = (next: ToolInfo) => {
    current = next;
    paintToolHead(box, toggle, current, lineDiff);
    paintOpen();
    render();
  };

  return { el: box, update };
}
