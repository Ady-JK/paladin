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
          sessionId: "sess-notify-1",
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
      default:
        if (String(cmd).startsWith("plugin:")) {
          return 1;
        }
        throw new Error(`e2e stub: unhandled command ${cmd}`);
    }

}

window.__paladinBaseInvoke = __paladinBaseInvoke;

window.paladin = {
  invoke: (method, params) => window.__paladinBaseInvoke(method, params || {}),
  chat: (params, handlers) => window.__paladinBaseInvoke("chat", params || {}),
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
