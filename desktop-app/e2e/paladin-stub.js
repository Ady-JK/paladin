// paladin-stub.js: window.paladin test double for Playwright e2e.
// Mirrors the old Tauri stub 1:1 (same fixtures, same __e2e* hooks).
// Specs may wrap window.paladin.invoke to override cases per-test.

async function __paladinBaseInvoke(cmd, args) {
    switch (cmd) {
      case "resolve_workspace":
        return (args && args.cwd) || "/home/tester/project";
      case "list_sessions":
        return [
          {
            sessionId: "sess-notify-1",
            cwd: "/home/tester/project",
            createdAt: Math.floor(Date.now() / 1000),
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: 6,
            title: "notify demo",
          },
        ];
      case "list_skills":
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
      case "open_session":
        return {
          sessionId: (args && args.sessionId) || "sess-notify-1",
          cwd: "/home/tester/project",
          provider: "Opencode Go",
          model: "openai/gpt-4o",
        };
      case "session_history": {
        const at = Math.floor(Date.now() / 1000);
        return [
          { role: "user", content: "run the slow job", createdAt: at },
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
      case "session_usage":
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
      case "session_todos":
        return (
          window.__e2eTodos || [
            { content: "Plan the work", status: "finished" },
            { content: "Build the hat", status: "in_progress" },
            { content: "Run the checks", status: "not_started" },
          ]
        );
      case "overall_usage":
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
      case "list_providers":
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
      case "refresh_providers":
        if (args && args.name) {
          return { name: args.name, count: 3, models: [] };
        }
        return false;
      case "chat": {
        const inner = args && args.args ? args.args : args || {};
        window.__e2eChats = window.__e2eChats || [];
        window.__e2eChats.push({
          sessionId: inner.sessionId,
          message: inner.message,
          resume: !!inner.resume,
          skill: inner.skill ?? null,
        });
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
      case "submit_chat":
        return __e2eHub.submit(args || {});
      case "turns":
        return __e2eHub.list();
      case "attach":
        return __e2eHub.attach(args || {});
      case "cancel_turn":
        return __e2eHub.cancel(args && args.turnId);
      case "stop_chat":
        return __e2eHub.stop(args && args.sessionId);
      case "set_default_provider":
        for (const p of window.__e2eProviders || []) {
          p.default = p.name === args.name;
        }
        return window.__e2eProviders;
      case "add_provider":
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
      case "update_provider":
        for (const p of window.__e2eProviders || []) {
          if (p.name === args.name) {
            p.apiUrl = args.apiUrl;
            p.apiType = args.apiType;
            if (args.apiKey) p.hasKey = true;
            return p;
          }
        }
        throw new Error("provider not found");
      case "get_workspace":
        return window.localStorage.getItem("e2e-workspace") || "";
      case "set_workspace":
        window.localStorage.setItem("e2e-workspace", args.path);
        return args.path;
      case "get_prompt":
        return window.__e2ePrompt || { systemPrompt: "stub system prompt" };
      case "set_prompt":
        window.__e2ePrompt = { systemPrompt: args.systemPrompt || "stub system prompt" };
        return window.__e2ePrompt;
      case "reset_prompt":
        window.__e2ePrompt = { systemPrompt: "stub system prompt" };
        return window.__e2ePrompt;
      case "fs_home":
        return "/home/tester";
      case "file_complete": {
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
      case "fs_list": {
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
// bus. A turn runs through window.paladin.chat, so specs that wrap chat
// (or set __e2eChatEvents) keep scripting what it streams.
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
    };
    Promise.resolve()
      .then(() => window.paladin.chat({ ...turn.params }, handlers))
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
        },
      };
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
      const history = await window.paladin.invoke("session_history", { sessionId: args.sessionId, cwd: null });
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

window.paladin = {
  metrics: async () => window.__e2eAppMetrics || null,
  invoke: (method, params) => window.__paladinBaseInvoke(method, params || {}),
  // __e2eChatEvents scripts stream events ({event, ..., waitMs?}) that
  // fire through the handlers before the stub reply resolves.
  chat: async (params, handlers) => {
    const events = window.__e2eChatEvents || [];
    window.__e2eChatEvents = null;
    for (const ev of events) {
      if (ev.waitMs) await new Promise((r) => setTimeout(r, ev.waitMs));
      if (ev.event === "chat/delta") handlers.onDelta(ev.delta);
      else if (ev.event === "chat/tool") handlers.onTool(ev.tool);
      else if (ev.event === "chat/retry" && handlers.onRetry) handlers.onRetry(ev.retry);
      else if (ev.event === "chat/compact" && handlers.onCompact) handlers.onCompact(ev.compaction);
    }
    return window.__paladinBaseInvoke("chat", params || {});
  },
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
