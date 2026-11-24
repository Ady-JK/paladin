# Agent adapters

Paladin can run a session on an external coding agent instead of its own loop. Today
those are the OpenAI Codex and Claude Code CLIs. An **adapter**
translates what the agent does into the things Paladin's UI already draws (tool cards,
questions, notices), so every agent shares the app, its colours and its cards.

```
core/internal/harness/                 the contract: Card, Question, Answer, Adapter
core/internal/harness/codexadapter/    Codex → Paladin: pure translation, no I/O
core/internal/codex/                   Codex's app-server protocol client (process, JSON-RPC)
core/internal/runtime/codex*.go        turn plumbing: scheduling, persistence, approvals
desktop-app/src/adapters/              shared agent UI (the question card) + one folder per agent
desktop-app/src/adapters/codex/        what is Codex-specific in the interface
```

## What an adapter owns, and what it does not

The adapter is **pure**. It reads the agent's JSON and returns what the conversation
should show; it does not start processes, write the transcript or touch the UI.

| Adapter (`harness/<agent>adapter`) | Runtime (`runtime/<agent>*.go`) |
|---|---|
| item → card (`Card`, `Outcome`, `PatchCards`) | the turn loop, steering, stopping |
| a question's card and the reply the agent reads (`QuestionReply`) | persisting rows, the transcript journal |
| settling a card from the conversation (`Annotate`) | approvals and the permission gate |
| what a background command's lifecycle looks like | following a command after its turn |

To add an agent: write `harness/<agent>adapter` implementing `harness.Adapter`, register
it in `runtime/codex_adapt.go`'s `adapters` map, write the runtime plumbing for its
protocol, and add `desktop-app/src/adapters/<agent>/` for anything only it needs (a
model list, a skill menu). The question card, the shell card and the diff cards are
already drawn for any agent.

## Shared vocabulary

| Card name | Drawn as | Result |
|---|---|---|
| `agent_question` | an inline form: the questions, their suggested options, a free-text field, one Send | `{status:"pending"}`, then `{status:"answered",answers:[{question,answer}]}` |
| `shell` | the terminal card | `{status:"running",output}` + `background:true` in its arguments while it runs after its turn, then `{status,exit_code,output}`; `unknown` when its outcome cannot be known |
| `write`, `edit`, `delete`, `apply_patch` | the file and diff cards | what the change did |
| `mcp__<server>__<tool>`, `web_search`, `view_image`, `image_generation`, `codex_<tool>` | the generic card | the call's result |

`answerQuestion` (RPC) sends an answer: the adapter writes it in the agent's own reply
format and the core steers it into the running turn, or starts a turn with it. Whether a
card reads pending or answered is derived from the conversation each time the history is
read, so it holds across session switches, restarts and imported threads.

`session/refresh` (event, no turn) tells a client that something settled while no turn was
running, so a session on screen reads its history again.

## Claude Code

`core/internal/claude` starts the installed `claude` CLI directly from Go with
`-p --verbose --input-format stream-json --output-format stream-json`, partial
messages, and a stdio permission host. No Node or TypeScript Agent SDK is required.
Each active session keeps one process; stdin stays open between turns. Control
requests are dispatched independently of output so a permission wait cannot block
interrupts. Writes, framing, shutdown and descendant cleanup are bounded.

`harness/claudeadapter` reconciles partial events with complete assistant blocks
using frame UUIDs and API block indices. Text, thinking, shell/file/MCP tools,
questions, compaction and tasks use the existing cards. Claude's native
TaskCreate/TaskUpdate also feed Paladin's activity list. A result ends a response,
not necessarily the process: background continuations are adopted by the turn hub
and background task cards settle even between user turns.

The CLI owns authentication, models, skills/plugins/MCP configuration and native
approval policy. Outside plan mode, Paladin automatically approves tools that pass
Paladin's guard (which asks once before catastrophic actions). A fail-closed PreToolUse host hook
applies the guard, and plan mode's restrictions, to model tool calls. Claude's
own configured hooks and startup customizations retain native CLI behavior, so choose
trusted folders; this integration does not add an OS sandbox. Hooks that cannot register safely
cause startup to fail instead of silently dropping the host gate.

Saved sessions are discovered from Claude's project transcript directories without
inference and linked into the normal sidebar. Native IDs survive app restarts;
forks and rewinds branch from the retained assistant checkpoint without modifying
the original conversation. Invisible attachment/progress entries must remain in
the parentUuid chain even though they are not rendered. Claude's on-disk format is
internal, so changes to it need fixture and live-protocol verification.

Token usage is counted once per API message rather than once per streamed block;
the context gauge uses the last request, and spend is the CLI's estimated API value,
not a charge. CLI budget flags are startup-only: changed budgets apply on the next
user turn, with bounded turns restarted using the remaining session allowance.

Hermetic tests use `PALADIN_CLAUDE_BIN`. Billed protocol/runtime tests require
`PALADIN_CLAUDE_LIVE=1` and always select `claude-haiku-5-5`. The real renderer test
uses `PALADIN_CLAUDE_REAL_HOST` pointing at a freshly built agent-host, with isolated
Paladin data and a small spend cap. The transport was validated against Claude Code
2.1.326 and Haiku 5.5.

## Linked Codex history

At renderer startup, `startCodexDiscovery` schedules a single background worker.
Each scan first checks whether the Codex CLI is available; absent installations
are skipped without starting a process. A later scan picks up a newly installed
CLI. It lists all pages of active saved user threads, creates their linked metadata
first, then hydrates histories one at a time, yielding that work while a foreground
turn runs. A `sessions/changed` event refreshes the sidebar without selecting a
session or changing the composer. Scans repeat every minute and stop at shutdown.
Deleted Paladin links are recorded in `codex-hidden.json` so discovery does not
recreate them; an explicit `importCodexThread` RPC restores a hidden link. Saved
threads appear directly in the sessions sidebar; there is no separate resume picker.
Original creation dates and workspaces are retained, including history whose old
workspace folder was deleted. Discovery never recreates those workspace folders.

