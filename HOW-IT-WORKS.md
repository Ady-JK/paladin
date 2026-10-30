# How Paladin Works (explained like you're 10)

> The diagrams use Mermaid. GitHub renders them, and in VS Code they show in the
> Markdown preview once a Mermaid extension (e.g. "Markdown Preview Mermaid Support") is installed.

## The big idea: Paladin is a restaurant 🍽️

Think of Paladin as a restaurant.

| Restaurant | Paladin | Where it lives |
|---|---|---|
| The dining room you sit in | The **desktop app** (Electron UI) | `desktop-app/src` |
| The waiter running between the two | **Electron main + preload** | `desktop-app/electron` |
| The kitchen | The **Go core** ("agent-host" sidecar) | `core/` |
| The head chef deciding who cooks next | The **turn hub** | `core/internal/runtime/hub.go` |
| A cook working on one order | The **agent loop** | `core/internal/agent/loop.go` |
| The cook's knives and pans | **Tools** (read, write, edit, shell…) | `core/internal/tools` |
| Guest chefs with their own gadgets | **MCP servers** | `core/internal/mcp` |
| The food supplier you phone | The **AI model** (via LiteLLM / providers) | `core/internal/inference` |
| A table with its own orders | A **session** | `core/internal/transcript` |
| The order notebook | **Transcript files** on disk | `<data>/sessions/…` |
| The recipe notebook everyone shares | **Memory** | `core/internal/memory` |
| "Ask the manager first!" | **Approvals / permissions** | `core/internal/permission` |
| A safety fence around the stove | **Sandbox** | `core/internal/sandbox` |
| A photo of the kitchen before cooking | **Snapshots** (for undo) | `core/internal/snapshot` |

The dining room never cooks. It only takes orders and shows the food as it arrives.
The kitchen does all the real work, and it keeps cooking even if you walk away from your table.

---

## 1. The three big pieces

```mermaid
flowchart LR
    subgraph You["🧑 You"]
        Eyes["see & click"]
    end

    subgraph Renderer["🖥️ Dining room — Renderer (src/)"]
        UI["Chat view, sidebar,<br/>composer, settings"]
        Live["live.ts<br/>(listens for events)"]
    end

    subgraph Electron["🚶 Waiter — Electron main"]
        Preload["preload.ts<br/>window.paladin.invoke()"]
        Main["main.ts<br/>spawns agent-host,<br/>JSON lines over stdin/stdout"]
    end

    subgraph Core["🍳 Kitchen — Go core (agent-host)"]
        RPC["methods.go<br/>(the menu of RPCs)"]
        Runtime["runtime<br/>(the manager)"]
        Hub["turn hub<br/>(head chef)"]
        Agents["agent loops<br/>(cooks)"]
    end

    Model["☁️ AI model<br/>(LiteLLM :4000 → GCP)"]
    Disk[("💾 Disk<br/>~/Library/Application Support/paladin")]

    Eyes --> UI
    UI -- "invoke('submitChat', …)" --> Preload --> Main
    Main -- "one JSON line per request" --> RPC --> Runtime --> Hub --> Agents
    Agents -- "stream" --> Model
    Agents -- "events: chat/delta, chat/tool…" --> Main
    Main -- "agent-event" --> Live --> UI
    Runtime <--> Disk
```

