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
| `revertSession` | `{sessionId,cwd?,index}` | `{index,message,restored[],skipped[],snapshots}`: undo from that user message on (files + conversation); `message` is its text for the composer. Fails while a turn runs |
| `unrevertSession` | `{sessionId,cwd?}` | same shape: redo the pending revert |
| `gitInfo` | `{cwd}` | `{repo,root?,branch?,hasCommits}` |
| `worktreeStatus` | `{sessionId,cwd?}` | `{exists,changed,ahead,branch,baseBranch?,into,path,repoRoot}`: `changed` files in the worktree, commits `ahead` of the repo's checkout (`into`) |
| `worktreeMerge` | `{sessionId,cwd?,message?}` | `{merged,nothingToMerge,commit?,conflicts[],into?}`: commits the worktree's changes (message defaults to the session title) and merges its branch into the repo's current checkout with `--no-ff`; conflicts abort the merge and are listed. Fails if the checkout has uncommitted tracked changes |
| `renameSession` | `{sessionId,cwd?,title}` | `bool`; blank title restores the derived one (max 120 chars) |
| `answerAsk` | `{sessionId,callId,answers:[{question,answer,selected?}]}` | `bool` (false: nothing waiting) |
| `cancelAsk` | `{sessionId,callId}` | `bool` |
| `answerApproval` | `{sessionId,callId,decision}` | `bool`; `decision` is `once`, `session`, `folder` or `deny` (must be one of the card's `choices`); unknown calls fail |
| `permissionsGet` | `{cwd?,sessionId?}` | `{global: Rule[], folder: Rule[], session: Rule[], folders: [{cwd,rules}], sandbox: {kind}, presets}`; `sandbox.kind` is `seatbelt`, `landlock` or `""` (approvals only) |
| `permissionsSet` | `{scope,cwd?,rules}` | same view; `scope` is `global` or `folder` (session rules come only from approvals). `Rule` is `{tool: shell\|write, pattern, action: allow\|deny}`: shell patterns are word prefixes (`npm test`), write patterns absolute directories, `*` matches all |
| `submitChat` | `{sessionId?,message,cwd,provider?,model?,systemPrompt?,resume?,skill?,display?,command?,mode?,compact?,permissions?}` | `TurnInfo` at once (`state` `running` or `queued`); progress arrives as events (below). `permissions` is `read-only`, `auto` (default) or `full-access` (see Approvals); `command` runs a prompt command with `message` as its arguments; `mode` is `build` (default) or `plan` (read-only tools); `compact:true` runs a manual compaction instead of a model turn (no message needed) |
| `turns` | `{}` | `TurnInfo[]`: every running and queued turn, all sessions |
| `resources` | `{}` | `{at, supported, self:{cpu,rss,procs}, sessions:{[id]:{cpu,rss,procs}}}`: the core process plus each session's running shell process trees (one `ps` sample, cached 1.5s; `cpu` is % of one core, `rss` bytes). `supported:false` off unix |
| `attach` | `{sessionId,cwd?}` | `{history: HistoryMessage[], turn: TurnInfo\|null, events: Event[], queued: TurnInfo[], seq}` |
| `cancelTurn` | `{turnId}` | `bool`: drops a queued turn (or stops it if already running) |

### Turns run in the background

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
stopped?, needsContinue?, cancelled?, awaitingApproval?, error?,
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
  Retries don't consume rounds; any progress resets the budget.
- `chat/compact`: context compaction. `reason` is `auto` (the next
  request would fill 85% of a known context window) or `overflow` (the
  provider rejected the prompt as too long). `phase` is `start`, then
  `done` or `failed` (with `error`). The older conversation is replaced
  by a model-written summary; the transcript keeps everything, and
  `sessionHistory` shows a `{"role": "system", "kind": "compaction",
  "content": "<summary>"}` divider where it happened.
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

Every `shell`, `write` and `edit` call passes the permission gate
before it runs. Presets:

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
`approved` or `denied` (`decision: "interrupted"` if the turn stopped). `session`
and `folder` save `remember`. A denied call returns an error result
telling the model not to retry. If a sandboxed command fails because
the sandbox blocked it, the gate sends a `kind: "escalate"` card
(`once`/`deny`): approving reruns the command unsandboxed. `attach`
replays each approval at its latest state. The sandbox is Seatbelt
(`sandbox-exec`) on macOS and Landlock on Linux; on Windows
`sandbox.kind` is `""` and only approvals apply.

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
revert: the hidden messages and their steps are dropped. Changes made by
background jobs after their call returned are not tracked.

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

- `main.ts` keeps a `pending` map `id -> {resolve, reject}`.
- Unary `agent:invoke` resolves on the matching `{id}` line.
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
- WebContentsViews (`views:*` IPC) are independent of the agent: one
  shared persistent partition (`persist:paladin-sites`) across chat
  pages so Google SSO cookies carry over, a single live guest (showing
  a page destroys the previous view; reloads stay logged in), views
  hidden — never destroyed — on Agent switch, 60s idle drop while hidden.
- The window is frameless; the renderer owns the title bar and sends
  `window:*` for minimize/maximize/close. `views:setTheme` mirrors the
  shell theme onto each guest view's background so the native surface
  never flashes white before the page paints.
