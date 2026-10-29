// toolBodies.ts renders the expanded body of a tool call as something a
// person would read: a terminal for shell, numbered lines for read, a
// checklist for todo, Q&A for ask and key/value tables for anything
// else. Raw JSON never reaches the screen.

import { escapeHtml } from "../core/format";
import type { ToolInfo } from "../core/types";
import { state } from "../core/state";

type Obj = Record<string, unknown>;

const MAX_TEXT = 8000;
const MAX_LINES = 400;
const MAX_ITEMS = 50;

const isObj = (v: unknown): v is Obj => !!v && typeof v === "object" && !Array.isArray(v);
const str = (v: unknown): string => (typeof v === "string" ? v : "");
const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

// capText keeps multi-KB dumps out of the live DOM; the full text stays
// in the session transcript on disk.
function capText(text: string): string {
  return text.length > MAX_TEXT ? `${text.slice(0, MAX_TEXT)}\n…(${text.length - MAX_TEXT} more characters in the history)` : text;
}

function parse(raw: string): unknown {
  if (!raw) {
    return null;
  }
  try {
    const value: unknown = JSON.parse(raw);
    return value ?? raw;
  } catch {
    return raw;
  }
}

function relPath(path: string): string {
  const root = state.cwd.replace(/[\\/]+$/, "");
  if (root && path.startsWith(root) && /[\\/]/.test(path.charAt(root.length))) {
    return path.slice(root.length + 1);
  }
  return path;
}

function trimEnd(text: string): string {
  return text.replace(/\s+$/, "");
}

// errorOf pulls the message out of a {"error": "..."} result.
function errorOf(tool: ToolInfo, result: unknown): string | null {
  if (isObj(result) && typeof result.error === "string") {
    return result.error;
  }
  if (tool.status === "failed") {
    return typeof result === "string" && result ? result : "the call failed";
  }
  return null;
}

const errBlock = (msg: string) => `<div class="tool-err">${escapeHtml(capText(msg))}</div>`;

// ---- shell ----

function shellStatus(tool: ToolInfo, args: Obj, result: unknown): { text: string; tone: string } {
  if (tool.status === "running") {
    return { text: "running…", tone: "is-run" };
  }
  if (!isObj(result)) {
    return { text: tool.status === "failed" ? "failed" : "done", tone: tool.status === "failed" ? "is-bad" : "is-ok" };
  }
  const status = str(result.status);
  const code = num(result.exit_code);
  if (status === "running") {
    return args.background === true && str(args.action || "run") === "run"
      ? { text: "running in background", tone: "is-run" }
      : { text: "still running", tone: "is-run" };
  }
  if (status === "timeout") {
    return { text: "timed out", tone: "is-bad" };
  }
  if (status === "cancelled") {
    return { text: "cancelled", tone: "is-warn" };
  }
  if (code !== null) {
    return { text: `exit ${code}`, tone: code === 0 ? "is-ok" : "is-bad" };
  }
  if (status === "failed") {
    return { text: "failed", tone: "is-bad" };
  }
  return { text: status || "done", tone: "is-ok" };
}

function shellHtml(tool: ToolInfo, args: Obj, result: unknown, err: string | null): string {
  const action = str(args.action) || "run";
  const cmd = str(args.cmd).trim() || `${action}${str(args.job_id) ? ` ${str(args.job_id)}` : ""}`;
  const st = err ? { text: "failed", tone: "is-bad" } : shellStatus(tool, args, result);
  let html =
    `<div data-body="shell" class="term">` +
    `<div class="term-head"><span class="term-prompt">$</span><code class="term-cmd">${escapeHtml(cmd)}</code>` +
    `<span class="term-status ${st.tone}">${escapeHtml(st.text)}</span></div>`;
  if (err) {
    return `${html}${errBlock(err)}</div>`;
  }
  const res = isObj(result) ? result : {};
  const output = isObj(result) ? str(res.output) : typeof result === "string" ? result : "";
  if (output.trim()) {
    html += `<pre class="term-out">${escapeHtml(capText(trimEnd(output)))}</pre>`;
  } else if (tool.status !== "running" && str(res.status) !== "running") {
    html += `<div class="term-empty">no output</div>`;
  }
  const meta: string[] = [];
  if (str(res.job_id)) {
    meta.push(`<span><b>job</b> <code>${escapeHtml(str(res.job_id))}</code></span>`);
  }
  if (str(res.log)) {
    meta.push(`<span><b>log</b> <code>${escapeHtml(str(res.log))}</code></span>`);
  }
  if (num(args.timeout_ms)) {
    meta.push(`<span><b>timeout</b> ${Math.round((num(args.timeout_ms) as number) / 1000)}s</span>`);
  }
  if (res.truncated === true) {
    html += `<div class="term-note">Output truncated: only the first part was kept.</div>`;
  }
  if (meta.length) {
    html += `<div class="term-meta">${meta.join("")}</div>`;
  }
  return `${html}</div>`;
}

