// paladin-stub.js: window.paladin test double for Playwright e2e.
// Holds the shared fixtures and the __e2e* hooks.
// Specs may wrap window.paladin.invoke to override cases per-test.

async function __paladinBaseInvoke(cmd, args) {
    switch (cmd) {
      case "resolveWorkspace":
        return (args && args.cwd) || "/home/tester/project";
      case "listSessions":
        return [
          {
            sessionId: "sess-notify-1",
            cwd: "/home/tester/project",
            createdAt: Math.floor(Date.now() / 1000),
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: 6,
            title: "notify demo",
            ...(window.__e2eSessionExtra || {}),
          },
        ].concat(window.__e2eExtraSessions || []);
      case "listSkills":
        return [
          {
            name: "reviewer",
            description: "Reviews code",
            source: "global",
            path: "/home/tester/.config/skills/reviewer/SKILL.md",
          },
          {
            name: "deployer",
            description: "Ships it",
            source: "workspace",
            path: "/home/tester/project/.paladin/skills/deployer/SKILL.md",
          },
        ];
      case "listCommands":
        return [
          { name: "init", description: "Create or improve AGENTS.md for this project", source: "builtin" },
          { name: "review", description: "Review the current changes", argumentHint: "[focus]", mode: "plan", source: "builtin" },
          { name: "ship", description: "Release checklist", argumentHint: "<version>", source: "workspace" },
        ];
      case "memoryGet": {
        window.__e2eMemory = window.__e2eMemory || {};
        const key = args.target + ":" + (args.cwd || "");
        const entries = window.__e2eMemory[key] || (args.target === "user" ? ["Prefers terse answers"] : []);
        const limits = { user: 1500, global: 2500, project: 4000 };
        return { target: args.target, cwd: args.cwd || undefined, path: `/data/memory/${args.target}.md`, entries, used: entries.join("\n§\n").length, limit: limits[args.target] || 2500 };
      }
      case "memorySet": {
        window.__e2eMemory = window.__e2eMemory || {};
        window.__e2eMemorySets = (window.__e2eMemorySets || []).concat([args]);
        const key = args.target + ":" + (args.cwd || "");
        window.__e2eMemory[key] = args.entries || [];
        const limits = { user: 1500, global: 2500, project: 4000 };
        return { target: args.target, cwd: args.cwd || undefined, path: `/data/memory/${args.target}.md`, entries: args.entries || [], used: (args.entries || []).join("\n§\n").length, limit: limits[args.target] || 2500 };
      }
      case "answerApproval": {
        window.__e2eApprovalAnswers = (window.__e2eApprovalAnswers || []).concat([args]);
        const waiter = (window.__e2eApprovalWaits || {})[args.callId];
        if (!waiter) throw new Error("no pending approval " + args.callId);
        delete window.__e2eApprovalWaits[args.callId];
        waiter(args.decision);
        return true;
      }
      case "permissionsGet":
      case "permissionsSet": {
        const p = (window.__e2ePerms = window.__e2ePerms || { global: [], folders: {}, kind: "seatbelt" });
        if (cmd === "permissionsSet") {
          window.__e2ePermSets = (window.__e2ePermSets || []).concat([JSON.parse(JSON.stringify(args))]);
          if (args.scope === "global") p.global = args.rules || [];
          else if (args.scope === "folder") {
            if ((args.rules || []).length) p.folders[args.cwd] = args.rules;
            else delete p.folders[args.cwd];
          } else throw new Error("scope must be global or folder");
        }
        const cwd = (args && args.cwd) || "/home/tester/project";
        return {
          global: p.global,
          folder: p.folders[cwd] || [],
          session: [],
          folders: Object.entries(p.folders).map(([c, rules]) => ({ cwd: c, rules })),
          sandbox: { kind: p.kind },
          presets: ["read-only", "auto", "full-access"],
        };
      }
      case "memoryProjects":
        return [{ cwd: "/home/tester/project", key: "project-1234", entries: 1, used: 20, limit: 4000 }];
      case "memoryRefreshSnapshot":
        window.__e2eSnapshotRefreshes = (window.__e2eSnapshotRefreshes || 0) + 1;
        return true;
      case "gitInfo":
        return window.__e2eGit || { repo: false, hasCommits: false };
      case "revertPreview":
        window.__e2ePreviews = (window.__e2ePreviews || []).concat([args]);
        return { index: args.index, turns: 1, files: [], conflicts: [], snapshots: true, ...(window.__e2ePreview || {}) };
      case "revertSession": {
        window.__e2eReverts = (window.__e2eReverts || []).concat([args]);
        window.__e2eRevertedAt = args.index;
        return { index: args.index, message: "run the slow job", restored: [], skipped: [], snapshots: true, ...(window.__e2eRevertResult || {}) };
      }
      case "unrevertSession":
        window.__e2eUnreverts = (window.__e2eUnreverts || 0) + 1;
        window.__e2eRevertedAt = undefined;
        return true;
      case "worktreeStatus":
        return window.__e2eWorktreeStatus || { exists: true, changed: 0, ahead: 0, branch: "paladin/demo", into: "main", path: "/wt/demo", repoRoot: "/home/tester/project" };
      case "worktreeMerge":
        window.__e2eMerges = (window.__e2eMerges || []).concat([args]);
        if (window.__e2eMergeError) throw new Error(window.__e2eMergeError);
        return window.__e2eMergeResult || { merged: true, nothingToMerge: false, commit: "abc1234", conflicts: [], into: "main" };
      case "deleteSession":
        window.__e2eDeletes = (window.__e2eDeletes || []).concat([args]);
        return true;
      case "forkSession":
        window.__e2eForks = (window.__e2eForks || []).concat([args]);
        if (args && args.worktree) {
          return { sessionId: "sess-wt-1", cwd: "/wt/demo", title: "notify demo (fork)", messageCount: 2, createdAt: Math.floor(Date.now() / 1000), worktree: { repoRoot: "/home/tester/project", path: "/wt/demo", branch: "paladin/demo", baseBranch: "main", origin: "/home/tester/project" } };
        }
        return { sessionId: "sess-fork-1", cwd: "/home/tester/project", title: "notify demo (fork)", messageCount: 2, createdAt: Math.floor(Date.now() / 1000) };
      case "renameSession":
        window.__e2eRenames = (window.__e2eRenames || []).concat([args]);
        return true;
      case "openSession":
        window.__e2eOpens = (window.__e2eOpens || []).concat([args]);
        return {
          sessionId: (args && args.sessionId) || "sess-notify-1",
          cwd: "/home/tester/project",
          provider: "Opencode Go",
          model: "openai/gpt-4o",
          ...((window.__e2eOpenExtra || {})[(args && args.sessionId) || ""] || {}),
        };
      case "sessionHistory": {
        const at = Math.floor(Date.now() / 1000);
        if (window.__e2eRevertedAt === 0) {
          return [];
        }
        return [
          { role: "user", content: "run the slow job", createdAt: at, index: 0 },
          { role: "assistant", content: "Starting the slow job.", createdAt: at },
          {
            role: "tool",
            content: '{"job_id":"job-1-1","status":"running"}',
            createdAt: at,
            name: "shell",
            arguments:
              '{"action":"run","cmd":"sleep 60","background":true,"notify":true}',
            status: "ok",
          },
          { role: "assistant", content: "Meanwhile done.", createdAt: at },
          {
            role: "tool",
            content:
              '{"notice":"background job finished","job_id":"job-1-1","status":"done","exit_code":0,"output":"hi"}',
            createdAt: at,
            name: "shell",
            arguments: '{"action":"notify","job_id":"job-1-1"}',
            status: "ok",
          },
          { role: "assistant", content: "It printed hi.", createdAt: at },
        ];
      }
      case "sessionUsage":
        return (
          window.__e2eUsage || {
            sessionId: "sess-notify-1",
            cwd: "/home/tester/project",
            model: "beam-free",
            contextWindow: 200000,
            contextKnown: true,
            calls: 3,
            promptTokens: 20000,
            completionTokens: 5000,
            reasoningTokens: 0,
            cacheReadTokens: 0,
            cacheWriteTokens: 0,
            totalTokens: 25000,
            lastInputTokens: 25000,
            cost: 0.0042,
            listCost: 0.008,
            hasCost: true,
            fillPercent: 12.5,
            daily: [
              {
                date: new Date(Date.now() - 86400000).toISOString().slice(0, 10),
                calls: 1,
                promptTokens: 5000,
                completionTokens: 1000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 6000,
                cost: 0.0012,
                listCost: 0.002,
                hasCost: true,
              },
              {
                date: new Date().toISOString().slice(0, 10),
                calls: 2,
                promptTokens: 15000,
                completionTokens: 4000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 19000,
                cost: 0.003,
                listCost: 0.006,
                hasCost: true,
              },
            ],
            models: [
              {
                model: "beam-free",
                contextWindow: 200000,
                contextKnown: true,
                calls: 2,
                promptTokens: 15000,
                completionTokens: 4000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 19000,
                cost: 0.003,
                listCost: 0.006,
                hasCost: true,
                fillPercent: 9.5,
              },
              {
                model: "kimi-k2.6",
                contextWindow: 262144,
                contextKnown: true,
                calls: 1,
                promptTokens: 5000,
                completionTokens: 1000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 6000,
                cost: 0.0012,
                listCost: 0.002,
                hasCost: true,
                fillPercent: 2.29,
              },
            ],
          }
        );
      case "sessionTodos":
        return (
          window.__e2eTodos || [
            { content: "Plan the work", status: "finished" },
            { content: "Build the hat", status: "in_progress" },
            { content: "Run the checks", status: "not_started" },
          ]
        );
      case "overallUsage":
        return (
          window.__e2eOverall || {
            sessionCount: 2,
            sessionsWithUsage: 2,
            calls: 3,
            promptTokens: 20000,
            completionTokens: 5000,
            reasoningTokens: 400,
            cacheReadTokens: 1200,
            cacheWriteTokens: 300,
            totalTokens: 26900,
            cost: 0.0042,
            listCost: 0.008,
            hasCost: true,
            updatedAt: Math.floor(Date.now() / 1000),
            models: [
              {
                model: "beam-free",
                contextWindow: 0,
                contextKnown: false,
                calls: 2,
                promptTokens: 15000,
                completionTokens: 4000,
                reasoningTokens: 400,
                cacheReadTokens: 1200,
                cacheWriteTokens: 300,
                totalTokens: 20900,
                cost: 0.003,
                listCost: 0.006,
                hasCost: true,
              },
              {
                model: "kimi-k2.6",
                contextWindow: 0,
                contextKnown: false,
                calls: 1,
                promptTokens: 5000,
                completionTokens: 1000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 6000,
                cost: 0.0012,
                listCost: 0.002,
                hasCost: true,
              },
            ],
            sessions: [
              {
                sessionId: "sess-notify-1",
                title: "notify demo",
                model: "beam-free",
                createdAt: Math.floor(Date.now() / 1000) - 86400,
                updatedAt: Math.floor(Date.now() / 1000),
                calls: 2,
                promptTokens: 15000,
                completionTokens: 4000,
                reasoningTokens: 400,
                cacheReadTokens: 1200,
                cacheWriteTokens: 300,
                totalTokens: 20900,
                cost: 0.003,
                listCost: 0.006,
                hasCost: true,
              },
              {
                sessionId: "sess-other-2",
                title: "second session",
                model: "kimi-k2.6",
                createdAt: Math.floor(Date.now() / 1000) - 172800,
                updatedAt: Math.floor(Date.now() / 1000) - 86400,
                calls: 1,
                promptTokens: 5000,
                completionTokens: 1000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 6000,
                cost: 0.0012,
                listCost: 0.002,
                hasCost: true,
              },
            ],
            daily: [
              {
                date: new Date(Date.now() - 86400000).toISOString().slice(0, 10),
                calls: 1,
                promptTokens: 5000,
                completionTokens: 1000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
                totalTokens: 6000,
                cost: 0.0012,
                listCost: 0.002,
                hasCost: true,
              },
              {
                date: new Date().toISOString().slice(0, 10),
                calls: 2,
                promptTokens: 15000,
                completionTokens: 4000,
                reasoningTokens: 400,
                cacheReadTokens: 1200,
                cacheWriteTokens: 300,
                totalTokens: 20900,
                cost: 0.003,
                listCost: 0.006,
                hasCost: true,
              },
            ],
          }
        );
      case "listProviders":
        window.__e2eLists = (window.__e2eLists || 0) + 1;
        if (!window.__e2eProviders) {
          window.__e2eProviders = [
            {
              name: "Opencode Go",
              apiType: "openai",
              apiUrl: "https://opencode.ai/inference/go/openai/v1",
              model: "",
              models: [],
              default: true,
              hasKey: false,
            },
            {
              name: "openrouter",
              apiType: "openai",
              apiUrl: "https://openrouter.ai/api/v1",
              model: "",
              models: ["openai/gpt-4o"],
              default: false,
              hasKey: false,
            },
            {
              name: "local",
              apiType: "openai",
              apiUrl: "http://localhost:11434/v1",
              model: "",
              models: ["other/llama3.2"],
              default: false,
              hasKey: true,
            },
          ];
        }
        window.__e2eProviders[0].models =
          window.__e2eLists > 1
            ? [
                "anthropic/claude-fable-20990101",
                "anthropic/claude-3-opus",
                "openai/gpt-4o",
              ]
            : ["~anthropic/claude-fable", "anthropic/claude-3-opus", "openai/gpt-4o"];
        return window.__e2eProviders;
      case "refreshProviders":
        if (args && args.name) {
          return { name: args.name, count: 3, models: [] };
        }
        return false;
      case "run_turn": {
        const inner = args && args.args ? args.args : args || {};
        window.__e2eChats = window.__e2eChats || [];
        window.__e2eChats.push({
          sessionId: inner.sessionId,
          message: inner.message,
          resume: !!inner.resume,
          skill: inner.skill ?? null,
          command: inner.command ?? null,
          mode: inner.mode || "",
          compact: !!inner.compact,
        });
        if (inner.compact) {
          return { sessionId: inner.sessionId || "sess-notify-1", reply: "", stopped: false, needsContinue: false };
        }
        if (inner.resume) {
          return {
            sessionId: inner.sessionId || "sess-notify-1",
            reply: "Resumed and finished.",
            stopped: false,
            needsContinue: false,
          };
        }
        if (window.__e2eChatPaused) {
          window.__e2eChatPaused = false;
          return {
            sessionId: inner.sessionId || "sess-notify-1",
            reply: "Partial work before the cap.",
            stopped: false,
            needsContinue: true,
          };
        }
        return {
          sessionId: inner.sessionId || "sess-notify-1",
          reply: "Stub reply to: " + (inner.message || ""),
          stopped: false,
          needsContinue: false,
        };
      }
      case "submitChat":
        return __e2eHub.submit(args || {});
      case "turns":
        return __e2eHub.list();
      case "attach":
        return __e2eHub.attach(args || {});
      case "cancelTurn":
        return __e2eHub.cancel(args && args.turnId);
      case "stopChat":
        return __e2eHub.stop(args && args.sessionId);
      case "setDefaultProvider":
        for (const p of window.__e2eProviders || []) {
          p.default = p.name === args.name;
        }
        return window.__e2eProviders;
      case "addProvider":
        if (!args.apiKey) throw new Error("api key is required");
        window.__e2eProviders.push({
          name: args.name,
          apiType: args.apiType,
          apiUrl: args.apiUrl,
          model: "",
          models: [],
          default: false,
          hasKey: true,
        });
        return window.__e2eProviders[window.__e2eProviders.length - 1];
      case "updateProvider":
        for (const p of window.__e2eProviders || []) {
          if (p.name === args.name) {
            p.apiUrl = args.apiUrl;
            p.apiType = args.apiType;
            if (args.apiKey) p.hasKey = true;
            return p;
          }
        }
        throw new Error("provider not found");
      case "getWorkspace":
        return window.localStorage.getItem("e2e-workspace") || "";
      case "setWorkspace":
        window.localStorage.setItem("e2e-workspace", args.path);
        return args.path;
      case "getPrompt":
        return window.__e2ePrompt || { systemPrompt: "stub system prompt" };
      case "setPrompt":
        window.__e2ePrompt = { systemPrompt: args.systemPrompt || "stub system prompt" };
        return window.__e2ePrompt;
      case "resetPrompt":
        window.__e2ePrompt = { systemPrompt: "stub system prompt" };
        return window.__e2ePrompt;
      case "fsHome":
        return "/home/tester";
      case "fileComplete": {
        const q = (args && args.query) || "";
        const all = [
          { path: "src/main.go", display: "main.go", resolvedPath: "/home/tester/project/src/main.go", outside: false, isDir: false },
          { path: "src/server/", display: "server/", resolvedPath: "/home/tester/project/src/server", outside: false, isDir: true },
          { path: "src/server/app.go", display: "app.go", resolvedPath: "/home/tester/project/src/server/app.go", outside: false, isDir: false },
          { path: "README.md", display: "README.md", resolvedPath: "/home/tester/project/README.md", outside: false, isDir: false },
          { path: "../sibling/outside.txt", display: "outside.txt", resolvedPath: "/home/tester/sibling/outside.txt", outside: true, isDir: false },
        ];
        const norm = (p) => p.replace(/\\/g, "/").replace(/\/+$/, "");
        const want = norm(q).toLowerCase();
        return all.filter((e) => norm(e.path).toLowerCase().includes(want));
      }
      case "fsList": {
        const at = (args && args.path) || "/home/tester";
        return {
          currentPath: at,
          parentPath: "/home",
          directories: [
            { name: ".hidden-dir", path: `${at}/.hidden-dir` },
            { name: "project", path: `${at}/project` },
          ],
          files: [
            { name: ".env", path: `${at}/.env` },
            { name: "README.md", path: `${at}/README.md` },
          ],
        };
      }
      case "resources":
        return window.__e2eRes || { at: 0, supported: false, self: { cpu: 0, rss: 0, procs: 0 }, sessions: {} };
      default:
        if (String(cmd).startsWith("plugin:")) {
          return 1;
        }
        throw new Error(`e2e stub: unhandled command ${cmd}`);
    }

}