**In kid words:**
1. You type a message. The dining room hands it to the waiter.
2. The waiter writes it as **one line of JSON** and slides it under the kitchen door (the Go program's `stdin`).
3. The kitchen answers straight away: "Got it, order #turn-123." Then it keeps shouting updates
   ("I'm writing words!", "I'm using the shell!") as more JSON lines on `stdout`.
4. The waiter carries every shout back to the dining room, which paints it on screen **live**.

The renderer never talks to the Go program directly. It always goes through the waiter (`preload.ts` → `main.ts`).
That way a broken web page can't mess with the kitchen.

---

## 2. What happens when you press Enter

```mermaid
sequenceDiagram
    autonumber
    actor You
    participant UI as Renderer (chat.ts)
    participant EL as Electron main
    participant Hub as Turn hub
    participant Agent as Agent loop
    participant LLM as AI model
    participant Disk as Disk

    You->>UI: types "fix the bug" + Enter
    UI->>UI: no session yet? openSession → new id
    UI->>EL: submitChat {sessionId, message, cwd, model, permissions}
    EL->>Hub: JSON line
    Hub-->>EL: TurnInfo {turnId, state: running|queued}  (instant!)
    EL-->>UI: reply → show "working…" spinner

    Hub->>Agent: start the turn (in its own goroutine)
    loop until the model stops asking for tools
        Agent->>LLM: messages + tool list (streaming)
        LLM-->>Agent: words… and/or "call tool X"
        Agent-->>UI: chat/delta (words), chat/tool (tool rows)
        Agent->>Agent: run tools (permission check → sandbox → tool)
        Agent-->>UI: chat/tool (result)
    end
    Agent->>Disk: save messages.jsonl, usage.json
    Hub-->>UI: turn/done
    UI->>You: spinner stops, final answer shown
```

The important trick is **step 5**: `submitChat` answers **immediately**.
It doesn't wait for the cooking to finish. Everything after that arrives as **events**.
That's what makes Paladin "async": the UI is never stuck waiting.

---

## 3. Many sessions at once (the head chef)

A **session** is one conversation (one table). Each one has:
- an **id** (like `a1b2c3…`)
- a **folder** it works in (`cwd`)
- its own **history** on disk

The **turn hub** is the head chef. It has a few rules:

1. **Different tables can cook at the same time.** By default up to **4** run at once
   (`maxConcurrentTurns` in `config/settings.json`, max 16).
2. **One table cooks one dish at a time.** If you send a second message to a busy session, it waits
   in line behind the first. Lines are first-in-first-out.
3. **If all stoves are busy**, new orders wait until a stove frees up.
4. **Every shout gets a number (`seq`)** that always goes up, so nobody gets confused about order.

```mermaid
flowchart TB
    subgraph Orders["📝 Orders coming in"]
        A1["Session A: msg 1"]
        A2["Session A: msg 2"]
        B1["Session B: msg 1"]
        C1["Session C: msg 1"]
        D1["Session D: msg 1"]
        E1["Session E: msg 1"]
    end

    subgraph Hub["👨‍🍳 Turn hub (max 4 stoves)"]
        S1["🔥 Stove 1: A msg 1"]
        S2["🔥 Stove 2: B msg 1"]
        S3["🔥 Stove 3: C msg 1"]
        S4["🔥 Stove 4: D msg 1"]
        Q["⏳ Waiting line:<br/>A msg 2 (A is busy)<br/>E msg 1 (no free stove)"]
    end

    A1 --> S1
    B1 --> S2
    C1 --> S3
    D1 --> S4
    A2 --> Q
    E1 --> Q
```

### Switching sessions without losing anything

Say you click from session A to session B while A is still cooking. **A keeps cooking.**
When you come back to A, the UI calls `attach`, and the kitchen hands over:
- everything already saved (history)
- everything the live turn has shouted **so far** (snapshotted with the current `seq`)
- anything still waiting in line

```mermaid
sequenceDiagram
    participant UI
    participant Hub
    Note over UI: You were in session B, now click session A
    UI->>Hub: attach {sessionId: A}
    Hub-->>UI: {history, turn (running), events so far, queued, seq: 812}
    UI->>UI: draw history + replay events
    Hub-->>UI: event seq 813 (new word!)
    UI->>UI: 813 > 812 → draw it
    Hub-->>UI: event seq 805 (old, arrived late)
    UI->>UI: 805 ≤ 812 → ignore (already drawn)
```

### The sidebar spinner

Every event names its `sessionId`. The sidebar watches `turn/start`, `turn/done` and `turn/queued`,
then puts a **spinner** (working), a **clock** (queued) or a **shield** (waiting for your approval) on each session row.
So you can see what's busy without opening it.

### Closing the app safely

When the app quits, the kitchen gets "stop". It stops every turn, **saves what it has**, and only then exits.
Half-finished turns are saved with a note so you can continue later.

### If the kitchen crashes

If the Go sidecar ever dies by surprise, the Electron app notices and starts a new one, waiting a little longer
after each crash in a row (half a second, then 1s, 2s… up to 30s). The window is told "the kitchen restarted",
so any turn that was cooking shows an "interrupted" error instead of spinning forever, and you can just send again.

---

## 4. Inside one turn (the cook's job)

This is the **agent loop** (`core/internal/agent/loop.go`). It's a "talk → use tools → talk again" circle.

```mermaid
flowchart TD
    Start(["Turn starts"]) --> Build["Build the prompt:<br/>system prompt + AGENTS.md +<br/>memory + switched-off tools +<br/>history + your message"]
    Build --> Big{"Too big for the<br/>model's memory?"}
    Big -- yes --> Compact["🗜️ Compaction:<br/>summarize old messages"] --> Budget
    Big -- no --> Budget{"💰 Over the<br/>spend budget?"}
    Budget -- "yes" --> WrapUp(["Tools off → model writes<br/>'done / still left' summary → end"])
    Budget -- no --> Steer["📨 Read any steer messages<br/>you sent mid-turn"] --> Round["Ask the model<br/>(streaming)"]
    Round --> Fail{"Network error /<br/>rate limit?"}
    Fail -- "yes, nothing streamed yet" --> Retry["⏱️ Wait (2s, 4s, 8s… max 30s)<br/>honor Retry-After"] --> Round
    Fail -- no --> Calls{"Did the model<br/>ask for tools?"}
    Calls -- no --> Done(["✅ Answer done → save"])
    Calls -- yes --> Doom{"Same exact call<br/>3× in a row?"}
    Doom -- yes --> Refuse["🛑 Refuse it with a hint<br/>(doom-loop guard)"] --> Round
    Doom -- no --> Batch["Group calls:<br/>safe ones run in parallel,<br/>risky ones one at a time"]
    Batch --> Gate["🛂 Permission gate"]
    Gate --> Run["Run the tool<br/>(in the sandbox)"]
    Run --> Results["Add results to the conversation"] --> Budget
```

**The safety nets, in kid words:**

| Net | What it does |
|---|---|
| **Streaming watchdog** | If the model goes silent too long, hang up and retry instead of waiting forever. |
| **Retry with backoff** | Wait a bit longer each time (2s, 4s, 8s…) with some randomness, so all sessions don't retry at the same moment. |
| **Empty-reply rescue** | If the stream came back empty, ask once more without streaming. |
| **Doom-loop guard** | If the model keeps asking for the exact same thing (or loops A, B, A, B… through the same 2–4 calls three times), say "that won't change, try something else." The turn keeps going; the model has to adapt. |
| **Parallel batches** | Reading 5 files? Do it at once (up to 8). Writing or running shell? One at a time, in order. |
| **Orphan fixing** | Every "call tool" in history always gets a matching "result", even after a crash or stop, so the model never gets confused. |
| **Panic catcher** | A tool crashing doesn't crash the kitchen. It becomes an error result. |
| **No round cap** | Like Codex and OpenCode, a turn runs until the model answers without asking for a tool, you press stop, or something breaks for good. The guards above keep it from spinning. |
| **Compaction** | When the conversation gets too long, older parts get summarized so it still fits. If compacting stops freeing space several times in a row, the turn ends with an error instead of looping. |
| **Spend budget** | Optional dollar limit per session (composer chip, `/budget 2.50`, or a default in Settings → Analytics). When spend crosses it, tools are switched off and the model writes a short "what's done / what's left" summary, then the turn ends. It's a **tracked-cost limit, not a guaranteed cap**: it only counts the cost your provider reports, and the call that crosses it (plus the summary) still runs. If the provider reports no cost, you get a warning that the limit isn't enforced; if the usage record got damaged, spend shown is only "at least" this much. |

**Steering.** While a turn runs, the composer shows a **Steer** button (or press ⌘/Ctrl+Enter). Your message is slipped into the *running* turn: the agent reads it before its next model call, so you can correct course without stopping. Plain Enter still queues a separate follow-up turn.

---

## 5. Tools (the knives and pans)

```mermaid
flowchart LR
    subgraph Builtin["🔧 Built-in tools"]
        read["read"]
        write["write"]
        edit["edit (fuzzy match)"]
        shell["shell (foreground/background jobs)"]
        todo["todo (plan checklist)"]
        ask["ask (asks you a question)"]
        memory["memory"]
        skill["skill"]
        ss["session_search"]
    end
    subgraph MCP["🔌 MCP tools"]
        m1["mcp__github__create_issue"]
        m2["mcp__dummy__echo"]
    end
    Registry["Tool registry"] --> Builtin
    Registry --> MCP
    Switches["🎚️ Tool switches<br/>session > global"] --> Filter["Filter"]
    Registry --> Filter --> Model["Tools the model sees<br/>this turn"]
    Plan["Plan mode?<br/>read-only only"] --> Filter
```

### Tool switches (on/off)

Every tool can be switched on or off in two places:

- **Global** (Settings → Tools): the default for **everything**.
- **Session** (the composer's **Tools** menu or `/tools`): **Default / On / Off** for **this session only**.

**Rule: the session wins.** If global says "shell OFF" but this session says "shell ON", shell is on here.

```mermaid
flowchart TD
    Q["Is tool X on for this session?"] --> S{"Session switch set?"}
    S -- "On" --> ON["✅ ON"]
    S -- "Off" --> OFF["❌ OFF"]
    S -- "not set (Default)" --> G{"Global switch set?"}
    G -- "Off" --> OFF
    G -- "On / not set" --> ON
```

### "New tools arrived!" (protecting the prompt cache)

Models remember the beginning of a conversation (the **prompt cache**) to go faster and cheaper.
If the tool list suddenly changes in the middle, that memory is thrown away.
So when a new MCP server shows up in an **existing** session:

```mermaid
sequenceDiagram
    participant MCP as New MCP server
    participant Core
    participant UI
    actor You
    MCP->>Core: ready, with 2 new tools
    Core->>Core: next turn: these are NEW for this session → hold them back
    Core-->>UI: tools/pending {sessionId, [echo, crash]}
    UI->>You: card: "2 new tools from dummy — adding rebuilds the cache"
    alt Add now
        You->>UI: click "Add now"
        UI->>Core: toolsAccept
        Note over Core: from the NEXT turn, the tools are included
    else Later
        You->>UI: click "Later"
        UI->>You: small "2 new" chip next to Tools (click to see the card again)
    end
```

**Brand-new sessions** get every tool right away, because there's no cache to protect yet.

---

## 6. MCP servers (guest chefs)

MCP (Model Context Protocol) lets other programs lend Paladin more tools.

```mermaid
flowchart LR
    Global["🌍 Global mcp.json<br/>&lt;data&gt;/config/mcp.json"] --> Mgr
    Folder["📁 Folder mcp.json<br/>&lt;project&gt;/.paladin/mcp.json"] --> Trust{"Trusted?"}
    Trust -- "no" --> Wait["⏸️ Waits — banner:<br/>'Trust this folder?'"]
    Trust -- "yes" --> Mgr["MCP manager"]
    Mgr --> Stdio["stdio servers<br/>(npx, uvx, docker…)"]
    Mgr --> Http["http servers<br/>(URL)"]
    Stdio --> Tools["mcp__server__tool"]
    Http --> Tools
```

- **Two places for config:** global (everywhere) and per-folder. If a folder server has the same name as a global one, the folder version **replaces** it in that folder.
- **Trust:** a folder's `mcp.json` could have been written by anyone (e.g. it came with a cloned repo), so it does nothing until you click **Trust**.
- **Lazy start:** a server starts when it's first needed. Global ones start at boot.
- **Crash recovery:** if a server dies, it restarts on the next use. Its error log is kept (with a `--- restarted ---` line) so you can see why.
- **Paste to add:** Settings → MCP → **Add server**. Paste `npx -y @modelcontextprotocol/server-github`, a URL, a `claude mcp add …` line or an `mcp.json` snippet. Paladin works out a name, shows a preview you can edit, and saves it.

---

## 7. Permissions, approvals and the sandbox (safety)

Before any risky tool runs, it goes through the **permission gate**.

```mermaid
flowchart TD
    Call["Model wants:<br/>shell 'npm install left-pad'"] --> Classify["Classify:<br/>read? write? shell? MCP?"]
    Classify --> Rules{"Saved rule?<br/>(session → folder → global)"}
    Rules -- "allow" --> Sandbox
    Rules -- "deny" --> No["❌ Refused<br/>(model is told why)"]
    Rules -- "none" --> Preset{"Preset?"}
    Preset -- "Read-only" --> RO{"Read-only<br/>action?"}
    RO -- yes --> Sandbox
    RO -- no --> Ask
    Preset -- "Auto (default)" --> Safe{"On the safe list?"}
    Safe -- yes --> Sandbox
    Safe -- no --> Ask["🙋 Approval card in chat:<br/>Once / This session /<br/>This folder / Deny"]
    Preset -- "Full access" --> Run["Run unconfined"]
    Ask -- "allow" --> Sandbox["🧱 Run in OS sandbox<br/>(macOS: Seatbelt, Linux: Landlock)"]
    Ask -- "deny" --> No
    Sandbox --> Blocked{"Sandbox blocked it?"}
    Blocked -- yes --> Escalate["🙋 'The sandbox blocked a write'<br/>Allow that folder (session / project / everywhere)<br/>or rerun once without the sandbox"]
    Escalate -- "allow folder" --> Sandbox
    Blocked -- no --> OK["✅ Result"]
```

- **The three presets** (the shield chip in the composer): **Read-only**, **Auto**, **Full access**.
- **Approval cards** pause **only that session**. Other sessions keep cooking, and the sidebar shows a shield on the waiting one.
- **"Remember" answers** become rules saved for the session, the folder or globally.
- **The sandbox** is a fence: inside it, shell commands can only write inside your project folder (plus temp and package caches). They can still read everything.
- **Opening a gate in the fence:** if a command bumps into the fence, the card names the folder it wanted and offers to open just that one, for the session, the project or everywhere. The command then reruns, still fenced. An "allow writes to `/some/dir`" rule in Settings → Permissions does the same ahead of time (it also covers the write/edit tools). "Rerun without the sandbox" is the one-off "climb over the fence" option.
- **Peeking outside the kitchen:** the fence doesn't stop *reading*, so the gate does. Reading a file outside your project (with the `read` tool, or a "look-only" command like `cat ~/.ssh/...`) asks first and can remember "reads in this folder". It's best effort: Paladin only looks at the paths written in the command.
- **No secret-spilling shortcuts:** `env`, `printenv`, `go env` and `git config` are not "safe" and are refused in plan mode, and shell output, errors and logs blur things that look like keys or passwords (pattern-based, so it can miss odd ones).
- **MCP tools are strangers:** a server saying "this tool is read-only" is just a claim. Paladin still asks (unless you made a rule), runs MCP calls one at a time, and in plan mode asks for every MCP call.

### Hooks (your own kitchen rules 📋)

A **hook** is a little script of yours that Paladin runs at certain moments.
It's like taping a rule to the kitchen wall: "before anyone uses the big knife, check with me".

```mermaid
flowchart LR
    Start["SessionStart<br/>(first turn)"] --> Prompt["UserPromptSubmit<br/>(you hit Enter)"]
    Prompt --> Pre["PreToolUse<br/>(before a tool)"]
    Pre -- "exit 2 / deny" --> Blocked["❌ Tool refused,<br/>model told why"]
    Pre -- "ok" --> Gate["Permission gate<br/>+ sandbox"] --> Tool["🔧 Tool runs"] --> Post["PostToolUse<br/>(after a tool)"]
    Post --> More{"More to do?"}
    More -- yes --> Pre
    More -- no --> Stop["Stop<br/>(turn about to end)"]
    Stop -- "block + reason" --> Again["🔁 Keep going<br/>(up to 8 times)"] --> Pre
    Stop -- "ok" --> Done["✅ Turn ends"]
    Compact["PreCompact / PostCompact<br/>(around a summary)"]
```

- **Same format as Codex and Claude Code.** Paladin reads its own `hooks.json` (global, and `.paladin/hooks.json` in a folder), plus a folder's `.codex/hooks.json` and `.claude/settings.json`.
- **How a hook talks back:** it gets a JSON note on stdin (the tool, its input, the prompt…). Exit `2` blocks the action (stderr is the reason). Printing JSON can add context for the model, allow or ask for a tool, or stop the turn.
- **Hooks never loosen safety.** A hook saying "allow" skips the approval card, but a deny rule or the sandbox still applies. "ask" forces a card.
- **Trust:** hooks run your commands **outside the sandbox**, so a folder's hook files stay off until you trust them in Settings → Hooks. Trust is pinned to the file's content, so if the file changes it needs trust again.
- **What you see:** a quiet one-line row in the turn ("PreToolUse hook blocked: …"). Settings → Hooks lists every file and handler, with on/off switches and an editor.

---

## 8. Undo and worktrees (time travel ⏪ and parallel universes 🌌)

### Undo with snapshots

Before a turn changes files, Paladin takes a **photo** of the folder.
It uses a hidden git repo under `<data>/snapshots/`, so your own `.git` is never touched.

```mermaid
flowchart LR
    T1["Turn 1<br/>📸 photo A"] --> T2["Turn 2<br/>📸 photo B"] --> T3["Turn 3<br/>📸 photo C"]
    T3 -. "/undo or 'Revert to here'" .-> Back["Files go back to photo B,<br/>messages after turn 2 hidden"]
    Back -. "Unrevert" .-> T3
```

- **Revert preview** shows which files would change, and warns about **conflicts** (files you edited by hand since).
- **Unrevert** brings it all back, as long as you haven't sent a new message yet.

### Worktrees

A **worktree** is a second copy of your git repo on its own branch (`paladin/…`).
A session can work there without touching your main checkout.

```mermaid
flowchart LR
    Main["📁 your repo<br/>(branch main)"] -- "New session in worktree<br/>or Fork → worktree" --> WT["📁 /…/worktrees/feature<br/>(branch paladin/feature)"]
    WT -- "agent edits here only" --> WT
    WT -- "Merge back" --> Main
    WT -- "delete session<br/>(clean → removed,<br/>dirty → kept)" --> Gone["🗑️"]
```

**Fork** copies a conversation into a new session. It can also give the copy its own worktree, to try a different idea.

---

## 9. Memory (the shared recipe notebook 📒)

Inspired by hermes-agent. Paladin remembers things **across sessions**.

```mermaid
flowchart TB
    subgraph MemoryRoot["&lt;data&gt;/memory/"]
        User["USER.md<br/>👤 facts about you<br/>(e.g. 'prefers uv')"]
        Glob["MEMORY.md<br/>🌍 app-wide notes"]
        subgraph Projects["projects/&lt;folder-key&gt;/"]
            P1["MEMORY.md<br/>📁 notes for THIS folder"]
            F1["folder.txt (which folder)"]
        end
    end
    S1["Session 1 (folder X)"] --> P1
    S2["Session 2 (folder X)"] --> P1
    S3["Session 3 (folder Y)"] --> Other["projects/Y/MEMORY.md"]
    S1 & S2 & S3 --> User
    S1 & S2 & S3 --> Glob
```

- The model uses the **`memory` tool** to add, replace or remove notes. Each notebook has a size limit, so it stays short.
- **Many sessions, same folder:** they share one folder notebook. Writes take a **file lock**, so two sessions can't scribble over each other.
- **Frozen snapshot per session:** at the start of a session, memory is copied into its prompt and stays fixed. A note written by session 2 doesn't suddenly change session 1's prompt (that would break its cache). Session 1 sees it next time it refreshes.
- You can view and edit everything in **Settings → Memory**, or with `/memories`.

---

## 10. Slash commands, skills and prompts

Type `/` in the composer and a menu pops up:

```mermaid
flowchart LR
    Slash["/ typed"] --> Menu["Slash menu<br/>(fuzzy search, arrows, Enter)"]
    Menu --> Local["Local commands<br/>(UI only): /tools /mcp /memories<br/>/models /usage /budget /settings /clear /help /rename /btw"]
    Menu --> Agent["Agent commands<br/>(sent to the core): /compact /plan /build<br/>/undo /fork"]
    Menu --> Prompts["Prompt commands<br/>(.md files): /review /init …"]
    Menu --> Skills["Skills<br/>(SKILL.md folders): /skill name"]
```

- **Plan mode** (`/plan`): the agent only gets read-only tools. It looks around and proposes a plan.
- **Build mode** (`/build`): back to normal.
- **Skills**: packs of instructions the model can pull in when needed (with the `skill` tool), or that you attach to a turn.
- **`/btw <question>`**: whisper a side question to the cook without interrupting. It works **while a turn is running**: Paladin copies the conversation so far (including tools still running), asks the model once with no tools, and shows the answer in a card you can dismiss. It is **never saved** to the chat, but its cost counts toward usage and the budget.

---

## 11. Providers and models (the food supplier ☁️)

```mermaid
flowchart LR
    Agent["Agent loop"] --> Prov{"Provider type"}
    Prov --> OAI["OpenAI-compatible<br/>(LiteLLM :4000)"]
    Prov --> Resp["OpenAI Responses"]
    Prov --> Anth["Anthropic"]
    Prov --> Goog["Google"]
    OAI & Resp & Anth & Goog --> Usage["Token & cost counting<br/>→ usage.json"]
```

- Providers live in `<data>/config/providers.json`. Keys are kept in the OS keychain, not in plain text.
- **OpenRouter and OpenCode are never pinged automatically.** Their model lists only refresh when you press refresh.
- Each session tracks **tokens, cost and context size**. That's the `12.5% · $0.0042` pill at the top.
- **Thinking level (how hard the cook thinks 🧠):** the ⚡ chip next to the model button opens a slider: *Default* (the provider decides, nothing is sent), then only the levels that model supports (Off, Minimal, Low, Medium, High, Extra High, Max). The choice is saved **per model for the whole app** in `settings.json` and is sent as `reasoning_effort`. Paladin guesses the supported levels from the model's name, so a model it doesn't recognize gets no chip. If a provider rejects the level, Paladin retries once without it and shows a notice. While the model thinks, a **Thinking** block streams in the chat and folds into **Thought** when the answer starts. The thinking is saved in history but never sent back to the model.

---

## 12. Where everything is saved 💾

`<data>` is `~/Library/Application Support/paladin` on macOS (or `$PALADIN_DATA_DIR`).

```text
<data>/
├── config/
│   ├── providers.json      ← AI providers (keys in keychain)
│   ├── models.json
│   ├── settings.json       ← e.g. maxConcurrentTurns
│   ├── SYSTEM_PROMPT.md    ← the editable system prompt
│   ├── mcp.json            ← global MCP servers
│   └── tools.json          ← global tool switches
├── permissions/
│   ├── global.json         ← saved allow/deny rules (everywhere)
│   └── folders/<key>.json  ← rules for one folder
├── sessions/
│   └── <encoded folder>/
│       └── <session id>/
│           ├── messages.jsonl   ← the conversation, one message per line
│           ├── meta.json        ← title, model, worktree, revert, tool switches…
│           └── usage.json       ← tokens & cost
├── memory/
│   ├── USER.md
│   ├── MEMORY.md
│   └── projects/<key>/MEMORY.md
└── snapshots/               ← hidden git repos for undo
```

Files are written **atomically**: a temp file first, then renamed into place.
So a crash in the middle never leaves a half-written file.

---

## 13. The renderer's parts (the dining room 🖥️)

Every file in `desktop-app/src/features/` owns one thing:

```mermaid
flowchart TB
    main["main.ts<br/>boots everything"] --> sessions & chat & settings & live
    sessions["sessions.ts<br/>sidebar list, open/new/delete"]
    chat["chat.ts<br/>send, ensureSession"]
    live["live.ts<br/>applies events to the right session"]
    live --> turnView["turnView.ts<br/>draws one turn"]
    turnView --> toolRun["toolRun.ts / toolBodies.ts<br/>pretty tool rows (diffs, terminal…)"]
    chat --> slash["slash.ts<br/>/ menu"]
    chat --> mentions["mentions.ts<br/>@file tagging"]
    settings["settings.ts<br/>the Settings modal"] --> mcp["mcp.ts"] & toolSwitches["toolSwitches.ts"] & memory["memory.ts"] & permissions["permissions.ts"]
    approvals["approvals.ts<br/>approval cards"]
    undo["undo.ts / worktrees.ts"]
    workingBar["workingBar.ts<br/>'Working…' bar"]
    resources["resources.ts<br/>CPU/RAM per session"]
    todos["todos.ts<br/>plan checklist above composer"]
```

**`live.ts` is the mail sorter.** Every event has a `sessionId`. If it's for the session on screen, it's drawn right away.
If not, it's remembered, and only the sidebar row changes (spinner, unread dot).

**The sidebar is a drawer.** In the Agent view it floats over the chat, like a sheet of paper laid on top, so the chat never squeezes or shifts. A click outside, Escape, or picking a session puts it away; clicking the Agent button on the left brings it back (there's no separate sidebar button). The Chat view (ChatGPT, Claude…) has no sidebar at all: those pages are real mini-browsers stuck on top of the window, and nothing can be drawn over them, so you switch between them with tabs along the top, like a browser.

---

## 14. The whole thing in one picture

```mermaid
flowchart TB
    You(["🧑 You"]) --> UI

    subgraph App["Paladin desktop app"]
        UI["Renderer<br/>sidebar · chat · composer · settings"]
        EM["Electron main<br/>(the waiter)"]
        UI <--> EM
    end

    EM <-- "JSON lines<br/>stdin / stdout" --> Host

    subgraph Host["Go core: agent-host"]
        RPCs["RPC methods"] --> RT["Runtime"]
        RT --> Hub["Turn hub<br/>≤4 at once, FIFO per session"]
        Hub --> L1["Agent loop<br/>session A"]
        Hub --> L2["Agent loop<br/>session B"]
        L1 & L2 --> Gate["Permission gate"] --> SB["Sandbox"] --> Tools["Tools"]
        Tools --> MCPm["MCP manager"] --> Servers["MCP servers"]
        RT --> Mem["Memory"]
        RT --> Snap["Snapshots"]
        RT --> WT["Worktrees"]
        RT --> TS["Tool switches"]
    end

    L1 & L2 <--> LLM["☁️ AI model"]
    Host <--> Disk[("💾 &lt;data&gt;/")]
    Hub -- "events (seq-numbered)" --> EM
```

### The 5 rules that make it robust

1. **Answer fast, work in the background.** Requests return right away, and progress arrives as events.
2. **Every session is its own lane.** One stuck or failing session never blocks the others.
3. **Never lose work.** Save on every turn, write atomically, save on quit, pair every tool call with a result.
4. **Expect failure.** Retries with backoff, watchdogs, panic catchers, MCP restarts, doom-loop guard.
5. **Ask before danger.** Permission presets, approval cards, sandbox, folder trust, and asking before the tool list changes.