// ---- read ----

function readHtml(tool: ToolInfo, args: Obj, result: unknown, err: string | null): string {
  const res = isObj(result) ? result : {};
  const path = relPath(str(res.resolved_path) || str(args.path) || str(res.requested_path));
  const start = num(res.start_line) ?? 0;
  const end = num(res.end_line) ?? 0;
  const chips: string[] = [];
  if (tool.status === "running") {
    chips.push(`<span class="file-chip">reading…</span>`);
  } else if (!err && start > 0) {
    chips.push(`<span class="file-chip">lines ${start}–${end}</span>`);
  }
  if (str(res.scope) === "outside") {
    chips.push(`<span class="file-chip is-warn">outside workspace</span>`);
  }
  if (res.has_more === true) {
    chips.push(`<span class="file-chip file-more" title="Stopped at the ${escapeHtml(str(res.stop_reason).replace("_", " ") || "limit")}">more below</span>`);
  }
  let html =
    `<div data-body="read" class="file-view">` +
    `<div class="file-head"><span class="file-path">${escapeHtml(path)}</span>${chips.join("")}</div>`;
  if (err) {
    return `${html}${errBlock(err)}</div>`;
  }
  const content = str(res.content);
  if (!content && tool.status !== "running") {
    return `${html}<div class="file-empty">${start === 0 ? "empty file" : "no lines in range"}</div></div>`;
  }
  const lines = content.replace(/\n$/, "").split("\n");
  const shown = lines.slice(0, MAX_LINES);
  const first = start || 1;
  html += `<div class="file-lines">`;
  shown.forEach((line, i) => {
    html += `<div class="file-line"><span class="ln">${first + i}</span><code>${escapeHtml(line) || " "}</code></div>`;
  });
  if (lines.length > shown.length) {
    html += `<div class="file-empty">… ${lines.length - shown.length} more lines in the history</div>`;
  }
  return `${html}</div></div>`;
}

// ---- todo ----

const TODO_MARK: Record<string, string> = { finished: "done", in_progress: "doing", not_started: "todo" };

function todoHtml(tool: ToolInfo, args: Obj, result: unknown, err: string | null): string {
  if (err) {
    return errBlock(err);
  }
  const list = isObj(result) && Array.isArray(result.todos) ? result.todos : Array.isArray(args.todos) ? args.todos : [];
  if (list.length === 0) {
    return `<div data-body="todo" class="tb-muted">${tool.status === "running" ? "updating the plan…" : "the plan is empty"}</div>`;
  }
  const items = list
    .filter(isObj)
    .slice(0, MAX_ITEMS)
    .map((t) => {
      const st = str(t.status) || "not_started";
      return `<li data-state="${escapeHtml(st)}" title="${TODO_MARK[st] ?? st}">${escapeHtml(str(t.content))}</li>`;
    })
    .join("");
  return `<ul data-body="todo" class="todo-check">${items}</ul>`;
}

// ---- ask ----

function askHtml(tool: ToolInfo, args: Obj, result: unknown, err: string | null): string {
  const qs = Array.isArray(args.questions) ? args.questions.filter(isObj) : [];
  const answers = new Map<string, string>();
  if (isObj(result) && Array.isArray(result.answers)) {
    for (const a of result.answers.filter(isObj)) {
      answers.set(str(a.question), str(a.answer));
    }
  }
  let html = `<div data-body="ask" class="qa">`;
  qs.forEach((q, i) => {
    const question = str(q.question);
    const answer = answers.get(question) ?? (isObj(result) && Array.isArray(result.answers) && isObj(result.answers[i]) ? str((result.answers[i] as Obj).answer) : "");
    const opts = Array.isArray(q.options) ? q.options.map(str).filter(Boolean) : [];
    html += `<div class="qa-item"><div class="qa-q">${escapeHtml(question)}</div>`;
    if (opts.length) {
      html += `<div class="qa-opts">${opts
        .map((o) => `<span class="qa-opt${o === answer ? " is-picked" : ""}">${escapeHtml(o)}</span>`)
        .join("")}</div>`;
    }
    if (answer) {
      html += `<div class="qa-a">${escapeHtml(answer)}</div>`;
    } else if (tool.status === "running") {
      html += `<div class="qa-wait">waiting for your answer…</div>`;
    }
    html += `</div>`;
  });
  html += `</div>`;
  return err ? html + errBlock(err) : html;
}

