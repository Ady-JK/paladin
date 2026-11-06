# Paladin JSONL protocol (Electron main <-> agent-host sidecar)

`agent-host` is a Go executable built from `core/cmd/agent-host`.
Electron main spawns it with piped stdio:

The same process can run headlessly from any JSONL client. In remote mode
Electron carries these streams over `ssh -T` to an `agent-host` process on the
remote machine; no protocol method or TCP listener changes. SSH uses batch
authentication and strict host-key checking. `agent-host extensions init` is a
separate setup command, not a JSONL method.

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
| `browserReply` | `{id,ok,result?,imageBase64?,error?}` | `bool`: resolves a pending `browser` tool call. The host emits `browser/request` with the same id; Electron runs Playwright and returns the result. Screenshot bytes are bounded and saved in the session before the model sees them. `open` takes local apps and http(s) sites (the gate asks per site first); a result may carry `blocked_navigation` when the page tried to leave the session's sites |
| `terminalStart` | `{sessionId,cwd,cols,rows}` | `terminalId`: starts an unsandboxed interactive PTY in the workspace; output arrives as `terminal/data` events with base64 `data` |
| `terminalWrite` | `{sessionId,terminalId,data}` | `bool`: sends UTF-8 or control characters to that PTY |
| `terminalResize` | `{sessionId,terminalId,cols,rows}` | `bool`: resizes that PTY |
| `terminalClose` | `{sessionId,terminalId}` | `bool`: stops that PTY |
| `terminalRun` | `{cwd,command}` | `{output,exitCode,truncated}`: runs a user-entered `!` or `!!` command outside the agent sandbox, in the workspace, with a 10-minute limit and 1 MiB output cap. It never writes a transcript entry by itself |
| `listProviders` | `{}` | `ProviderInfo[]` (`name,apiType,apiUrl,model,models,default,hasKey,meta?`); `meta` is each listed model's catalog metadata, published by providers that describe their models (OpenCode Go): `contextWindow,maxOutput,protocol,thinkingLevels,thinkingKnown,priceInput,priceOutput,priceCacheRead,priceCacheWrite`, with prices in USD per 1M tokens. Models the listing said nothing about are absent, so the UI falls back to its built-in tables |
| `refreshProviders` | `{name?}` | no name: `bool` (background refresh-all started); `{name}`: `RefreshResult` (`name,count,models,error?`) for that provider only. Catalogs are never fetched on startup or on add/update — only on this explicit call. A refresh also stores the listing's per-model metadata, which then drives that model's wire protocol, thinking levels and context window |
| `setDefaultProvider` | `{name}` | `ProviderInfo[]` |
| `addProvider` | `{name,apiUrl,apiKey,apiType}` | `ProviderInfo` |
| `updateProvider` | `{name,apiUrl,apiKey,apiType}` | `ProviderInfo` |
| `getWorkspace` | `{}` | `string` |
| `setWorkspace` | `{path}` | `string` |
| `resolveWorkspace` | `{cwd}` | `string` |
| `fsHome` | `{}` | `string` |
| `fsList` | `{path?}` | `FsList` |
| `fileComplete` | `{query,cwd?,limit?}` | `FileEntry[]` (`path` chat path, `resolvedPath` OS-absolute path to insert, `outside`, `isDir`; gitignored paths never suggested; `../` completes from the workspace parent) |
| `openSession` | `{sessionId?,cwd?,worktree?,name?}` | `Session` (`sessionId,cwd,provider,model,revert?,worktree?,subagent?`; `subagent` is set for a subagent session, with its live `status`, see Subagents); `worktree:true` creates the session in a new git worktree of `cwd`'s repo on branch `paladin/<name>` (see Undo and worktrees) |
| `listSessions` | `{cwd?,limit?}` | `SessionInfo[]`, newest first; only the `limit` newest sessions' transcripts are read. A session that cannot be read is still listed with `damaged` (the reason) so it can be deleted; its `cwd` may be empty (see Transcript format) |
| `sessionHistory` | `{sessionId,cwd?}` | `HistoryMessage[]` (the whole transcript; the renderer paints a page instead) |
| `sessionHistoryPage` | `{sessionId,cwd?,limit?,before?}` | `HistoryPage` (`messages`, `start`, `hasMore`, `total`); `before` is the row ordinal to end at (`0` = newest), so appending a turn never shifts an older page. `limit` defaults to 120. The renderer uses this for the initial paint and for "load earlier" |
| `sessionImages` | `{sessionId,cwd?}` | `ImagesView` (`latestUser`, `images[]` of `{index,role,path,name?}`): just the image rows, so a finished turn does not re-download the transcript to find a screenshot |
| `saveAttachment` | `{sessionId,cwd?,name,data}` (base64, up to 10 MiB decoded) | Session-owned attachment reference |
| `sessionImage` | `{sessionId,cwd?,path}` (path must occur in session history) | Image data URL |
| `sessionTodos` | `{sessionId,cwd?}` | `TodoEntry[]` (`content,status`): the agent's todo list for the panel above the composer; empty before it plans |
| `sessionUsage` | `{sessionId,cwd?}` | `SessionUsage` (`sessionId,model,contextWindow,contextKnown,calls,promptTokens,completionTokens,reasoningTokens,cacheReadTokens,cacheWriteTokens,totalTokens,lastInputTokens,cost,listCost,hasCost,fillPercent?` + per-model `models[]` + per-day `daily[]`; `fillPercent` is the last call's input tokens over the window, omitted when the model's window is unknown) |
| `budgetGet` | `{sessionId?,cwd?}` | `BudgetView` (`defaultUsd,sessionUsd,effectiveUsd,spentUsd,hasCost,calls`); `calls` counts model calls that reported usage, so `hasCost:false` with `calls>0` means the provider sends no cost; `sessionUsd` is `null` when the session inherits the default; `effectiveUsd` `0` means no limit. Without `sessionId` only the default is reported |
| `budgetSet` | `{scope,sessionId?,cwd?,budgetUsd}` | `BudgetView`; `scope` `global` (default in `config/settings.json` as `defaultBudgetUsd`) or `session` (session meta); `budgetUsd` a non-negative USD amount, `0` for a session turns the limit off, `null` clears it (inherit) |
| `thinkingGet` | `{provider?,model?}` | `ThinkingView` (`provider,model,levels,level`); `levels` are the thinking (reasoning effort) levels the model accepts, weakest first, from `none,minimal,low,medium,high,xhigh,max` — empty when the model has no adjustable thinking; `level` `""` means the provider default (nothing is sent). Empty `provider`/`model` resolve to the default ones. The provider's own catalog metadata decides the levels when it published them (OpenCode Go), else the model's name does; the levels are only offered for the chat-completions and responses dialects, because the Anthropic and Gemini requests do not carry an effort yet |
| `thinkingSet` | `{provider,model,level}` | `ThinkingView`; stores the level app-wide per model in `config/settings.json` as `thinkingLevels: {provider: {model: level}}`; `""` clears it. A level the model doesn't list is rejected. Applies from the next model call |
| `overallUsage` | `{cwd?,limit?}` | `OverallUsage` (fleet totals + `models[]` heaviest-first + `sessions[]` heaviest-first capped at `limit` + `daily[]` oldest-first) |
| `deleteSession` | `{sessionId,cwd?,discardWorktree?}` | `bool`; a worktree session's worktree is removed with its last session if clean, or always with `discardWorktree:true` |
| `getPrompt` | `{}` | `Prompt` (`systemPrompt` = `config/SYSTEM_PROMPT.md`; a missing/blank file seeds from the built-in default) |
| `suggestPrompt` | `{systemPrompt, instruction, provider?, model?}` | `Prompt` proposal; does not save or start a turn |
| `setPrompt` | `{systemPrompt}` | `Prompt` (blank rejected) |
| `resetPrompt` | `{}` | `Prompt` (file rewritten from the built-in default) |
| `stopChat` | `{sessionId?,cwd?}` | `bool` |
| `listSkills` | `{cwd?}` | `SkillEntry[]` (name, description, source, path; content only at turn time) |
| `listCommands` | `{cwd?}` | `CommandEntry[]` (`name,description,argumentHint?,mode?,tools?,source` builtin/global/workspace, `path?`); `tools` are command-only tools such as `report_findings` |
| `memoryGet` | `{target,cwd?}` | `MemoryView` (`target` user/global/project, `cwd?`, `path`, `entries[]`, `used`, `limit` chars); `project` requires `cwd` |
| `memorySet` | `{target,cwd?,entries[]}` | `MemoryView`; replaces the store (dedupes, masks secret-shaped values, enforces the limit and the threat scan) |
| `memoryProjects` | `{}` | `Project[]` (`cwd,key,entries,used,limit`): folders that have memory |
| `memoryRefreshSnapshot` | `{sessionId,cwd?}` | `bool`: the session's next turn re-reads memory instead of its frozen snapshot |
| `memorySnapshotStatus` | `{sessionId,cwd?}` | `{frozen,stale,changed[]}`: whether the session's frozen memory snapshot is behind the stores on disk (`changed` names the targets) |
| `memoryConfig` | `{}` | `{limits{user,global,project},disabled[],provider,reviewTurns}`: effective `<data>/config/memory.json` |
| `contextSources` | `{cwd?}` | `ContextView` `{cwd,sections[],bytes,files,skills,mcp}`: everything a turn in `cwd` loads into its system prompt without user or agent action. Each `ContextEntry` is `{kind: instructions\|memory\|skills\|mcp, scope, title, path?, detail?, bytes, truncated?, content?}` — project instruction files in prompt order (global `AGENTS.md`, then the `.git` root → `cwd` chain of `AGENTS.override.md`/`AGENTS.md`/`CLAUDE.md`), each enabled memory scope with entries, the skill catalog and the MCP server notes; `content` is the text the prompt carries. `files`, `skills` and `mcp` are the counts the summary line uses |
| `forkSession` | `{sessionId,cwd?,worktree?,name?}` | `SessionInfo` of the copy (title `… (fork)`); `worktree:true` forks into a new worktree, a plain fork stays in the source's folder (and worktree) |
| `revertPreview` | `{sessionId,cwd?,index}` | `{index,turns,files[],deleted[],skipped[],snapshots}`: what reverting to the user message at transcript `index` would restore, delete, or leave alone |
| `sessionChanges` | `{sessionId,cwd?}` | `{available,turns:[{turn,files:[{path,status}],callIds:[]}],files:[{path,status}]}` from this session's saved file snapshots |
| `sessionDiff` | `{sessionId,cwd?,turn?,path}` | `{path,patch,truncated}`: unified diff for one changed file; `turn` is a transcript index or omitted for the session |
| `fileExcerpt` | `{sessionId,cwd?,path,start,end}` | `{path,rel,startLine,lines,truncated}`: lines `start`..`end` plus 3 of context (at most 60) of a text file inside the session workspace; used by review findings |
| `filePreview` | `{cwd?,path}` | `{path,rel,kind: text\|image\|binary\|dir\|outside\|missing,lines?,dataUrl?,truncated?}`: the first 4 non-empty lines of a workspace text file, or a small image as a data URL, for the composer's tag cards. A path outside the workspace or a folder yields its kind with no content |
| `revertSession` | `{sessionId,cwd?,index}` | `{index,message,restored[],deleted[],skipped[],failed?[],snapshots}`: restores the folder to the snapshot taken before that user message and drops the message and everything after it. This is final and cannot be undone. `message` is its text for the composer. `deleted` lists files created since the snapshot; `failed` lists files whose restore errored (also in `skipped`). Fails while a turn runs |
| `gitInfo` | `{cwd}` | `{repo,root?,branch?,hasCommits}` |
| `worktreeStatus` | `{sessionId,cwd?}` | `{exists,changed,ahead,branch,baseBranch?,into,path,repoRoot}`: `changed` files in the worktree, commits `ahead` of the repo's checkout (`into`) |
| `worktreeMerge` | `{sessionId,cwd?,message?}` | `{merged,nothingToMerge,commit?,conflicts[],into?}`: commits the worktree's changes (message defaults to the session title) and merges its branch into the repo's current checkout with `--no-ff`; conflicts abort the merge and are listed. Fails if the checkout has uncommitted tracked changes |
| `renameSession` | `{sessionId,cwd?,title}` | `bool`; blank title restores the derived one (max 120 chars) |
| `answerAsk` | `{sessionId,callId,answers:[{question,answer,selected?}]}` | `bool` (false: nothing waiting) |
| `cancelAsk` | `{sessionId,callId}` | `bool` |
| `answerApproval` | `{sessionId,callId,decision}` | `bool`; `decision` is `once`, `session`, `folder`, `global` or `deny` (must be one of the card's `choices`); unknown calls fail |
| `permissionsGet` | `{cwd?,sessionId?}` | `{global: Rule[], folder: Rule[], session: Rule[], folders: [{cwd,rules}], sandbox: {kind}, presets}`; `sandbox.kind` is `seatbelt`, `landlock` or `""` (approvals only) |
| `permissionsSet` | `{scope,cwd?,rules}` | same view; `scope` is `global` or `folder` (session rules come only from approvals). `Rule` is `{tool: shell\|write\|read\|mcp\|web\|project\|extension, pattern, action: allow\|deny}`: shell patterns are word prefixes (`npm test`), write and read patterns absolute directories (an allow-write rule also allows reading there), mcp patterns `server`, `server/*` or `server/tool`, web patterns a domain (`example.com`, covering its subdomains), extension patterns a plugin (`jira`), one of its tools (`jira/lookup`) or everything (`jira/*`); `*` matches all |
| `mcpList` | `{cwd?}` | `MCPOverview`: `{servers: ServerStatus[], globalPath, folderPath?, hasFolderConfig, trusted, errors?: {global?, folder?}}` — global servers plus `cwd`'s own (see MCP servers) |
| `mcpConfig` | `{scope,cwd?}` | `{path, text, exists}`: one `mcp.json` for editing (`scope` `global` or `folder`; a missing file returns a starter `text`) |
| `mcpSetConfig` | `{scope,cwd?,text}` | `MCPOverview`; validates, writes and applies the file. A folder file saved here is trusted |
| `mcpSetEnabled` | `{scope,cwd?,name,enabled}` | `MCPOverview`; sets the server's `enabled` flag in its file (a trusted folder file stays trusted) |
| `mcpRestart` | `{scope,cwd?,name}` | `MCPOverview`; stops the server, clears its failure and starts it |
| `mcpTrust` | `{cwd,trust,fingerprint}` | `MCPOverview`; approves (or revokes) the folder's `mcp.json`; `fingerprint` is the listed `folderFingerprint`, and trust fails if the file changed since |
| `mcpParse` | `{text}` | `{servers: {name, config}[]}`: servers read from pasted text — launch commands (`npx`/`uvx`/`docker run`/…, `KEY=value` prefixes, one per line), a Streamable HTTP URL, `claude`/`codex mcp add …` lines or `mcp.json` JSON. Nothing is saved |
| `mcpAdd` | `{scope,cwd?,servers,replace?}` | `MCPOverview`; adds `{name, config}[]` to one `mcp.json` (existing names fail unless `replace`) and starts them. A new or trusted folder file is trusted afterwards; an untrusted one stays untrusted |
| `listAgents` | `{sessionId}` | `AgentInfo[]`: the session's subagents in spawn order with live status (see Subagents) |
| `interruptAgent` | `{sessionId,agentId}` | `AgentInfo`: stops the subagent's running turn and drops its queued follow-ups; an idle agent is returned unchanged. `agentId` may also be the agent's name |
| `sideQuestion` | `{sessionId,cwd?,question,chatId?,provider?,model?}` | `{chatId, answer, model}`: a `/btw` side-chat answer about the session — the running turn's conversation as of its latest model call when one is live, else the saved history — plus that side chat's earlier exchanges, with no tools. Without `chatId` a new side chat starts. The question and answer are saved to `<session dir>/side-chats/<chatId>/chat.json` once the answer arrives (a failed first question leaves no chat); nothing is added to the transcript. One question per chat at a time (a second fails "side chat is busy"). Billed to the session's usage (and budget). Runs on the long pool, so its response can arrive after later requests' |
| `listSideChats` | `{sessionId,cwd?}` | `[{id, title, updatedAt, messageCount}]`: the session's saved side chats, newest first |
| `loadSideChat` | `{sessionId,cwd?,chatId}` | `{id, title, createdAt, updatedAt, messages: [{role, content, model?, createdAt}]}` |
| `deleteSideChat` | `{sessionId,cwd?,chatId}` | `null`; removes the side chat's folder |
| `hooksList` | `{cwd?}` | `HooksOverview`: `{sources: Source[], events}` — the global `config/hooks.json` plus, with `cwd`, the folder's `.paladin/hooks.json`, `.codex/hooks.json` and `.claude/settings.json` (see Hooks). `Source` is `{path, scope, format: paladin\|codex\|claude, exists, editable, trusted, error?, warnings?, text?, handlers: [{key, event, matcher?, command, timeout?, statusMessage?, enabled}]}` |
| `hooksSetConfig` | `{scope,cwd?,text}` | `HooksOverview`; validates and writes the global or folder `hooks.json` (blank `text` deletes it). A folder file saved here is trusted |
| `hooksTrust` | `{cwd,path,trust,fingerprint}` | `HooksOverview`; approves (or revokes) one of the folder's hooks files; `fingerprint` is the listed source's, and trust fails if the file changed since |
| `hooksToggle` | `{cwd?,key,enabled}` | `HooksOverview`; turns one handler on or off |
| `toolsList` | `{cwd?,sessionId?}` | `ToolsOverview`: `{tools: ToolInfo[], pending: string[], extensionErrors?: string[], extensions?: PluginInfo[], runners?: RunnerInfo[], behavior: {formatOnWrite, verifyNudge, lspDiagnostics, webPrivateNetwork}, languageServers: {id,root,state,pid?,error?}[]}` (see Tool switches and Local extensions) |
| `toolsSet` | `{scope,cwd?,sessionId?,names,enabled}` | `ToolsOverview`; `scope` `global` or `session`; `enabled` `true`/`false`, or `null` to clear the switch |
| `toolBehaviorSet` | `{key,enabled,cwd?,sessionId?}` | `ToolsOverview`; app-wide tool behavior saved in `settings.json`; `key` `formatOnWrite`, `verifyNudge`, `lspDiagnostics` or `webPrivateNetwork` (off by default: lets web tools reach private addresses) |
| `customList` | `{}` | `CustomItem[]`: every editable built-in, by group (System prompt, Tool guidance, Tool descriptions, Modes, Reminders, Subagents, Compaction, Commands, Skills, Settings files): `{id, group, title, description, kind: text\|command\|skill\|json, vars?, customized, enabled?}` (`enabled` for skills) |
| `customGet` | `{id}` | `CustomState`: the item plus `value` (current text), `default`, `customized`, `path` (the override file), `missing?` (placeholders the text no longer uses) and `enabled?` |
| `customSet` | `{id,text}` | `CustomState`; saves an override (text items in `<data>/config/custom/<id>.md`; commands, skills, `settings.json` and `lsp.json` in their own files). Text equal to the default removes the override; JSON files are validated first |
| `customReset` | `{id}` | `CustomState`; deletes the override so the default applies (for `settings.json`: every setting returns to its default) |
| `skillEnabledSet` | `{name,enabled}` | `true`; turns a skill on or off by name (`settings.json` `disabledSkills`), whatever its source |
| `toolsAccept` | `{cwd?,sessionId,names?}` | `ToolsOverview`; the session uses these pending tools (all when `names` is empty) from its next turn |
| `toolsReload` | `{cwd?,sessionId?}` | `ToolsOverview`; re-reads `extensions.json` and every enabled plugin, drops the warm plugin runners and returns the refreshed view. A running turn keeps the tools it started with: the new set applies from the next turn |
| `pluginViews` | `{}` | `{views: [{plugin,view,title,mount,permissions,destructive?}], cards: [{plugin,tool,exposed}], errors?: string[]}`: the side-pane panels and inline tool-result cards enabled plugins ship. Reads manifests and checks files only; starts no plugin process |
| `pluginInvoke` | `{sessionId,cwd?,plugin,view,tool,args?,destructive?}` | `{content,isError}`: runs one tool for a plugin panel through the normal permission gate. An approval publishes `plugin/approval` and waits for `answerApproval`; the view's `invokeTool`/`invokeToolDestructive` check happens in Electron main |
| `submitChat` | `{sessionId?,message,cwd,provider?,model?,systemPrompt?,resume?,skill?,display?,command?,mode?,compact?,permissions?}` | `TurnInfo` at once (`state` `running` or `queued`); progress arrives as events (below). `permissions` is `read-only`, `auto` (default) or `full-access` (see Approvals); `command` runs a prompt command with `message` as its arguments; `mode` is `build` (default) or `plan` (read-only tools); `compact:true` runs a manual compaction instead of a model turn (no message needed) |
| `steerChat` | `{sessionId,message,display?}` | `TurnInfo` of the running turn; fails when the session has nothing running (submit instead). The message joins that turn before its next model call (see Steering) |
| `turns` | `{}` | `TurnInfo[]`: every running and queued turn, all sessions |
| `resources` | `{}` | `{at, supported, self:{cpu,rss,procs}, sessions:{[id]:{cpu,rss,procs}}}`: the core process plus each session's running shell process trees (one `ps` sample, cached 1.5s; `cpu` is % of one core, `rss` bytes). `supported:false` off unix |
| `runningJobs` | `{}` | `JobInfo[]`: every shell command still running (id, sessionId, workspace, command, startedAt, elapsedMs, auto), oldest first |
| `killJob` | `{id}` | `JobInfo`; stops a running command from the app and makes sure the next turn sees that the user stopped it |
| `attach` | `{sessionId,cwd?,limit?}` | `{history: HistoryPage, turn: TurnInfo\|null, events: Event[], queued: TurnInfo[], seq}`; `history` is the newest page (`sessionHistoryPage`), `limit` defaults to 120; older rows load with `sessionHistoryPage` |
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
Code. `transcript_path` is the session's `messages.jsonl` (see
Transcript format below). Exit 2 blocks (stderr is the reason) for events that can block;
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
resume?, permissions?, parentSessionId?, state: queued|running|done,
persisted?, reply?, stopped?, cancelled?, awaitingApproval?, error?,
queuedAt, startedAt?, endedAt?, stats?, index?}`. `index` is the
transcript position of the turn's user row (absent on a resume turn), so
the renderer can attach the turn's change card once it ends. `stats` is the turn's cost,
`{durationMs, tokens}`, where `tokens` counts generated (completion)
tokens and therefore includes reasoning; it is written on the turn's
transcript line and repeated on `turn/done` so the renderer can draw the
summary under the turn without a second read. `awaitingApproval` is true
while a tool call waits on an approval card. `parentSessionId` marks a
subagent's turn (the session that gave it the work); the desktop does
not notify or toast for those.

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
`getPrompt`/`setPrompt`/`resetPrompt`. `suggestPrompt` sends the current
editor text and instruction to the selected provider and returns a draft
without changing the file or starting a chat turn.

## Responses (stdout)

Unary:

```json
{"id": 1, "ok": true, "result": [...]}
{"id": 1, "ok": false, "error": "..."}
```

Responses are matched by `id` and can arrive out of request order. The
host answers control calls (`ping`, `stopChat`, `cancelTurn`,
`answerApproval`, `answerAsk`, `cancelAsk`, `turns`, `resources`,
`getWorkspace`, `fsHome`, `resolveWorkspace`) inline, so they never wait
behind slow work. Everything else runs on a bounded worker pool:

- session mutations (`submitChat`, `steerChat`, `renameSession`, `forkSession`,
  `deleteSession`, `revertSession`, …) run one at a time per `sessionId`, in
  request order;
- host-wide settings (providers, prompt, memory, permissions, hooks, MCP,
  tools) run one at a time, in request order;
- reads run in any order; `sideQuestion` and `refreshProviders` use a
  separate small pool.

A `stopChat` refuses that session's `submitChat`/`steerChat` requests sent
before it that have not started yet (error `stopped before it started`),
and waits for one already starting so the stop reaches its turn. When
too many requests are in flight a request fails at once with `agent host
is busy, try again`; after stdin closes, queued requests fail with `agent
host is shutting down`.

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

A plugin panel's `invokeTool` may need approval. That request is not part
of a turn, so it arrives as a `plugin/approval` event with an
`approval` payload and no `turnId`:

```json
{"event": "plugin/approval", "sessionId": "...", "approval": {"id": "plugin-...", "kind": "extension", "state": "pending", ...}}
```

The client answers it with `answerApproval(sessionId, approval.id, decision)`.
`pluginInvoke` then returns the tool result or the denial.

`plugin/updated` has no session and no payload: plugin manifests were
re-read (a `toolsReload`), so an open panel should offer to reload.

```json
{"event": "plugin/updated"}
```

Events are written to stdout in `seq` order by one writer that never
holds up the agent. If the client stops reading, the writer buffers up
to about 20,000 events or 8 MB; past that it drops streaming chunks
(`chat/delta`, `chat/reasoning`, `chat/retry`, `chat/compact`,
`chat/hook`, `chat/usage`) but never `turn/*`, `chat/tool`,
`chat/approval`, `chat/steer`, `chat/budget` or `chat/thinkingDropped`.
Once it catches
up it sends

```json
{"event": "host/resync", "seq": 812, "sessionId": "", "turnId": ""}
```

and the client re-reads `turns` and re-`attach`es the sessions it
shows; the turns themselves are unaffected.

More streaming events:

```json
{"event": "chat/retry", "sessionId": "...", "retry": {"attempt": 1, "max": 6, "delayMs": 2000, "reason": "status 503: overloaded"}}
{"event": "chat/compact", "sessionId": "...", "compaction": {"phase": "start", "reason": "auto", "tokensBefore": 170000}}
{"event": "chat/compact", "sessionId": "...", "compaction": {"phase": "done", "reason": "auto", "tokensBefore": 170000, "tokensAfter": 30000, "summary": "..."}}
```

- `chat/retry`: a round failed with a transient provider error (429,
  5xx, timeout, stalled stream) before producing output and runs again
  after `delayMs` (exponential backoff, or the server's Retry-After
  header or a wait hint in its error message). Any progress resets the
  retry budget.
- `chat/compact`: context compaction. `reason` is `auto` (the next
  request would fill 85% of a known context window), `overflow` (the
  provider rejected the prompt as too long), or `manual` (the user ran
  `/compact`). `phase` is `start`, then `done` (with `summary`, the
  model-written summary) or `failed` (with `error`). The older
  conversation is replaced
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
- `chat/usage`: `{}` — one LLM call's tokens and cost were written to
  the session's usage ledger. The client refetches `sessionUsage` (which
  knows the model's context window and running totals) so the spend and
  context-fill pill move after **every** call, not only at `turn/done`.
  The event is a signal with no payload and is droppable: a later call
  or `turn/done` refreshes the same reading.
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

Web requests (`web_fetch`, and `browser` `open`) pass the gate as
`kind: "web"` cards with `host` and `url`. They run under a `web` allow
rule whose pattern is a domain (it covers its subdomains) or `*`, or ask
once/session/folder/global/deny and remember the host. A service on
this machine is `localhost:PORT` whichever name the URL used: only a
rule naming that port allows it (`*` does not), so each local port asks
once. Full access runs them; deny rules (a deny `*` included) win.

A tool the workspace itself ships (a formatter or language server in
`node_modules/.bin` or a virtualenv, or a server that runs the
project's own code: rust-analyzer, typescript-language-server with the
project's TypeScript) asks before it first runs, as a `kind: "project"`
card on the call that would start it: `command` names the tool, `path`
the folder, and the choices are session/folder/deny. A `project` rule
(an absolute folder) remembers the answer; a refusal is kept for the
session. Without it, Paladin uses a copy installed outside the
workspace, or skips the tool. Writes into `.git`, `.paladin`, `.claude`,
`node_modules` or a virtualenv ask once/session/deny in every preset but
full access.

Extension tools (local tools from `config/extensions`) run without the
sandbox, so each call is a `kind: "extension"` card with `tool` and
`args`, unless an `extension` rule names the tool (or `*`); answers are
once/session/folder/global/deny. Plan mode and the read-only preset
leave extension tools out.

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
`ToolInfo` is `{name, description?, source: builtin|mcp|extension, server?, tool?,
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

### Local extensions

Tools the user writes themselves live in `<data>/config/extensions`.
`extensions.json` lists what is enabled, and each entry is either a
plugin folder or a legacy loose file:

```jsonc
{"enabled": ["my-plugin", "legacy.ts"]}
```

A **plugin folder** is `config/extensions/<name>/` with an `index.ts` (or
`index.js`), an optional `package.json` plus `node_modules` of its own,
and an optional `paladin-plugin.json`:

```jsonc
{
  "entry": "index.ts",         // relative to the folder; default index.ts then index.js
  "tools": null,               // static declarations; null asks the plugin instead
  "timeoutMs": 120000,         // per call, clamped to 1000..300000
  "idleMs": 300000,            // reap the warm process after this idle; 0 = never
  "env": {"FOO": "bar"},       // extra environment; PATH is refused
  "node": "/opt/node/bin/node",// absolute path, this plugin only
  "permissions": ["network"],  // "network" | "spawn" | "fs-outside-workspace"
  "views": [                   // optional side-pane panels, max 4
    {"id": "board", "title": "Ticket Board", "entry": "ui/board.html",
     "mount": "side-pane", "permissions": ["invokeTool", "events"]}
  ],
  "cards": [                   // optional inline result renderers, max 16
    {"tool": "lookup", "entry": "ui/card.js", "inline": true}
  ]
}
```

The module's default export is `{tools: [{name, description,
parameters, execute(args, ctx)}], onActivate?(ctx),
onDeactivate?()}`. Every tool of a plugin folder runs on one long-lived
Node process (one per plugin), started on first use and reused across
turns and tool calls; it is reaped when idle, restarted after a crash,
and asked to shut down when the app exits. `ctx` is `{pluginDir, pid,
workspace}`. A plugin's tools are exposed as
`extension__<folder>__<tool>`; a loose file `legacy.ts` keeps
`extension__legacy__<tool>`, so moving `legacy.ts` into `legacy/`
does not change the names the model learned.

Runners resolve Node in this order: the plugin's own `node`, the
Electron binary the app passes down (`ELECTRON_NODE`, i.e. Electron's
bundled Node 24 via `ELECTRON_RUN_AS_NODE`), `PALADIN_NODE`, then `node`
on `PATH` (24 or newer). A setting that is present but broken fails that
plugin loudly instead of falling through.

Calls are gated like MCP tools: each asks until a rule allows the plugin,
one of its tools or everything. Approval cards offer `once`, `session`,
`folder`, `global` and `deny`, and the saved rule reads `jira`,
`jira/lookup`, `jira/*` or `*`. A loose file counts as the plugin of the
same name (`legacy.ts` → `legacy/…`), so a rule keeps working when the
file moves into a folder; rules that name an exposed tool
(`extension__jira__lookup`) were refused by the validator before this
grammar, so none are stored. Extension tools are left out of read-only
and plan mode.

`extensions` in `ToolsOverview` reports each enabled plugin (`kind`,
`path`, declared `permissions`, its `tools`, or an `error`), `runners`
reports the warm processes (`plugin`, `pid`, `node`, `lastUsed`,
`inflight`, `generation`, a `stderr` tail), and `toolsReload` re-reads
everything and drops the warm runners.

### Plugin UI

`pluginViews` lists the panels and cards the enabled plugins ship;
`pluginInvoke` runs one tool for a panel through the normal gate (see
the method table). The desktop app serves panel files over a private
`plugin://` scheme: `plugin://<plugin>/<view>/<path>` where `<path>` is
under the plugin's `ui/` folder. Frames are sandboxed with
`allow-scripts` only (opaque origin, no storage, no network) and get a
`window.paladin.plugins` bridge instead of the raw `agent:invoke`:

| Call | Permission | Notes |
|---|---|---|
| `getContext()` | — | `{sessionId, workspace, plugin, view, theme}` |
| `invokeTool(tool,args)` | `invokeTool` | own tools + `read`/`grep`/`glob`/`list`; other tools need `invokeToolDestructive`; 60/min |
| `readFile(rel)` | `readFile` | relative to the panel's workspace, ≤1 MiB text; 120/min |
| `readStore(key)` / `writeStore(key,value)` | — | per-plugin JSON store, 100 KiB per key / 1 MiB total |
| `openExternal(url)` | `openExternal` | `https:` only; 3/min |
| `subscribe(filter)` / `unsubscribe(id)` / `onEvent(cb)` | `events` | chat/delta, chat/tool, turn/start, turn/done, browser/navigated, plugin/updated; own session only; 240 events/min |
| `card.js` | — | no bridge; a card is a pure renderer |

A tool call that needs approval publishes `plugin/approval` and waits for
`answerApproval`; the card is rendered by the host, never by the panel.
The plugin UI guide is `docs/PLUGIN-UI.md`.

### Subagents

A turn can delegate focused work to **subagents** with six tools:
`spawn_agent`, `send_message`, `followup_task`, `wait_agent`,
`interrupt_agent` and `list_agents`. Each subagent is a persistent child
session stored beside its parent (`sessions/<encoded-cwd>/<child-uuid>/`);
its `meta.json` has `subagent: {parentId, parentTurnId, spawnCallId, type,
name, task, forkTurns, status, result?, error?, reported, completionId?,
updatedAt}` and
the parent's lists the child ids under `children`. `SessionInfo` carries
both (`subagent` with a live `status`, `children`), so `listSessions`
can nest children under their parent after a restart. A subagent is not
a fork: `forkedFrom` stays empty.

Two kinds exist, both read-only (no `write`/`edit`, plan-mode shell):
`explore` (codebase investigation) and `reviewer` (code review). A
child's tools are its kind's allowlist narrowed by the parent's and its
own tool switches, never MCP or the agent tools, so children cannot
spawn children and never gain a tool the parent lacks. They run on the
parent's provider (a `model` override must be one the provider lists)
with the parent's permission preset, through the same hub, approvals,
hooks, sandbox and budget. Spend is shared: a child counts toward its
parent's budget limit and the family's usage is summed.

`fork_turns` is `none` (default: the child sees only its task) or `all`
(it also gets a copy of the parent's finished turns, cut at a pending
undo; the running turn is never copied). At most `maxSubagents`
(settings, default 5) children may be queued or running per parent.
The limit and name checks are atomic with the child's creation (one
gate per parent, so parents never wait on each other), so parallel
`spawn_agent` calls cannot overshoot the limit or share a name,
and a `followup_task` that reactivates a finished child needs a free
slot too.

Each finished child result is delivered to the parent: as the
`wait_agent` result, as a notify row (`agent` tool name, content
`{"event":"subagent_finished",…}`) drained before the parent's next
model call, or — when the parent is idle — by starting a parent turn
with `resume:true` to read it. Each result has a
`completionId` (`<child id>#<child turn id>`), used as the notify row's
tool-call id and returned as `completion_id` by `wait_agent`, so the
parent transcript records which results it holds. A delivered result is
marked `reported` only once the parent turn that took it is saved; if
that turn's save fails it goes back to the parent's mailbox, and after
a crash before the save the restart delivers it again. On restart, an
unreported result whose `completionId` is already in the parent
transcript (a crash between the save and the `reported` write) is
marked reported instead of delivered again. Delivery is therefore
exactly once, including across crashes; a failed `reported` write is
logged and settled by the same check at the next restart. A child stopped by its parent (or by
stopping the parent) is not reported back. `wait_agent` lends the
parent's hub slot, so a child runs even with `maxConcurrentTurns: 1`.
After a restart, a child whose turn was cut off reads `interrupted` and
unreported results are delivered on the parent's next turn.

`submitChat` into a subagent session fails: only its parent starts new
work. `steerChat` is different — the user may watch a child's live
window and guide it while it runs, so a steer joins that turn exactly as
it does for a normal session. With nothing running it still fails, so a
finished child stays read-only. Stopping its turn
(`stopChat`/`interruptAgent`) works, and the parent is told. Deleting a
parent deletes its children.

Status changes stream as

```json
{"event": "agents/changed", "parentSessionId": "…", "agent": {"agentId": "…", "parentSessionId": "…", "name": "explore-1", "type": "explore", "task": "…", "forkTurns": "none", "status": "running", "result": "…", "error": "…", "updatedAt": 1767225600000}}
```

with `status` one of `queued`, `running`, `completed`, `failed`,
`interrupted`. The child's own turn events stream like any session's.

### Undo and worktrees

Every `shell`, `write` and `edit` run is bracketed by snapshots of the
session's folder in a shadow git repo (`snapshots/<folder>-<hash>/`,
never the project's own `.git`); the step (`{turn, callId, tool,
before, after, files}`) goes to the session's `steps.jsonl`. Snapshots
skip the project's ignored files, dependency/cache folders, binaries
and files over 10 MiB, and turn off for `/`, the home folder or folders
with more than 50,000 files; `PALADIN_SNAPSHOTS=off` disables them.
Without git, undo reverts only the conversation (`snapshots:false`).
A foreground shell run the plan-mode read-only allowlist accepts is not
bracketed at all, and `write`/`edit` snapshot only their target path
(falling back to the whole folder when the path leaves the workspace),
so a busy agent does not pay two full git scans per command.

The renderer paints transcripts in pages: `attach` and
`sessionHistoryPage` return one `HistoryPage`, and `sessionHistoryPage`
with the previous `start` loads older rows. `sessionImages` returns
just the image rows, which is what a finished turn needs to mount a
screenshot without downloading the transcript.

`sessionHistory` user rows carry `index`, their transcript position, and
`stats` (`{durationMs, tokens}`, optional) for the turn that row starts,
so a reopened session shows the same end-of-turn summary the live turn
did.
`revertSession` restores the folder to the snapshot taken before that
user message, exactly: files changed since go back to their old content,
and files created since are deleted. It then drops that message and
everything after it from the transcript. The revert is final — there is
no redo — and `openSession` / `listSessions` stop reporting a pending
`revert`. Changes made by the user or another session after the turn are
overwritten too.

Shell commands belong to the session that started them: `status`, `wait` and
`cancel` with another session's `job_id` (including a subagent's) fails as an
unknown job. A foreground call waits up to 60 s; a command still running after
that is moved to the background automatically and returns its `job_id`, and
the process keeps running until it exits or is cancelled. No shell command has
a timeout (`timeout_ms` is gone). An auto-backgrounded job reports its running
time through the session's notice stream every five minutes until it ends,
and its completion notice wakes an idle session. Explicit `background: true`
jobs send no running reminders and report their completion by default; pass
`notify: false` to opt out. `wait` takes a `job_id` and `seconds` (default 60,
capped at 300), blocks until the job ends or the time is up, returns its
output, and consumes the completion notice it saw, so no second notification
follows.
They are tracked separately: the sidecar keeps a record in
`<sessionDir>/jobs/<jobId>.json` (`status` running/done/failed/cancelled,
`startedAt`, `endedAt`, `exitCode`, `command`, `finalized`) so the app can
list and stop them. A finished or cancelled job is finalized exactly once.
`revertSession` fails while one of the session's jobs is still running, or
while a job left by an earlier sidecar process is still alive. A recycled
pid can cause a false refusal; where process groups cannot be probed
(Windows) the job is treated as ended. Undo takes its snapshot at the start
of a turn, so a job's files belong to that turn.

Commands and tools run at the same priority as the user's terminal. They do
not hold a folder-wide lock while they run; only a revert briefly serializes
the folder with other reverts. Sessions that need real isolation should use a
worktree.

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

## Transcript format

`<session dir>/messages.jsonl` holds one JSON object per line. A turn
is written as a single line, `{"turn": {"messages": [Message, ...]}}`,
and synced, so a crash or failed write leaves all of a turn or none of
it; a failed write is cut back off the file. Lines written by older
builds, forks and subagent snapshots hold one `Message` each. Readers
must accept both; message indexes (undo, `revertPreview`) count the
messages inside turn lines. An unterminated last line is a torn write
and is ignored (the next append removes it); any other line that does
not decode is an error. Builds before this format show a turn line as
an empty row.

`<session dir>/summary.json` caches what `listSessions` shows (message
count, fallback title) and the byte offset it has read up to, so a
listing reads only what was appended since. It is rebuilt from
`messages.jsonl` whenever it is missing, unreadable, or does not match
the file (a rewrite such as undo drops it), and may be deleted freely.
A session whose `meta.json` cannot be read, or whose transcript has a
line that does not decode, is listed with `damaged` instead of being
skipped.

## Electron side

- `sidecar.ts` owns the agent-host process and a `pending` map
  `id -> {resolve, reject}`; unary `agent:invoke` resolves on the
  matching `{id}` line, whatever order replies arrive in.
- Every call has a deadline: 30s for reads (the dispatcher's read
  methods), 10 min for `sideQuestion` and `refreshProviders`, 11 min for
  `terminalRun` (the host stops it at 10 min), and 120s for
  everything else. A timed-out mutation rejects with `agent-host did not
  answer <method> within <n>s; it may still have completed, ...`. The
  bridge never retries a call; a late reply is dropped.
- More than 512 calls awaiting replies, or 16 MB of request bytes the
  host has not read, rejects new calls with `agent-host is busy, try
  again` (the host's own queue answers the same way).
- A reply whose id is known but which is not valid JSON, or lacks a
  boolean `ok`, rejects that call with `agent-host sent a malformed
  reply to <method>`. Other stray lines are logged and ignored.
- If the sidecar exits unexpectedly, in-flight calls reject with
  `agent-host exited`, calls made while it is down reject with
  `agent-host is not running`, and it is restarted with exponential
  backoff (0.5s doubling to 30s; reset after 60s of uptime). Once the
  new process answers a `ping`, the renderer receives
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
- WebContentsViews (`views:*` IPC) back the side pane's web tabs and the
  agent's local browser. Web tabs use one shared persistent partition
  (`persist:paladin-sites`) so Google SSO cookies carry over. Each open
  web tab owns a live guest: switching away detaches and throttles it,
  and closing the tab (or reloading the renderer) destroys it. The
  renderer sends `views:back`, `views:forward` and gets `views:state`
  (`name,url,title,canGoBack,canGoForward`) after navigations so the
  address bar and tab title stay truthful.
- The window is frameless; the renderer owns the title bar and sends
  `window:*` for minimize/maximize/close. `views:setTheme` mirrors the
  shell theme onto each guest view's background so the native surface
  never flashes white before the page paints.
