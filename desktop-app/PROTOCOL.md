# Paladin JSONL protocol (Electron main <-> agent-host sidecar)

`agent-host` is a Go executable built from `core/cmd/agent-host`.
Electron main spawns it with piped stdio:

- stdin: requests, one JSON object per line
- stdout: responses + stream events, one JSON object per line
- stderr: logs (never JSONL)

## Requests (stdin)

```json
{"id": 1, "method": "listProviders", "params": {}}
{"id": "s2", "method": "submitChat", "params": {
  "sessionId": "", "message": "hi", "cwd": "/tmp/proj",
  "provider": null, "model": null, "systemPrompt": null, "resume": false}}
```

| method | params | result |
|---|---|---|
| `ping` | `{}` | `{"ok":true}` |
| `listProviders` | `{}` | `ProviderInfo[]` |
| `refreshProviders` | `{name?}` | no name: `bool` (background refresh-all started); `{name}`: `RefreshResult` (`name,count,models,error?`) for that provider only. Catalogs are never fetched on startup or on add/update — only on this explicit call |
| `setDefaultProvider` | `{name}` | `ProviderInfo[]` |
| `addProvider` | `{name,apiUrl,apiKey,apiType}` | `ProviderInfo` |
| `updateProvider` | `{name,apiUrl,apiKey,apiType}` | `ProviderInfo` |
| `getWorkspace` | `{}` | `string` |
| `setWorkspace` | `{path}` | `string` |
| `resolveWorkspace` | `{cwd}` | `string` |
| `fsHome` | `{}` | `string` |
| `fsList` | `{path?}` | `FsList` |
| `fileComplete` | `{query,cwd?,limit?}` | `FileEntry[]` (`path` chat path, `resolvedPath` OS-absolute path to insert, `outside`, `isDir`; gitignored paths never suggested; `../` completes from the workspace parent) |
| `openSession` | `{sessionId?,cwd?,worktree?,name?}` | `Session` (`sessionId,cwd,provider,model,revert?,worktree?`); `worktree:true` creates the session in a new git worktree of `cwd`'s repo on branch `paladin/<name>` (see Undo and worktrees) |
| `listSessions` | `{cwd?,limit?}` | `SessionInfo[]` |
| `sessionHistory` | `{sessionId,cwd?}` | `HistoryMessage[]` |
| `sessionTodos` | `{sessionId,cwd?}` | `TodoEntry[]` (`content,status`): the agent's todo list for the panel above the composer; empty before it plans |
| `sessionUsage` | `{sessionId,cwd?}` | `SessionUsage` (`sessionId,model,contextWindow,contextKnown,calls,promptTokens,completionTokens,reasoningTokens,cacheReadTokens,cacheWriteTokens,totalTokens,lastInputTokens,cost,listCost,hasCost,fillPercent?` + per-model `models[]` + per-day `daily[]`; `fillPercent` is the last call's input tokens over the window, omitted when the model's window is unknown) |
| `budgetGet` | `{sessionId?,cwd?}` | `BudgetView` (`defaultUsd,sessionUsd,effectiveUsd,spentUsd,hasCost,calls`); `calls` counts model calls that reported usage, so `hasCost:false` with `calls>0` means the provider sends no cost; `sessionUsd` is `null` when the session inherits the default; `effectiveUsd` `0` means no limit. Without `sessionId` only the default is reported |
| `budgetSet` | `{scope,sessionId?,cwd?,budgetUsd}` | `BudgetView`; `scope` `global` (default in `config/settings.json` as `defaultBudgetUsd`) or `session` (session meta); `budgetUsd` a non-negative USD amount, `0` for a session turns the limit off, `null` clears it (inherit) |
| `thinkingGet` | `{provider?,model?}` | `ThinkingView` (`provider,model,levels,level`); `levels` are the thinking (reasoning effort) levels the model accepts, weakest first, from `none,minimal,low,medium,high,xhigh,max` — empty when the model has no adjustable thinking; `level` `""` means the provider default (nothing is sent). Empty `provider`/`model` resolve to the default ones |
| `thinkingSet` | `{provider,model,level}` | `ThinkingView`; stores the level app-wide per model in `config/settings.json` as `thinkingLevels: {provider: {model: level}}`; `""` clears it. A level the model doesn't list is rejected. Applies from the next model call |
| `overallUsage` | `{cwd?,limit?}` | `OverallUsage` (fleet totals + `models[]` heaviest-first + `sessions[]` heaviest-first capped at `limit` + `daily[]` oldest-first) |
| `deleteSession` | `{sessionId,cwd?,discardWorktree?}` | `bool`; a worktree session's worktree is removed with its last session if clean, or always with `discardWorktree:true` |
| `getPrompt` | `{}` | `Prompt` (`systemPrompt` = `config/SYSTEM_PROMPT.md`; a missing/blank file seeds from the built-in default) |
| `setPrompt` | `{systemPrompt}` | `Prompt` (blank rejected) |
| `resetPrompt` | `{}` | `Prompt` (file rewritten from the built-in default) |
| `stopChat` | `{sessionId?,cwd?}` | `bool` |
| `listSkills` | `{cwd?}` | `SkillEntry[]` (name, description, source, path; content only at turn time) |
| `listCommands` | `{cwd?}` | `CommandEntry[]` (`name,description,argumentHint?,mode?,source` builtin/global/workspace, `path?`) |
| `memoryGet` | `{target,cwd?}` | `MemoryView` (`target` user/global/project, `cwd?`, `path`, `entries[]`, `used`, `limit` chars); `project` requires `cwd` |
| `memorySet` | `{target,cwd?,entries[]}` | `MemoryView`; replaces the store (dedupes, enforces the limit and the threat scan) |
| `memoryProjects` | `{}` | `Project[]` (`cwd,key,entries,used,limit`): folders that have memory |
| `memoryRefreshSnapshot` | `{sessionId,cwd?}` | `bool`: the session's next turn re-reads memory instead of its frozen snapshot |
| `forkSession` | `{sessionId,cwd?,worktree?,name?}` | `SessionInfo` of the copy (title `… (fork)`); `worktree:true` forks into a new worktree, a plain fork stays in the source's folder (and worktree) |
| `revertPreview` | `{sessionId,cwd?,index}` | `{index,turns,files[],conflicts[],snapshots}`: what reverting to the user message at transcript `index` would restore |
| `revertSession` | `{sessionId,cwd?,index}` | `{index,message,restored[],skipped[],failed?[],snapshots}`: undo from that user message on (files + conversation); `message` is its text for the composer. `failed` lists files whose restore errored (also in `skipped`; the others were still restored). Fails while a turn runs |
| `unrevertSession` | `{sessionId,cwd?}` | same shape: redo the pending revert |
| `gitInfo` | `{cwd}` | `{repo,root?,branch?,hasCommits}` |
| `worktreeStatus` | `{sessionId,cwd?}` | `{exists,changed,ahead,branch,baseBranch?,into,path,repoRoot}`: `changed` files in the worktree, commits `ahead` of the repo's checkout (`into`) |
| `worktreeMerge` | `{sessionId,cwd?,message?}` | `{merged,nothingToMerge,commit?,conflicts[],into?}`: commits the worktree's changes (message defaults to the session title) and merges its branch into the repo's current checkout with `--no-ff`; conflicts abort the merge and are listed. Fails if the checkout has uncommitted tracked changes |
| `renameSession` | `{sessionId,cwd?,title}` | `bool`; blank title restores the derived one (max 120 chars) |
| `answerAsk` | `{sessionId,callId,answers:[{question,answer,selected?}]}` | `bool` (false: nothing waiting) |
| `cancelAsk` | `{sessionId,callId}` | `bool` |
| `answerApproval` | `{sessionId,callId,decision}` | `bool`; `decision` is `once`, `session`, `folder`, `global` or `deny` (must be one of the card's `choices`); unknown calls fail |
| `permissionsGet` | `{cwd?,sessionId?}` | `{global: Rule[], folder: Rule[], session: Rule[], folders: [{cwd,rules}], sandbox: {kind}, presets}`; `sandbox.kind` is `seatbelt`, `landlock` or `""` (approvals only) |
| `permissionsSet` | `{scope,cwd?,rules}` | same view; `scope` is `global` or `folder` (session rules come only from approvals). `Rule` is `{tool: shell\|write\|read\|mcp, pattern, action: allow\|deny}`: shell patterns are word prefixes (`npm test`), write and read patterns absolute directories (an allow-write rule also allows reading there), mcp patterns `server`, `server/*` or `server/tool`; `*` matches all |
| `mcpList` | `{cwd?}` | `MCPOverview`: `{servers: ServerStatus[], globalPath, folderPath?, hasFolderConfig, trusted, errors?: {global?, folder?}}` — global servers plus `cwd`'s own (see MCP servers) |
| `mcpConfig` | `{scope,cwd?}` | `{path, text, exists}`: one `mcp.json` for editing (`scope` `global` or `folder`; a missing file returns a starter `text`) |
| `mcpSetConfig` | `{scope,cwd?,text}` | `MCPOverview`; validates, writes and applies the file. A folder file saved here is trusted |
| `mcpSetEnabled` | `{scope,cwd?,name,enabled}` | `MCPOverview`; sets the server's `enabled` flag in its file (a trusted folder file stays trusted) |
| `mcpRestart` | `{scope,cwd?,name}` | `MCPOverview`; stops the server, clears its failure and starts it |
| `mcpTrust` | `{cwd,trust}` | `MCPOverview`; approves (or revokes) the folder's current `mcp.json` |
| `mcpParse` | `{text}` | `{servers: {name, config}[]}`: servers read from pasted text — launch commands (`npx`/`uvx`/`docker run`/…, `KEY=value` prefixes, one per line), a Streamable HTTP URL, `claude`/`codex mcp add …` lines or `mcp.json` JSON. Nothing is saved |
| `mcpAdd` | `{scope,cwd?,servers,replace?}` | `MCPOverview`; adds `{name, config}[]` to one `mcp.json` (existing names fail unless `replace`) and starts them. A new or trusted folder file is trusted afterwards; an untrusted one stays untrusted |
| `sideQuestion` | `{sessionId,cwd?,question,provider?,model?}` | `{answer, model}`: a `/btw` answer about the session — the running turn's conversation as of its latest model call when one is live, else the saved history — with no tools. Nothing is added to the transcript; the call is billed to the session's usage (and budget). Answered off the request loop, so its response can arrive after later requests' |
| `hooksList` | `{cwd?}` | `HooksOverview`: `{sources: Source[], events}` — the global `config/hooks.json` plus, with `cwd`, the folder's `.paladin/hooks.json`, `.codex/hooks.json` and `.claude/settings.json` (see Hooks). `Source` is `{path, scope, format: paladin\|codex\|claude, exists, editable, trusted, error?, warnings?, text?, handlers: [{key, event, matcher?, command, timeout?, statusMessage?, enabled}]}` |
| `hooksSetConfig` | `{scope,cwd?,text}` | `HooksOverview`; validates and writes the global or folder `hooks.json` (blank `text` deletes it). A folder file saved here is trusted |
| `hooksTrust` | `{cwd,path,trust}` | `HooksOverview`; approves (or revokes) one of the folder's hooks files at its current content |
| `hooksToggle` | `{cwd?,key,enabled}` | `HooksOverview`; turns one handler on or off |
| `toolsList` | `{cwd?,sessionId?}` | `ToolsOverview`: `{tools: ToolInfo[], pending: string[]}` (see Tool switches) |
| `toolsSet` | `{scope,cwd?,sessionId?,names,enabled}` | `ToolsOverview`; `scope` `global` or `session`; `enabled` `true`/`false`, or `null` to clear the switch |
| `toolsAccept` | `{cwd?,sessionId,names?}` | `ToolsOverview`; the session uses these pending tools (all when `names` is empty) from its next turn |
| `submitChat` | `{sessionId?,message,cwd,provider?,model?,systemPrompt?,resume?,skill?,display?,command?,mode?,compact?,permissions?}` | `TurnInfo` at once (`state` `running` or `queued`); progress arrives as events (below). `permissions` is `read-only`, `auto` (default) or `full-access` (see Approvals); `command` runs a prompt command with `message` as its arguments; `mode` is `build` (default) or `plan` (read-only tools); `compact:true` runs a manual compaction instead of a model turn (no message needed) |
| `steerChat` | `{sessionId,message,display?}` | `TurnInfo` of the running turn; fails when the session has nothing running (submit instead). The message joins that turn before its next model call (see Steering) |
| `turns` | `{}` | `TurnInfo[]`: every running and queued turn, all sessions |
| `resources` | `{}` | `{at, supported, self:{cpu,rss,procs}, sessions:{[id]:{cpu,rss,procs}}}`: the core process plus each session's running shell process trees (one `ps` sample, cached 1.5s; `cpu` is % of one core, `rss` bytes). `supported:false` off unix |
| `attach` | `{sessionId,cwd?}` | `{history: HistoryMessage[], turn: TurnInfo\|null, events: Event[], queued: TurnInfo[], seq}` |
| `cancelTurn` | `{turnId}` | `bool`: drops a queued turn (or stops it if already running) |

### Turns run in the background

A turn has **no round cap**: it runs until the model replies without
calling a tool, the user stops it, or an error ends it. Guards keep it
from spinning: a call repeated three times in a row — or a 2–4 call
cycle repeated three times — is refused with guidance (the turn goes
on), and compaction gives up after three unproductive attempts.

**Steering:** `steerChat` sends a message into the running turn. It is
added as a user message before the next model call — after the current
tool batch finishes, or right after a final answer, which then keeps the
turn going — and echoed as `chat/steer`. A steer the turn never picked
up (it was already ending) becomes the session's next turn, ahead of
anything queued later; stopping the turn drops it.

**Budget:** an optional tracked-cost limit (`budgetSet`) is checked
before every model call against the session's gateway-reported spend. It
is not a guaranteed spending cap: calls without a reported cost are not
counted, and the call that crosses the limit plus the wrap-up summary
still run, so spend can end above it. Once spent ≥
limit, the turn emits `chat/budget` `reached`, the tools are taken away
and the model is asked for a final done/remaining summary, which ends the
turn (steers wait for a later turn). A turn that starts already over the
limit ends at once without a model call. When a budget is set but the
provider reports no cost, `chat/budget` `unknownCost` warns once per turn
and the limit is not enforced. `usage.json` is written atomically (temp
file + fsync + rename). A ledger that does not decode never counts as
zero spend: the next call renames it to `usage.json.corrupt-<n>` and
starts a new one with `recoveredFrom` naming it, and while the ledger is
corrupt or recovered `chat/budget` `spendIncomplete` warns once per turn
that `spent` is only a lower bound (the limit is still checked against
it, so real spend may already be past the limit).

**Hooks:** user commands run (outside the sandbox, via `sh -c`) at
`SessionStart` (first turn of a session in this process: `startup`,
`resume`, or `compact` after a summary), `UserPromptSubmit`,
`PreToolUse`, `PostToolUse`, `Stop`, `PreCompact` and `PostCompact`.
Each gets a snake_case JSON object on stdin (`session_id`,
`transcript_path`, `cwd`, `hook_event_name`, `permission_mode`, plus
`prompt`, `tool_name`/`tool_input`/`tool_response`, `stop_hook_active`,
`trigger`, `source` as fitting) — the same contract as Codex and Claude
Code. Exit 2 blocks (stderr is the reason) for events that can block;
stdout JSON may set `continue: false`, `systemMessage`,
`decision: "block"` + `reason`, or `hookSpecificOutput` with
`additionalContext`, `permissionDecision` (`allow` skips the approval
prompt but never the sandbox or deny rules; `ask` forces one; `deny`
refuses) and `updatedInput`. A blocking `Stop` hook continues the turn
with its reason (at most 8 times per turn). Folder files only run once
trusted; every run emits `chat/hook`.

Every turn runs in the sidecar's turn hub, independent of any client
request: different sessions run concurrently (at most
`maxConcurrentTurns` from `config/settings.json`, default 4, max 16),
and a message sent to a session that is already busy queues behind it
(FIFO). Extra turns beyond the cap wait in the same queue. `stopChat`
stops the running turn **and** drops the session's queued turns;
`deleteSession` on a busy session stops it and waits for it to land
first. Closing stdin — or SIGTERM/SIGINT — stops every turn, persists
it, then exits.

`TurnInfo` is `{turnId, sessionId, cwd, message, display?, skill?,
resume?, permissions?, state: queued|running|done, persisted?, reply?,
stopped?, cancelled?, awaitingApproval?, error?,
queuedAt, startedAt?, endedAt?}`. `awaitingApproval` is true while a
tool call waits on an approval card.

`attach` lets a client (re)join a session at any time — after a
renderer reload, or when switching to a session that is working in the
background. It returns the persisted history and the live turn's
events so far (consecutive deltas merged, each tool reduced to its
latest state), snapshotted atomically with `seq`. Apply the snapshot,
then apply live events whose `seq` is greater. `turn.persisted: true`
means the transcript already holds the turn, so `events` is empty.

The prompt file is authoritative: every turn uses it unless the
`submitChat` call carries an explicit `systemPrompt` override. Tool
guidance and examples are compiled per-OS in the core (a Windows turn
never sees sh text, a Unix turn never sees PowerShell text) and always
appended with the machine (OS + arch) and session workspace
environment block. The settings UI edits the file via
`getPrompt`/`setPrompt`/`resetPrompt`.

## Responses (stdout)

Unary:

```json
{"id": 1, "ok": true, "result": [...]}
{"id": 1, "ok": false, "error": "..."}
```

Events are broadcast for every turn, whoever submitted it. Each carries
`event`, a global strictly increasing `seq`, `sessionId` and `turnId`:

```json
{"event": "turn/queued", "seq": 4, "sessionId": "...", "turnId": "t1-3", "turn": {...TurnInfo}}
{"event": "turn/start",  "seq": 9, "sessionId": "...", "turnId": "t1-3", "turn": {...TurnInfo}}
{"event": "turn/done",   "seq": 31, "sessionId": "...", "turnId": "t1-3", "turn": {...TurnInfo, "state": "done"}}
```

`turn/done` fires for finished, stopped, failed (`error`) and dropped
(`cancelled`) turns; the transcript is already written when it arrives.

Streaming (between `turn/start` and `turn/done`):

```json
{"event": "chat/delta", "seq": 10, "sessionId": "...", "turnId": "t1-3", "delta": "..."}
{"event": "chat/tool", "seq": 11, "sessionId": "...", "turnId": "t1-3", "tool": {...}}
```

More streaming events:

```json
{"event": "chat/retry", "sessionId": "...", "retry": {"attempt": 1, "max": 6, "delayMs": 2000, "reason": "status 503: overloaded"}}
{"event": "chat/compact", "sessionId": "...", "compaction": {"phase": "start", "reason": "auto", "tokensBefore": 170000}}
{"event": "chat/compact", "sessionId": "...", "compaction": {"phase": "done", "reason": "auto", "tokensBefore": 170000, "tokensAfter": 30000}}
```

- `chat/retry`: a round failed with a transient provider error (429,
  5xx, timeout, stalled stream) before producing output and runs again
  after `delayMs` (exponential backoff, or the server's Retry-After).
  Any progress resets the retry budget.
- `chat/compact`: context compaction. `reason` is `auto` (the next
  request would fill 85% of a known context window) or `overflow` (the
  provider rejected the prompt as too long). `phase` is `start`, then
  `done` or `failed` (with `error`). The older conversation is replaced
  by a model-written summary; the transcript keeps everything, and
  `sessionHistory` shows a `{"role": "system", "kind": "compaction",
  "content": "<summary>"}` divider where it happened. A turn may
  compact any number of times; three compactions in a row that fail or
  leave the context over 85% stop compaction for the turn, and a later
  overflow then fails it.
- `chat/steer`: `{"steer": {"message", "display?"}}` — a message sent
  with `steerChat` joined the conversation here, as a user message.
- `chat/budget`: `{"budget": {"state", "spent", "limit", "ended?"}}` —
  `state` `reached` (the turn is wrapping up with a summary; with
  `ended: true` it was over the limit before any model call and ends at
  once), `unknownCost` (the provider reports no cost, so the limit
  cannot be enforced) or `spendIncomplete` (the usage ledger was
  corrupt; `spent` is a lower bound).
- `chat/reasoning`: `{"reasoning": "..."}` — a piece of the model's
  thinking, streamed apart from the answer. It is saved on the round's
  assistant message and `sessionHistory` shows it as a
  `{"role": "assistant", "kind": "thinking", "content": "..."}` row
  before that message. Thinking is never sent back to the model.
- `chat/thinkingDropped`: `{"thinkingDropped": "high"}` — the provider
  rejected the model's thinking level, so the call was retried without
  it. The level stays off for that model until it is changed or the
  provider is reconfigured.
- `chat/hook`: `{"hook": {"event", "command", "outcome", "message?"}}` — a hook ran and did something worth showing:
  `outcome` is `blocked`, `context`, `error`, `stopped` or `message`.
- Tool calls cut short by stop or a crash settle as `failed` `chat/tool`
  events with an `aborted` result, so no row is left spinning.

Blocking tools: `ask` registers a pending call and blocks the turn
until the UI resolves it. A running `ask` tool event opens the
question wizard (one question at a time, arrows move, Enter advances,
review screen, Submit sends `answerAsk`); Esc sends `cancelAsk` and
stops the turn. Settled `ask` events close the wizard. Answers arrive
as the tool result (`{"answers":[{"question","answer"}]}`) and the
turn continues on its own. A question with `multiple:true` takes any
number of options: the UI sends `selected:[…]` (and `answer` joined
with ", ").

### Approvals and sandbox

Every `shell`, `write` and `edit` call, every `read` outside the
workspace and every MCP tool call passes the permission gate before it
runs. Presets:

- `read-only`: safe read commands run in a read-only OS sandbox; any
  write or other command asks.
- `auto` (default): safe commands, and writes inside the workspace, run
  without asking. Other commands run once a rule matches or the user
  approves, inside a `workspace-write` sandbox (workspace, temp dirs and
  dev caches writable; network allowed). Writes outside the workspace ask.
- `full-access`: nothing asks and nothing is sandboxed.

Destructive commands (`rm -rf`, `sudo`, `git push --force`, `git reset
--hard`, …) always ask with only `once`/`deny`, except in full access.
A `deny` rule wins in every preset. Rules are checked session, then
folder (`permissions/folders/<key>.json`), then global
(`permissions/global.json`); session rules live in memory.

```json
{"event": "chat/approval", "sessionId": "...", "approval": {"id": "c1", "tool": "shell", "kind": "shell", "command": "npm install", "reasons": ["not on the safe list"], "destructive": false, "choices": ["once", "session", "folder", "deny"], "remember": [{"tool": "shell", "pattern": "npm install", "action": "allow"}], "state": "pending"}}
{"event": "chat/approval", "sessionId": "...", "approval": {"id": "c1", "state": "approved", "decision": "session", ...}}
```

The turn blocks until `answerApproval`; `state` then becomes
`approved` or `denied` (`decision: "interrupted"` if the turn stopped). `session`,
`folder` and `global` save `remember` in that scope; write and read
cards (outside the workspace) also offer `global`. A denied call returns
an error result telling the model not to retry.

**Reads outside the workspace.** `read` inside the workspace never
asks. A `read` whose path (after following symlinks) is outside it
sends a `kind: "read"` card with `path` and a `read` rule for the
file's folder in `remember` (the file itself when the folder is `/`, a
top-level folder, home or home's parent), unless a `read` or `write`
allow rule covers the real path; full access runs it. Read-only shell
commands (`cat`, `ls`, `grep`, …) that would run without asking are
checked too: an argument that is absolute, starts with `~`, contains
`..` or is an existing symlink leading out of the workspace makes the
call ask (`reasons` name each path, `remember` holds `read` rules); an
argument that expands a variable (`$HOME`) asks `once`/`deny`. This is
best effort: it looks only at arguments, so a program that opens other
paths itself, or a command already allowed by a `shell` rule, is not
checked. `read` deny rules block both. `/dev/null` and the std streams
are exempt.

**Plan mode and safe commands** never include environment or config
dumpers: `env`, `printenv`, `go env`, `git config`, `awk` using
`ENVIRON` and `jq` using `env`/`$ENV` need approval (and are refused by
the plan-mode shell).

**Redaction.** Shell output (including job polls and notices), tool
error results, provider errors and the sidecar log mask likely secrets
before they leave: known key prefixes (`sk-`, `ghp_`, `AKIA`, `AIza`,
`xox?-`, `npm_`, …), `Bearer` tokens, `scheme://user:pass@`, secret-named
`KEY=value` assignments, PEM private keys and every stored provider
key. It is pattern matching, so unusual secrets can slip through.
`read` returns file contents unmasked so edits still match.

**What the sandbox does not do.** The OS sandbox confines writes (and,
in read-only mode, all writes); it does not stop shell commands from
reading files the user can read, and network access stays on. Read
protection outside the workspace is the approval gate above, not the
sandbox. Hooks and `full-access` run unsandboxed.

Allow-write rules double as **sandbox grants**: every absolute folder
in an effective `write` allow rule is writable by sandboxed shell
commands too, in every preset (read-only included). `*` never lifts the
sandbox, and a grant overlapping a `write` deny rule is dropped.

If a sandboxed command fails because the sandbox blocked it, the gate
sends a `kind: "escalate"` card. When the output names the denied path
(and the command is not destructive) the card carries `path` (the
folder to grant: the path's parent, or the path itself when the parent
is `/`, a top-level folder, home or home's parent), a `write` rule in
`remember`, and choices `session`, `folder`, `global`, `once`, `deny`:
a scope saves the grant and reruns the command still sandboxed, `once`
reruns it unsandboxed. Without a path the choices are `once`/`deny`.
A rerun that hits the next folder asks again, up to 3 times per call.

```json
{"event": "chat/approval", "sessionId": "...", "approval": {"id": "c1", "tool": "shell", "kind": "escalate", "command": "npm i -g pnpm", "path": "/Users/me/.nvm/versions/node/v24/lib/node_modules", "reasons": ["blocked by the sandbox"], "choices": ["session", "folder", "global", "once", "deny"], "remember": [{"tool": "write", "pattern": "/Users/me/.nvm/versions/node/v24/lib/node_modules", "action": "allow"}], "state": "pending"}}
```

`attach`
replays each approval at its latest state. The sandbox is Seatbelt
(`sandbox-exec`) on macOS and Landlock on Linux; on Windows
`sandbox.kind` is `""` and only approvals apply.

### MCP servers

Servers come from `config/mcp.json` in the data dir (global) and
`<folder>/.paladin/mcp.json` (folder), both in the common shape:

```json
{"mcpServers": {"github": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"], "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
                "docs": {"type": "http", "url": "https://example.com/mcp", "headers": {"Authorization": "Bearer ${DOCS_TOKEN}"}}}}
```

Per server: `type` (`stdio`/`http`, inferred), `command`, `args`, `env`,
`cwd`, `url`, `headers`, `enabled`, `startupTimeoutSec` (30),
`toolTimeoutSec` (120), `allowedTools`, `deniedTools`. Strings expand
`${VAR}`, `${VAR:-default}` and `${workspaceFolder}`. Transports are
stdio and Streamable HTTP; legacy SSE is rejected.

Tools appear to the model as `mcp__<server>__<tool>`. A folder server
replaces a global one of the same name (`shadowed: true` on the
global). Folder servers run only once the user trusts that exact file
(`mcpTrust`); any outside edit needs trust again. Servers start lazily
on a turn's first use (global ones also at launch); a turn waits for
starting servers, never for failed ones. A server that fails to start
stays `failed` until its config changes or `mcpRestart`; one that dies
after starting restarts on next use (3 times per 5 minutes).

MCP calls pass the gate as `kind: "mcp"` cards with `server`, `mcpTool`
and `args`. Tool annotations come from the server and are not trusted:
`readOnlyHint` neither skips the question nor lets calls run in
parallel (MCP calls always run one at a time). Tools with
`destructiveHint` always ask `once`/`deny`; others run under an
`mcp` allow rule or ask and can remember `server/tool`. Full access
runs everything; deny rules win over everything. Plan mode keeps all
MCP tools but asks `once`/`deny` for every call, even under full
access, allow rules or a hook's allow. Refusals name `server/tool`.
Server instructions join the system prompt.

```json
{"event": "mcp/status", "server": {"name": "github", "scope": "global", "transport": "stdio", "state": "ready", "tools": [{"name": "search", "exposed": "mcp__github__search", "readOnly": true, "destructive": false}], "server": {"name": "github-mcp", "version": "1.0"}, "stderr": "...", "restarts": 0}}
```

`state` is `disabled`, `untrusted`, `stopped`, `starting`, `ready`,
`failed` (with `error`) or `removed`. The event has no `sessionId`.

### Tool switches

Every tool (built-in and MCP) is on unless switched off. A session's
switch beats the global one (`config/tools.json`); session switches and
the tools a session has seen live in its `meta.json` under `tools`.
`ToolInfo` is `{name, description?, source: builtin|mcp, server?, tool?,
readOnly?, destructive?, global?, session?, enabled, pending?}`; MCP
tools are listed while their server is ready. When built-in tools are
off, the system prompt says so.

A session's first turn takes every switched-on tool. After that, tools
it has not seen (a new MCP server, a server adding tools, a new
built-in) are held back, since changing the tool list throws away the
prompt cache: the turn runs without them and emits

```json
{"event": "tools/pending", "sessionId": "…", "pending": ["mcp__github__search"]}
```

`toolsAccept` (or switching the tool on for the session) lets the next
turn use them. Tools that disappear are simply dropped. New sessions
never have pending tools.

### Undo and worktrees

Every `shell`, `write` and `edit` run is bracketed by snapshots of the
session's folder in a shadow git repo (`snapshots/<folder>-<hash>/`,
never the project's own `.git`); the step (`{turn, callId, tool,
before, after, files}`) goes to the session's `steps.jsonl`. Snapshots
skip the project's ignored files, dependency/cache folders, binaries
and files over 10 MiB, and turn off for `/`, the home folder or folders
with more than 50,000 files; `PALADIN_SNAPSHOTS=off` disables them.
Without git, undo reverts only the conversation (`snapshots:false`).

`sessionHistory` user rows carry `index`, their transcript position.
`revertSession` restores each file the session changed from that
message on to its state before the first change, but only if it still
holds what the session last wrote: files changed since (by the user or
another session in the same folder) are `skipped`. The reverted
messages disappear from `sessionHistory`, and `openSession` /
`listSessions` report `revert`. `unrevertSession` puts the files back
(again skipping any changed since). The next message commits the
revert: the hidden messages and their steps are dropped.

Background shell jobs (`background: true`) time out after 300 s unless
`timeout_ms` says otherwise (foreground commands default to 10 s). They
are tracked separately: the
sidecar snapshots the folder when the job starts, keeps a record in
`<sessionDir>/jobs/<jobId>.json` (`status` running/done/failed/
cancelled/timeout, `startedAt`, `endedAt`, `exitCode`, `files`,
`uncertain`, `finalized`) and, exactly once when the job exits, is
cancelled or times out, snapshots again and records the changed files
as a step of the turn that started it. `revertSession` and
`unrevertSession` fail while one of the session's jobs is still
running. Files that another call (any session) also changed while the
job ran are `uncertain`: they are not in the step and a revert reports
them as `skipped` instead of restoring them. If two jobs overlap in one
folder, all their files are uncertain. Each record keeps the job's
process-group `pid`. A record left `running` by an earlier sidecar
process is checked on revert and redo: while that process group is
still alive both fail with an error naming the job and pid; once it is
gone, the revert marks the record `interrupted` (finalized, with a
`note`), and every file changed since the job started is `uncertain`
and reported as `skipped`, never restored. A recycled pid can cause a
false refusal; where process groups cannot be probed (Windows) the job
is treated as ended. `job_id`, polling, cancel and completion notices are
unchanged.

File-changing calls (write, edit, foreground shell) and reverts in one
folder run one at a time across all sessions of this sidecar, so each
step's before/after snapshots hold only that call's changes. That is
in-process only: an editor, a background job or another program writing
during a call is attributed to the call, and a long foreground shell
command makes other sessions' writes in that folder wait. Sessions that
need real isolation should use a worktree.

`write` with `overwrite` on an existing file needs this session to have
read the whole current version (line 1 to EOF, in one read or
consecutive pages of an unchanged file) or to have written it last;
otherwise it returns a conflict result (`conflict: true`, `reason`
`unread`, `stale` or `changed`) and writes nothing. A read that cut a
long line (`… [truncated]`) does not count: the conflict's `reason` is
`truncated` and the model is told to use `edit`, which never needs a
full read.

Worktrees live in `worktrees/<repo>-<hash>/<slug>` on branch
`paladin/<slug>`, created from the repo's HEAD commit (uncommitted
changes stay behind). The session's cwd is the matching subfolder. Its
system prompt says it works in a worktree, folder memory stays keyed on
the original folder, and sandboxed shell calls may also write the
repo's shared git dir so commits work.

Knowledge in the system prompt, in cache-stable order:
1. `AGENTS.md` instructions — global `config/AGENTS.md`, then each
   directory from the git root down to the session cwd (per directory
   the first of `AGENTS.override.md`, `AGENTS.md`, `CLAUDE.md`).
2. The skill catalog (names + descriptions); the model loads a body
   with the `skill` tool.
3. Memory, Hermes-style: `memory/USER.md`, `memory/MEMORY.md` and
   `memory/projects/<key>/MEMORY.md` for the folder, bounded `§`-joined
   entries written by the `memory` tool (file-locked, so sessions of the
   same folder never clobber each other). Each session freezes a
   snapshot at its first turn (`memory_snapshot.md`); writes land on
   disk at once and reach other sessions on their next snapshot.

Prompt commands: `.paladin/commands/<name>.md` (workspace) shadows
`config/commands/<name>.md` (global) shadows built-ins (`init`,
`review`). Front matter: `description`, `argument-hint`, `mode`.
The body expands `$ARGUMENTS` and `$1`..`$9`; history shows the
typed `/name args` (`display`) while the model sees the expansion.

Skills: the composer attaches a skill by name (`/skill <name>` picker
or hand-typed line) and the `submitChat` call carries it separately in
`skill`. The sidecar loads `<name>/SKILL.md` (workspace
`.paladin/skills/` shadows global `config/skills/`) and injects its
content into the model-facing user message before the LLM call — the
composer and picker never carry the content. Unknown or oversize
skills fail the turn with a `not found` / `exceeds` error; `listSkills`
never returns content.

## Electron side

- `sidecar.ts` owns the agent-host process and a `pending` map
  `id -> {resolve, reject}`; unary `agent:invoke` resolves on the
  matching `{id}` line.
- If the sidecar exits unexpectedly, in-flight calls reject with
  `agent-host exited`, calls made while it is down reject with
  `agent-host is not running`, and it is restarted with exponential
  backoff (0.5s doubling to 30s; reset after 60s of uptime). Once the
  new process is up, the renderer receives
  `{"event": "host/restarted"}` (no `sessionId`, sent by Electron, not
  the sidecar): it ends the turns that died with the old process and
  re-reads `turns`.
- Every `chat/*` and `turn/*` event is forwarded to the renderer on
  `agent:event`; the renderer routes it by `sessionId`, so any number
  of sessions stream at once and a session keeps updating while it is
  not shown. The renderer submits with `submitChat` and re-joins
  sessions with `attach`.
- The sidecar outlives the window: on macOS closing the window keeps
  the app (and running turns) alive in the dock, and reopening
  re-attaches. When the window is unfocused or closed, `turn/done` and
  a running `ask` raise a native notification; clicking it opens that
  session (`app:open-session`, or `app:takePendingOpen` after a fresh
  window boots).
- Quitting closes the sidecar's stdin and waits up to 8s for it to
  persist and exit before killing it.
- IPC inputs are validated in main: guest views and
  `shell:openExternal` accept only `http(s)` URLs (plus `mailto:` for
  openExternal), and `views:setBounds` needs finite numbers.
- WebContentsViews (`views:*` IPC) are independent of the agent: one
  shared persistent partition (`persist:paladin-sites`) across chat
  pages so Google SSO cookies carry over, a single live guest (showing
  a page destroys the previous view; reloads stay logged in), views
  hidden — never destroyed — on Agent switch, 60s idle drop while hidden.
- The window is frameless; the renderer owns the title bar and sends
  `window:*` for minimize/maximize/close. `views:setTheme` mirrors the
  shell theme onto each guest view's background so the native surface
  never flashes white before the page paints.