A Paladin Codex session retains the original Codex thread ID. Opening it, importing
it again, and starting its next turn reconcile the saved conversation with Codex's
`thread/turns/list` history. The renderer also calls `syncSession` every three seconds
for the visible idle Codex session and when the app regains focus. These reads start
no inference and do not resume or acquire the external client's thread.

`runtime/codex_sync.go` reconciles turns by Codex ID under the session persistence
gate, preserving local attachments and turn statistics. A small `codex-sync.json`
checkpoint keeps source fingerprints across restarts, so an external rewind removes
acknowledged turns while a local turn still waiting to flush stays intact. A latest-turn
probe avoids fetching the whole conversation when nothing changes; a periodic full
check also catches updates to earlier turns, such as a background command finishing.
Paladin's running turns keep ownership of their transcript and live event stream.

## Codex: what is translated

Audited against the app-server schema the installed CLI generates
(`codex app-server generate-json-schema --experimental`, 0.169) and `tmp/codex`.

### Items (`ThreadItem`)

| Item | Paladin |
|---|---|
| `agentMessage` | assistant text, streamed from `item/agentMessage/delta` |
| `agentMessage` with `delivery:"async"` and `questions` | an `agent_question` card; **not** also text |
| `reasoning` | the thinking block (summaries, which every turn requests) |
| `plan` | text; `turn/plan/updated` becomes the todo list |
| `commandExecution` | `shell` card with its output; a command still running when its turn ends is background work (below) |
| `fileChange` | one `write` card per new file, one `edit` per hunk; `apply_patch` when no hunks can be read |
| `mcpToolCall`, `dynamicToolCall` | a card named for the call, with its result or error |
| `webSearch` | `web_search` (query, or the action's URL or pattern) |
| `collabAgentToolCall`, `subAgentActivity` | `codex_<tool>` / `codex_subagent`: prompt, model, each agent's state |
| `imageView`, `imageGeneration` | `view_image`, `image_generation` (prompt, saved path, failure) |
| `enteredReviewMode`, `exitedReviewMode` | a notice |
| `contextCompaction` | a durable horizontal "Context compacted" divider, live and in history; no invented summary or token counts |
| `userMessage`, `hookPrompt`, `functionCallOutput`, `sleep` | none: Paladin owns the user rows; the rest are internal |

### Background commands

`item/started` for a command carries a `processId` and `source: "unifiedExecStartup"`.
When the turn ends first, the item stays `inProgress` and **completes later with the old
turn's id while no turn runs** (recorded against Codex 0.169, `gpt-6-luna`). Paladin:

1. closes the card as `running`, `background:true` rather than failed;
2. keeps following the command on the thread: between turns, and during later turns,
   whose messages about an earlier turn it would otherwise drop;
3. on `item/completed` rewrites the stored row with the real outcome and publishes
   `session/refresh`;
4. after a restart nothing follows the process, so opening a Codex session reads Codex's own
   record and settles such rows (and rows an older Paladin closed as `unknown` or
   `interrupted`); one Codex cannot vouch for reads `unknown`, never `running`.

### Questions

Codex's `request_user_input_async` emits the agent message above and returns
`{"accepted":true}` at once; the reply arrives as a user message
`<send_user_message_question_reply>[{answer,question,questionItemId}]</…>` with
`questionItemId = ["request_user_input_async","<card id>",<index>]`.
(The older blocking `item/tool/requestUserInput` request is the `ask` wizard.)

### Notifications

| Handled | Why |
|---|---|
| `turn/started`, `turn/completed`, `item/*`, `item/agentMessage/delta`, `item/plan/delta`, `item/reasoning/*`, `item/commandExecution/outputDelta` | the turn |
| `turn/plan/updated` | the todo list |
| `thread/tokenUsage/updated` | the context meter and the ledger |
| `warning`, `guardianWarning`, `configWarning`, `deprecationNotice`, `model/rerouted`, `mcpServer/startupStatus/updated`, `hook/completed` | notices |
| `error` | the turn's failure |

| Not handled, by design | Why |
|---|---|
| `turn/diff/updated`, `item/fileChange/outputDelta`, `item/fileChange/patchUpdated` | the finished `fileChange` item already gives every hunk as an edit card |
| `item/commandExecution/terminalInteraction` | stdin the model writes to a background terminal; nothing to show |
| `item/mcpToolCall/progress`, `item/autoApprovalReview/*`, `serverRequest/resolved` | progress detail with no card of its own |
| `thread/status/changed`, `thread/queue/changed`, `thread/*` housekeeping, `account/*`, `app/list/updated`, `fs/changed`, `thread/realtime/*`, `windows*` | not part of a conversation |

### Server requests

Handled: `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`,
`item/permissions/requestApproval` (approval cards), `item/tool/requestUserInput` and
`mcpServer/elicitation/request` (the question wizard). Refused so Codex does not wait:
`item/tool/call` (Paladin registers no dynamic tools), `currentTime/read`,
`attestation/generate`, `account/chatgptAuthTokens/refresh`, the legacy
`applyPatchApproval` and `execCommandApproval`.

## Verified live

Against Codex 0.169 and `gpt-6-luna` through the real agent-host: an async question as a
pending card with the turn carrying on; a background command closing as running and
settling 20 s later with `exit 0` and its output; an answer sent after the turn (a new
turn that Codex read) and during it (a steer Codex acted on mid-turn); both across an
agent-host restart; both imported into a fresh data directory.