// ---- generic: key/value ----

function valueHtml(v: unknown, depth: number): string {
  if (v === null || v === undefined) {
    return `<span class="kv-tok is-null">none</span>`;
  }
  if (typeof v === "boolean") {
    return `<span class="kv-tok is-bool">${v}</span>`;
  }
  if (typeof v === "number") {
    return `<span class="kv-tok is-num">${v}</span>`;
  }
  if (typeof v === "string") {
    return v.includes("\n") || v.length > 160
      ? `<pre class="kv-text">${escapeHtml(capText(trimEnd(v)))}</pre>`
      : escapeHtml(v);
  }
  if (Array.isArray(v)) {
    if (v.length === 0) {
      return `<span class="kv-tok is-null">empty</span>`;
    }
    const shown = v.slice(0, MAX_ITEMS);
    const more = v.length > shown.length ? `<span class="kv-tok is-null">+${v.length - shown.length} more</span>` : "";
    if (shown.every((x) => x === null || typeof x !== "object")) {
      return `<span class="kv-chips">${shown.map((x) => `<span class="kv-chip">${escapeHtml(String(x))}</span>`).join("")}${more}</span>`;
    }
    return `<ol class="kv-list">${shown.map((x) => `<li>${valueHtml(x, depth + 1)}</li>`).join("")}</ol>${more}`;
  }
  if (isObj(v)) {
    const keys = Object.keys(v);
    if (keys.length === 0) {
      return `<span class="kv-tok is-null">empty</span>`;
    }
    if (depth >= 3) {
      return `<span class="kv-tok is-null">${keys.length} fields</span>`;
    }
    return kvHtml(v, depth + 1);
  }
  return escapeHtml(String(v));
}

function kvHtml(obj: Obj, depth = 0, attr = ""): string {
  const rows = Object.entries(obj)
    .slice(0, MAX_ITEMS)
    .map(([k, v]) => `<div class="kv-row"><div class="kv-key">${escapeHtml(k)}</div><div class="kv-val">${valueHtml(v, depth)}</div></div>`)
    .join("");
  return `<div${attr} class="kv">${rows}</div>`;
}

function section(label: string, inner: string): string {
  return `<div class="tb-sec"><div class="tb-label">${label}</div>${inner}</div>`;
}

function genericHtml(tool: ToolInfo, args: unknown, result: unknown, err: string | null): string {
  let html = "";
  if (isObj(args) && Object.keys(args).length) {
    html += section("Called with", kvHtml(args, 0, ' data-body="args"'));
  } else if (typeof args === "string" && args.trim()) {
    html += section("Called with", `<pre data-body="args" class="kv-text">${escapeHtml(capText(args))}</pre>`);
  }
  if (err) {
    return html + errBlock(err);
  }
  let out: string;
  if (tool.status === "running") {
    out = `<div data-body="result" class="tb-muted">running…</div>`;
  } else if (result === null || result === "") {
    out = `<div data-body="result" class="tb-muted">no output</div>`;
  } else if (isObj(result)) {
    out = kvHtml(result, 0, ' data-body="result"');
  } else if (Array.isArray(result)) {
    out = `<div data-body="result">${valueHtml(result, 0)}</div>`;
  } else {
    out = `<pre data-body="result" class="kv-text">${escapeHtml(capText(trimEnd(String(result))))}</pre>`;
  }
  return html + section("Returned", out);
}

// toolBodyHtml renders a call's body. Write and edit keep their own rich
// file views (passed in); everything else is handled here.
export function toolBodyHtml(tool: ToolInfo, rich: string | null = null): string {
  if (rich) {
    return rich;
  }
  const rawArgs = parse(tool.arguments);
  const args = isObj(rawArgs) ? rawArgs : {};
  const result = tool.status === "running" ? null : parse(tool.result);
  const err = tool.status === "running" ? null : errorOf(tool, result);
  switch (tool.name) {
    case "shell":
      return shellHtml(tool, args, result, err);
    case "read":
      return readHtml(tool, args, result, err);
    case "todo":
      return todoHtml(tool, args, result, err);
    case "ask":
      return askHtml(tool, args, result, err);
    default:
      return genericHtml(tool, rawArgs, result, err);
  }
}
