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
| `refresh_providers` | `{}` | `bool` |
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
| `answer_ask` | `{sessionId,callId,answers:[{question,answer}]}` | `bool` (false: nothing waiting) |
| `cancel_ask` | `{sessionId,callId}` | `bool` |
| `chat` | `{sessionId?,message,cwd,provider?,model?,systemPrompt?,resume?}` | streaming (below) |

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

Streaming (`chat` with `id = streamId`):

```json
{"event": "chat/delta", "streamId": "s2", "sessionId": "...", "delta": "..."}
{"event": "chat/tool", "streamId": "s2", "sessionId": "...", "tool": {...}}
{"id": "s2", "ok": true, "result": {"sessionId": "...", "reply": "...", "stopped": false, "needsContinue": false}}
```

Blocking tools: `ask` registers a pending call and blocks the turn
until the UI resolves it. A running `ask` tool event opens the
question wizard (one question at a time, arrows move, Enter advances,
review screen, Submit sends `answer_ask`); Esc sends `cancel_ask` and
stops the turn. Settled `ask` events close the wizard. Answers arrive
as the tool result (`{"answers":[{"question","answer"}]}`) and the
turn continues on its own.

## Electron side

- `main.ts` keeps a `pending` map `id -> {resolve, reject}`.
- Unary `agent:invoke` resolves on the matching `{id}` line.
- `agent:chat` returns the `streamId` immediately; deltas forward via
  `agent:chat-event`, completion via `agent:chat-done` to the sender.
- WebContentsViews (`views:*` IPC) are independent of the agent: one
  shared persistent partition (`persist:paladin-sites`) across chat
  pages so Google SSO cookies carry over, a single live guest (showing
  a page destroys the previous view; reloads stay logged in), views
  hidden — never destroyed — on Agent switch, 60s idle drop while hidden.
- The window is frameless; the renderer owns the title bar and sends
  `window:*` for minimize/maximize/close. `views:setTheme` mirrors the
  shell theme onto each guest view's background so the native surface
  never flashes white before the page paints.
