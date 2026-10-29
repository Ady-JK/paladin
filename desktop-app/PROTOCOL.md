# Paladin JSONL protocol (Electron main <-> agent-host sidecar)

`agent-host` is a Go executable built from `core/cmd/agent-host`.
Electron main spawns it with piped stdio:

- stdin: requests, one JSON object per line
- stdout: responses + stream events, one JSON object per line
- stderr: logs (never JSONL)

## Requests (stdin)

```json
{"id": 1, "method": "list_providers", "params": {}}
{"id": "s2", "method": "chat", "params": {
  "sessionId": "", "message": "hi", "cwd": "/tmp/proj",
  "provider": null, "model": null, "systemPrompt": null, "resume": false}}
```

| method | params | result |
|---|---|---|
| `ping` | `{}` | `{"ok":true}` |
| `list_providers` | `{}` | `ProviderInfo[]` |
| `refresh_providers` | `{name?}` | no name: `bool` (background refresh-all started); `{name}`: `RefreshResult` (`name,count,models,error?`) for that provider only. Catalogs are never fetched on startup or on add/update — only on this explicit call |
| `set_default_provider` | `{name}` | `ProviderInfo[]` |
| `add_provider` | `{name,apiUrl,apiKey,apiType}` | `ProviderInfo` |
| `update_provider` | `{name,apiUrl,apiKey,apiType}` | `ProviderInfo` |
| `get_workspace` | `{}` | `string` |
| `set_workspace` | `{path}` | `string` |
| `resolve_workspace` | `{cwd}` | `string` |
| `fs_home` | `{}` | `string` |
| `fs_list` | `{path?}` | `FsList` |
| `file_complete` | `{query,cwd?,limit?}` | `FileEntry[]` (`path` chat path, `resolvedPath` OS-absolute path to insert, `outside`, `isDir`; gitignored paths never suggested; `../` completes from the workspace parent) |
| `open_session` | `{sessionId?,cwd?}` | `Session` |
| `list_sessions` | `{cwd?,limit?}` | `SessionInfo[]` |
| `session_history` | `{sessionId,cwd?}` | `HistoryMessage[]` |
| `session_usage` | `{sessionId,cwd?}` | `SessionUsage` (`sessionId,model,contextWindow,contextKnown,calls,promptTokens,completionTokens,reasoningTokens,cacheReadTokens,cacheWriteTokens,totalTokens,lastInputTokens,cost,listCost,hasCost,fillPercent?` + per-model `models[]` + per-day `daily[]`; `fillPercent` is the last call's input tokens over the window, omitted when the model's window is unknown) |
| `overall_usage` | `{cwd?,limit?}` | `OverallUsage` (fleet totals + `models[]` heaviest-first + `sessions[]` heaviest-first capped at `limit` + `daily[]` oldest-first) |
| `delete_session` | `{sessionId,cwd?}` | `bool` |
| `get_prompt` | `{}` | `Prompt` (`systemPrompt` = `config/SYSTEM_PROMPT.md`; a missing/blank file seeds from the built-in default) |
| `set_prompt` | `{systemPrompt}` | `Prompt` (blank rejected) |
| `reset_prompt` | `{}` | `Prompt` (file rewritten from the built-in default) |
| `stop_chat` | `{sessionId?,cwd?}` | `bool` |
| `list_skills` | `{cwd?}` | `SkillEntry[]` (name, description, source, path; content only at turn time) |
| `answer_ask` | `{sessionId,callId,answers:[{question,answer}]}` | `bool` (false: nothing waiting) |
| `cancel_ask` | `{sessionId,callId}` | `bool` |
| `chat` | `{sessionId?,message,cwd,provider?,model?,systemPrompt?,resume?,skill?,display?}` | streaming (below); answered when the turn ends |
| `submit_chat` | same as `chat` | `TurnInfo` at once (`state` `running` or `queued`); progress arrives as events |
| `turns` | `{}` | `TurnInfo[]`: every running and queued turn, all sessions |
| `resources` | `{}` | `{at, supported, self:{cpu,rss,procs}, sessions:{[id]:{cpu,rss,procs}}}`: the core process plus each session's running shell process trees (one `ps` sample, cached 1.5s; `cpu` is % of one core, `rss` bytes). `supported:false` off unix |
| `attach` | `{sessionId,cwd?}` | `{history: HistoryMessage[], turn: TurnInfo\|null, events: Event[], queued: TurnInfo[], seq}` |
| `cancel_turn` | `{turnId}` | `bool`: drops a queued turn (or stops it if already running) |

### Turns run in the background

Every turn runs in the sidecar's turn hub, independent of any client
request: different sessions run concurrently (at most
`max_concurrent_turns` from `config/settings.json`, default 4, max 16),
and a message sent to a session that is already busy queues behind it
(FIFO). Extra turns beyond the cap wait in the same queue. `stop_chat`
stops the running turn **and** drops the session's queued turns;
`delete_session` on a busy session stops it and waits for it to land
first. Closing stdin — or SIGTERM/SIGINT — stops every turn, persists
it, then exits.

`TurnInfo` is `{turnId, sessionId, cwd, message, display?, skill?,
resume?, state: queued|running|done, persisted?, reply?, stopped?,
needsContinue?, cancelled?, error?, queuedAt, startedAt?, endedAt?}`.

`attach` lets a client (re)join a session at any time — after a
renderer reload, or when switching to a session that is working in the
background. It returns the persisted history and the live turn's
events so far (consecutive deltas merged, each tool reduced to its
latest state), snapshotted atomically with `seq`. Apply the snapshot,
then apply live events whose `seq` is greater. `turn.persisted: true`
means the transcript already holds the turn, so `events` is empty.

