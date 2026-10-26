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
| `open_session` | `{sessionId?,cwd?}` | `Session` |
| `list_sessions` | `{cwd?,limit?}` | `SessionInfo[]` |
| `session_history` | `{sessionId,cwd?}` | `HistoryMessage[]` |
| `delete_session` | `{sessionId,cwd?}` | `bool` |
| `stop_chat` | `{sessionId?,cwd?}` | `bool` |
| `chat` | `{sessionId?,message,cwd,provider?,model?,systemPrompt?,resume?}` | streaming (below) |

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

## Electron side

- `main.ts` keeps a `pending` map `id -> {resolve, reject}`.
- Unary `agent:invoke` resolves on the matching `{id}` line.
- `agent:chat` returns the `streamId` immediately; deltas forward via
  `agent:chat-event`, completion via `agent:chat-done` to the sender.
- WebContentsViews (`views:*` IPC) are independent of the agent: one
  persistent partition per page (`persist:paladin-gemini`,
  `persist:paladin-chatgpt`), views hidden — never destroyed — on Agent
  switch, 60s idle drop only for built-ins.