window.__paladinBaseInvoke = __paladinBaseInvoke;

// __e2eHub mirrors the sidecar's turn hub: FIFO per session, at most
// __e2eTurnCap sessions at once, turn/* + chat/* events on the onEvent
// bus. Each turn runs through window.__e2eRunTurn, so specs that replace
// it (or set __e2eChatEvents) script what it streams.
const __e2eHub = (() => {
  const listeners = new Set();
  let seq = 0;
  let next = 0;
  const turns = [];
  const logs = new Map();
  const copy = (t) => JSON.parse(JSON.stringify(t));
  const emit = (ev) => {
    // Specs may pin a seq (re-attach tests); the counter never goes back.
    seq = ev.seq ? Math.max(seq, ev.seq) : seq + 1;
    if (!ev.seq) ev.seq = seq;
    if (ev.event.startsWith("chat/") && logs.has(ev.turnId)) logs.get(ev.turnId).push(copy(ev));
    for (const cb of [...listeners]) cb(copy(ev));
  };
  const chat = (turn, event, extra) => {
    if (turn.state !== "running") return;
    emit({ event, sessionId: turn.sessionId, turnId: turn.turnId, ...extra });
  };
  const finish = (turn, res) => {
    if (turn.state === "done") return;
    Object.assign(turn, res, { state: "done", persisted: !res.cancelled, endedAt: new Date().toISOString() });
    turns.splice(turns.indexOf(turn), 1);
    logs.delete(turn.turnId);
    emit({ event: "turn/done", sessionId: turn.sessionId, turnId: turn.turnId, turn: copy(turn) });
    pump();
  };
  const start = (turn) => {
    turn.state = "running";
    turn.startedAt = new Date().toISOString();
    logs.set(turn.turnId, []);
    emit({ event: "turn/start", sessionId: turn.sessionId, turnId: turn.turnId, turn: copy(turn) });
    const handlers = {
      onDelta: (delta) => chat(turn, "chat/delta", { delta }),
      onTool: (tool) => chat(turn, "chat/tool", { tool }),
      onRetry: (retry) => chat(turn, "chat/retry", { retry }),
      onCompact: (compaction) => chat(turn, "chat/compact", { compaction }),
      onApproval: (approval) => chat(turn, "chat/approval", { approval }),
    };
    Promise.resolve()
      .then(() => window.__e2eRunTurn({ ...turn.params }, handlers))
      .then((res) => finish(turn, { reply: res?.reply || "", stopped: !!res?.stopped, needsContinue: !!res?.needsContinue }))
      .catch((err) => finish(turn, { error: String((err && err.message) || err) }));
  };
  const pump = () => {
    const cap = window.__e2eTurnCap || 4;
    for (const t of turns) {
      if (t.state !== "queued") continue;
      const active = turns.filter((x) => x.state === "running");
      if (active.length >= cap) break;
      if (active.some((x) => x.sessionId === t.sessionId)) continue;
      start(t);
    }
  };
  const info = (t) => {
    const { params, ...rest } = t;
    return copy(rest);
  };
  return {
    on(cb) {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    emit,
    submit(args) {
      const turn = {
        turnId: `turn-${++next}`,
        sessionId: args.sessionId,
        cwd: args.cwd || "/home/tester/project",
        message: args.message || "",
        state: "queued",
        queuedAt: new Date().toISOString(),
        params: {
          sessionId: args.sessionId,
          message: args.message || "",
          resume: !!args.resume,
          cwd: args.cwd,
          provider: args.provider ?? null,
          model: args.model ?? null,
          systemPrompt: null,
          skill: args.skill ?? null,
          command: args.command ?? null,
          mode: args.mode || "",
          compact: !!args.compact,
          permissions: args.permissions || "",
        },
      };
      window.__e2eSubmits = (window.__e2eSubmits || []).concat([JSON.parse(JSON.stringify(args))]);
      if (args.command) turn.command = args.command;
      if (args.mode) turn.mode = args.mode;
      if (args.permissions) turn.permissions = args.permissions;
      if (args.compact) turn.compact = true;
      if (args.display) turn.display = args.display;
      if (args.skill) turn.skill = args.skill;
      if (args.resume) turn.resume = true;
      turns.push(turn);
      pump();
      if (turn.state === "queued") {
        emit({ event: "turn/queued", sessionId: turn.sessionId, turnId: turn.turnId, turn: info(turn) });
      }
      return info(turn);
    },
    list() {
      return turns.map(info);
    },
    async attach(args) {
      const history = await window.paladin.invoke("sessionHistory", { sessionId: args.sessionId, cwd: null });
      const running = turns.find((t) => t.sessionId === args.sessionId && t.state === "running");
      return {
        history,
        turn: running ? info(running) : null,
        events: running ? copy(logs.get(running.turnId) || []) : [],
        queued: turns.filter((t) => t.sessionId === args.sessionId && t.state === "queued").map(info),
        seq,
      };
    },
    cancel(turnId) {
      const t = turns.find((x) => x.turnId === turnId);
      if (!t) return false;
      finish(t, t.state === "queued" ? { cancelled: true } : { stopped: true, reply: "" });
      return true;
    },
    stop(sessionId) {
      const mine = turns.filter((t) => !sessionId || t.sessionId === sessionId);
      for (const t of mine.filter((x) => x.state === "queued")) finish(t, { cancelled: true });
      for (const t of mine.filter((x) => x.state === "running")) finish(t, { stopped: true, reply: "" });
      return true;
    },
  };
})();
window.__e2eHub = __e2eHub;

// __e2eRunTurn runs one stub turn for __e2eHub: it streams the scripted
// __e2eChatEvents ({event, ..., waitMs?}) through the handlers, then
// resolves with the "run_turn" fixture reply. Specs replace it to script a turn.
window.__e2eRunTurn = async (params, handlers) => {
  const events = window.__e2eChatEvents || [];
  window.__e2eChatEvents = null;
  for (const ev of events) {
    if (ev.waitMs) await new Promise((r) => setTimeout(r, ev.waitMs));
    if (ev.event === "chat/delta") handlers.onDelta(ev.delta);
    else if (ev.event === "chat/tool") handlers.onTool(ev.tool);
    else if (ev.event === "chat/retry" && handlers.onRetry) handlers.onRetry(ev.retry);
    else if (ev.event === "chat/compact" && handlers.onCompact) handlers.onCompact(ev.compaction);
    else if (ev.event === "chat/approval" && handlers.onApproval) {
      // A pending approval blocks the stub turn until answerApproval,
      // then reports the outcome like the real gate.
      handlers.onApproval(ev.approval);
      if (ev.approval.state === "pending" && ev.block !== false) {
        const decision = await new Promise((resolve) => {
          window.__e2eApprovalWaits = window.__e2eApprovalWaits || {};
          window.__e2eApprovalWaits[ev.approval.id] = resolve;
        });
        handlers.onApproval({ ...ev.approval, state: decision === "deny" ? "denied" : "approved", decision });
      }
    }
  }
  return window.__paladinBaseInvoke("run_turn", params || {});
};

window.paladin = {
  metrics: async () => window.__e2eAppMetrics || null,
  invoke: (method, params) => window.__paladinBaseInvoke(method, params || {}),
  onEvent: (cb) => __e2eHub.on(cb),
  onOpenSession: (cb) => {
    window.__e2eOpenSession = cb;
    return () => {};
  },
  takePendingOpen: async () => {
    const id = window.__e2ePendingOpen || null;
    window.__e2ePendingOpen = null;
    return id;
  },
  views: {
    ensure: async () => true,
    show: async () => true,
    setBounds: async () => {
      window.__e2eMoves = (window.__e2eMoves || 0) + 1;
      return true;
    },
    navigate: async () => true,
    reload: async () => true,
    drop: async () => true,
    setTheme: async () => true,
  },
  shell: {
    openExternal: async () => {},
  },
  windows: {
    minimize: async () => {},
    toggleMaximize: async () => {},
    close: async () => {},
    isMaximized: async () => false,
    onResized: () => () => {},
  },
};