The prompt file is authoritative: every turn uses it unless the
`chat` call carries an explicit `systemPrompt` override. Tool
guidance and examples are compiled per-OS in the core (a Windows turn
never sees sh text, a Unix turn never sees PowerShell text) and always
appended with the machine (OS + arch) and session workspace
environment block. The settings UI edits the file via
`get/set/reset_prompt`.

## Responses (stdout)

Unary:

```json
{"id": 1, "ok": true, "result": [...]}
{"id": 1, "ok": false, "error": "..."}
```

Events are broadcast for every turn, whoever submitted it. Each carries
`event`, a global strictly increasing `seq`, `sessionId`, `turnId`, and
`streamId` (the `chat` request id; absent for `submit_chat`):

```json
{"event": "turn/queued", "seq": 4, "sessionId": "...", "turnId": "t1-3", "turn": {...TurnInfo}}
{"event": "turn/start",  "seq": 9, "sessionId": "...", "turnId": "t1-3", "turn": {...TurnInfo}}
{"event": "turn/done",   "seq": 31, "sessionId": "...", "turnId": "t1-3", "turn": {...TurnInfo, "state": "done"}}
```

`turn/done` fires for finished, stopped, failed (`error`) and dropped
(`cancelled`) turns; the transcript is already written when it arrives.

Streaming (`chat` with `id = streamId`):

```json
{"event": "chat/delta", "streamId": "s2", "sessionId": "...", "delta": "..."}
{"event": "chat/tool", "streamId": "s2", "sessionId": "...", "tool": {...}}
{"id": "s2", "ok": true, "result": {"sessionId": "...", "reply": "...", "stopped": false, "needsContinue": false}}
```

More streaming events:

```json
{"event": "chat/retry", "streamId": "s2", "sessionId": "...", "retry": {"attempt": 1, "max": 6, "delayMs": 2000, "reason": "status 503: overloaded"}}
{"event": "chat/compact", "streamId": "s2", "sessionId": "...", "compaction": {"phase": "start", "reason": "auto", "tokensBefore": 170000}}
{"event": "chat/compact", "streamId": "s2", "sessionId": "...", "compaction": {"phase": "done", "reason": "auto", "tokensBefore": 170000, "tokensAfter": 30000}}
```

- `chat/retry`: a round failed with a transient provider error (429,
  5xx, timeout, stalled stream) before producing output and runs again
  after `delayMs` (exponential backoff, or the server's Retry-After).
  Retries don't consume rounds; any progress resets the budget.
- `chat/compact`: context compaction. `reason` is `auto` (the next
  request would fill 85% of a known context window) or `overflow` (the
  provider rejected the prompt as too long). `phase` is `start`, then
  `done` or `failed` (with `error`). The older conversation is replaced
  by a model-written summary; the transcript keeps everything, and
  `session_history` shows a `{"role": "system", "kind": "compaction",
  "content": "<summary>"}` divider where it happened.
- Tool calls cut short by stop or a crash settle as `failed` `chat/tool`
  events with an `aborted` result, so no row is left spinning.

Blocking tools: `ask` registers a pending call and blocks the turn
until the UI resolves it. A running `ask` tool event opens the
question wizard (one question at a time, arrows move, Enter advances,
review screen, Submit sends `answer_ask`); Esc sends `cancel_ask` and
stops the turn. Settled `ask` events close the wizard. Answers arrive
as the tool result (`{"answers":[{"question","answer"}]}`) and the
turn continues on its own.

Skills: the composer attaches a skill by name (`/skill <name>` picker
or hand-typed line) and the `chat` call carries it separately in
`skill`. The sidecar loads `<name>/SKILL.md` (workspace
`.paladin/skills/` shadows global `config/skills/`) and injects its
content into the model-facing user message before the LLM call — the
composer and picker never carry the content. Unknown or oversize
skills fail the turn with a `not found` / `exceeds` error; `list_skills`
never returns content.

## Electron side

- `main.ts` keeps a `pending` map `id -> {resolve, reject}`.
- Unary `agent:invoke` resolves on the matching `{id}` line.
- Every `chat/*` and `turn/*` event is forwarded to the renderer on
  `agent:event`; the renderer routes it by `sessionId`, so any number
  of sessions stream at once and a session keeps updating while it is
  not shown. The renderer submits with `submit_chat` and re-joins
  sessions with `attach`.
- The sidecar outlives the window: on macOS closing the window keeps
  the app (and running turns) alive in the dock, and reopening
  re-attaches. When the window is unfocused or closed, `turn/done` and
  a running `ask` raise a native notification; clicking it opens that
  session (`app:open-session`, or `app:takePendingOpen` after a fresh
  window boots).
- Quitting closes the sidecar's stdin and waits up to 8s for it to
  persist and exit before killing it.
- WebContentsViews (`views:*` IPC) are independent of the agent: one
  shared persistent partition (`persist:paladin-sites`) across chat
  pages so Google SSO cookies carry over, a single live guest (showing
  a page destroys the previous view; reloads stay logged in), views
  hidden — never destroyed — on Agent switch, 60s idle drop while hidden.
- The window is frameless; the renderer owns the title bar and sends
  `window:*` for minimize/maximize/close. `views:setTheme` mirrors the
  shell theme onto each guest view's background so the native surface
  never flashes white before the page paints.
