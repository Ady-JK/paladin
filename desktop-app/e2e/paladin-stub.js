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
        {
          name: "review",
          description: "Review the current changes",
          argumentHint: "[focus]",
          mode: "plan",
          source: "builtin",
        },
        { name: "ship", description: "Release checklist", argumentHint: "<version>", source: "workspace" },
      ];
    case "memoryGet": {
      window.__e2eMemory = window.__e2eMemory || {};
      const key = args.target + ":" + (args.cwd || "");
      const entries = window.__e2eMemory[key] || (args.target === "user" ? ["Prefers terse answers"] : []);
      const limits = { user: 1500, global: 2500, project: 4000 };
      return {
        target: args.target,
        cwd: args.cwd || undefined,
        path: `/data/memory/${args.target}.md`,
        entries,
        used: entries.join("\n§\n").length,
        limit: limits[args.target] || 2500,
      };
    }
    case "memorySet": {
      window.__e2eMemory = window.__e2eMemory || {};
      window.__e2eMemorySets = (window.__e2eMemorySets || []).concat([args]);
      const key = args.target + ":" + (args.cwd || "");
      window.__e2eMemory[key] = args.entries || [];
      const limits = { user: 1500, global: 2500, project: 4000 };
      return {
        target: args.target,
        cwd: args.cwd || undefined,
        path: `/data/memory/${args.target}.md`,
        entries: args.entries || [],
        used: (args.entries || []).join("\n§\n").length,
        limit: limits[args.target] || 2500,
      };
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
    case "mcpList":
    case "mcpConfig":
    case "mcpSetConfig":
    case "mcpSetEnabled":
    case "mcpRestart":
    case "mcpTrust":
    case "mcpParse":
    case "mcpAdd":
      return window.__e2eMcpInvoke(cmd, args || {});
    case "hooksList":
    case "hooksSetConfig":
    case "hooksTrust":
    case "hooksToggle":
      return window.__e2eHooksInvoke(cmd, args || {});
    case "sideQuestion":
      window.__e2eSideCalls = (window.__e2eSideCalls || []).concat([JSON.parse(JSON.stringify(args || {}))]);
      if (window.__e2eSideQuestion) return window.__e2eSideQuestion(args || {});
      return {
        chatId: (args && args.chatId) || "chat-" + window.__e2eSideCalls.length,
        answer: "Side answer to: " + (args && args.question),
        model: (args && args.model) || "stub-model",
      };
    case "listSideChats":
      return window.__e2eSideChats || [];
    case "loadSideChat": {
      const chat = (window.__e2eSideChatBodies || {})[args && args.chatId];
      if (!chat) throw new Error("side chat not found");
      return chat;
    }
    case "deleteSideChat":
      window.__e2eSideDeletes = (window.__e2eSideDeletes || []).concat([args]);
      window.__e2eSideChats = (window.__e2eSideChats || []).filter((c) => c.id !== (args && args.chatId));
      return null;
    case "listAgents":
      return window.__e2eAgents || [];
    case "interruptAgent":
      window.__e2eAgentInterrupts = (window.__e2eAgentInterrupts || []).concat([args]);
      return {
        agentId: args && args.agentId,
        parentSessionId: args && args.sessionId,
        name: "",
        type: "",
        status: "interrupted",
        updatedAt: Date.now(),
      };
    case "toolsList":
    case "toolsSet":
    case "toolsAccept":
      return window.__e2eToolsInvoke(cmd, args || {});
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
      return {
        index: args.index,
        message: "run the slow job",
        restored: [],
        skipped: [],
        snapshots: true,
        ...(window.__e2eRevertResult || {}),
      };
    }
    case "unrevertSession":
      window.__e2eUnreverts = (window.__e2eUnreverts || 0) + 1;
      window.__e2eRevertedAt = undefined;
      return true;
    case "worktreeStatus":
      return (
        window.__e2eWorktreeStatus || {
          exists: true,
          changed: 0,
          ahead: 0,
          branch: "paladin/demo",
          into: "main",
          path: "/wt/demo",
          repoRoot: "/home/tester/project",
        }
      );
    case "worktreeMerge":
      window.__e2eMerges = (window.__e2eMerges || []).concat([args]);
      if (window.__e2eMergeError) throw new Error(window.__e2eMergeError);
      return (
        window.__e2eMergeResult || {
          merged: true,
          nothingToMerge: false,
          commit: "abc1234",
          conflicts: [],
          into: "main",
        }
      );
    case "deleteSession":
      window.__e2eDeletes = (window.__e2eDeletes || []).concat([args]);
      return true;
    case "forkSession":
      window.__e2eForks = (window.__e2eForks || []).concat([args]);
      if (args && args.worktree) {
        return {
          sessionId: "sess-wt-1",
          cwd: "/wt/demo",
          title: "notify demo (fork)",
          messageCount: 2,
          createdAt: Math.floor(Date.now() / 1000),
          worktree: {
            repoRoot: "/home/tester/project",
            path: "/wt/demo",
            branch: "paladin/demo",
            baseBranch: "main",
            origin: "/home/tester/project",
          },
        };
      }
      return {
        sessionId: "sess-fork-1",
        cwd: "/home/tester/project",
        title: "notify demo (fork)",
        messageCount: 2,
        createdAt: Math.floor(Date.now() / 1000),
      };
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
      const own = (window.__e2eHistories || {})[(args && args.sessionId) || ""];
      if (own) return own;
      if (window.__e2eHistory) return window.__e2eHistory;
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
          arguments: '{"action":"run","cmd":"sleep 60","background":true,"notify":true}',
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
          ? ["anthropic/claude-fable-20990101", "anthropic/claude-3-opus", "openai/gpt-4o"]
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
        return { sessionId: inner.sessionId || "sess-notify-1", reply: "", stopped: false };
      }
      return {
        sessionId: inner.sessionId || "sess-notify-1",
        reply: "Stub reply to: " + (inner.message || ""),
        stopped: false,
      };
    }
    case "submitChat":
      return __e2eHub.submit(args || {});
    case "steerChat":
      return __e2eHub.steer(args || {});
    case "thinkingGet":
    case "thinkingSet": {
      // Models named like gpt-4o or llama have no adjustable thinking.
      const store = (window.__e2eThinking = window.__e2eThinking || {});
      const provider = (args && args.provider) || "Opencode Go";
      const model = (args && args.model) || "";
      const levels = /gpt-4o|llama/.test(model) ? [] : ["none", "low", "medium", "high", "xhigh", "max"];
      const key = provider + "\u0000" + model;
      if (cmd === "thinkingSet") {
        window.__e2eThinkingSets = (window.__e2eThinkingSets || []).concat([JSON.parse(JSON.stringify(args))]);
        if (args.level && !levels.includes(args.level)) throw new Error("unsupported level " + args.level);
        if (args.level) store[key] = args.level;
        else delete store[key];
      }
      return { provider, model, levels, level: store[key] || "" };
    }
    case "budgetGet":
    case "budgetSet": {
      const b = (window.__e2eBudget = window.__e2eBudget || {
        defaultUsd: 0,
        sessions: {},
        spentUsd: 0,
        hasCost: true,
      });
      const sid = (args && args.sessionId) || "";
      if (cmd === "budgetSet") {
        window.__e2eBudgetSets = (window.__e2eBudgetSets || []).concat([JSON.parse(JSON.stringify(args))]);
        if (args.scope === "global") b.defaultUsd = args.budgetUsd || 0;
        else if (args.budgetUsd === null || args.budgetUsd === undefined) delete b.sessions[sid];
        else b.sessions[sid] = args.budgetUsd;
      }
      const own = sid && sid in b.sessions ? b.sessions[sid] : null;
      return {
        defaultUsd: b.defaultUsd,
        sessionUsd: own,
        effectiveUsd: own ?? b.defaultUsd,
        spentUsd: sid ? b.spentUsd : 0,
        hasCost: sid ? b.hasCost : false,
        calls: sid ? (b.calls ?? 1) : 0,
      };
    }
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
        {
          path: "src/main.go",
          display: "main.go",
          resolvedPath: "/home/tester/project/src/main.go",
          outside: false,
          isDir: false,
        },
        {
          path: "src/server/",
          display: "server/",
          resolvedPath: "/home/tester/project/src/server",
          outside: false,
          isDir: true,
        },
        {
          path: "src/server/app.go",
          display: "app.go",
          resolvedPath: "/home/tester/project/src/server/app.go",
          outside: false,
          isDir: false,
        },
        {
          path: "README.md",
          display: "README.md",
          resolvedPath: "/home/tester/project/README.md",
          outside: false,
          isDir: false,
        },
        {
          path: "../sibling/outside.txt",
          display: "outside.txt",
          resolvedPath: "/home/tester/sibling/outside.txt",
          outside: true,
          isDir: false,
        },
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

// __e2eMcpInvoke backs the mcp* RPCs from window.__e2eMcp
// ({servers, hasFolderConfig, trusted, errors, configs}); every call is
// logged to __e2eMcpCalls.
window.__e2eMcpInvoke = (cmd, args) => {
  const m = (window.__e2eMcp = window.__e2eMcp || {});
  m.servers = m.servers || [];
  m.configs = m.configs || {};
  window.__e2eMcpCalls = (window.__e2eMcpCalls || []).concat([{ cmd, args: JSON.parse(JSON.stringify(args)) }]);
  const cwd = args.cwd || "/home/tester/project";
  const overview = () => ({
    servers: JSON.parse(JSON.stringify(m.servers)),
    globalPath: "/data/config/mcp.json",
    folderPath: cwd + "/.paladin/mcp.json",
    hasFolderConfig: !!m.hasFolderConfig,
    trusted: m.trusted !== false,
    errors: m.errors || undefined,
  });
  const find = () => m.servers.find((s) => s.name === args.name && s.scope === args.scope);
  switch (cmd) {
    case "mcpList":
      return overview();
    case "mcpConfig": {
      const text = m.configs[args.scope];
      return {
        path: args.scope === "global" ? "/data/config/mcp.json" : cwd + "/.paladin/mcp.json",
        text: text || '{\n  "mcpServers": {}\n}\n',
        exists: !!text,
      };
    }
    case "mcpSetConfig": {
      let parsed;
      try {
        parsed = JSON.parse(args.text);
      } catch (err) {
        throw new Error("invalid JSON: " + err.message, { cause: err });
      }
      m.configs[args.scope] = args.text;
      m.servers = m.servers.filter((s) => s.scope !== args.scope);
      for (const name of Object.keys(parsed.mcpServers || {})) {
        m.servers.push({ name, scope: args.scope, transport: "stdio", state: "ready", restarts: 0, tools: [] });
      }
      if (args.scope === "folder") {
        m.hasFolderConfig = true;
        m.trusted = true;
      }
      return overview();
    }
    case "mcpSetEnabled": {
      const s = find();
      if (!s) throw new Error("no server " + args.name);
      s.state = args.enabled ? "ready" : "disabled";
      return overview();
    }
    case "mcpRestart": {
      const s = find();
      if (!s) throw new Error("no server " + args.name);
      Object.assign(s, { state: "ready", error: "" });
      return overview();
    }
    case "mcpParse": {
      if (m.parse) return { servers: m.parse };
      const lines = String(args.text || "")
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean);
      if (!lines.length || lines.some((l) => l.startsWith("{")))
        throw new Error("nothing to add: paste a command, URL or mcp.json snippet");
      return {
        servers: lines.map((l) => {
          if (/^https?:\/\//.test(l)) return { name: new URL(l).hostname.split(".")[0], config: { url: l } };
          const [command, ...rest] = l.split(/\s+/);
          const last = rest[rest.length - 1] || command;
          return {
            name: last
              .split("/")
              .pop()
              .replace(/^(mcp-)?server-/, ""),
            config: { command, args: rest },
          };
        }),
      };
    }
    case "mcpAdd": {
      for (const n of args.servers) {
        const dup = m.servers.find((s) => s.name === n.name && s.scope === args.scope);
        if (dup && !args.replace)
          throw new Error(
            `a server named "${n.name}" already exists in ${args.scope === "global" ? "/data/config/mcp.json" : cwd + "/.paladin/mcp.json"}`,
          );
        m.servers = m.servers.filter((s) => !(s.name === n.name && s.scope === args.scope));
        m.servers.push({
          name: n.name,
          scope: args.scope,
          transport: n.config.url ? "http" : "stdio",
          state: "ready",
          restarts: 0,
          tools: [],
        });
      }
      return overview();
    }
    case "mcpTrust":
      m.trusted = !!args.trust;
      for (const s of m.servers) {
        if (s.scope === "folder") s.state = args.trust ? "ready" : "untrusted";
      }
      return overview();
  }
  throw new Error("unknown " + cmd);
};

// __e2eToolsInvoke backs the tools* RPCs from window.__e2eTools
// ({tools, global: {name: bool}, sessions: {sid: {name: bool}},
// pending: {sid: [names]}}); every call is logged to __e2eToolsCalls.
window.__e2eToolsInvoke = (cmd, args) => {
  const t = (window.__e2eTools = window.__e2eTools || {});
  t.tools = t.tools || [];
  t.global = t.global || {};
  t.sessions = t.sessions || {};
  t.pending = t.pending || {};
  window.__e2eToolsCalls = (window.__e2eToolsCalls || []).concat([{ cmd, args: JSON.parse(JSON.stringify(args)) }]);
  const sid = args.sessionId || "";
  const mine = () => (sid ? (t.sessions[sid] = t.sessions[sid] || {}) : {});
  const overview = () => {
    const sess = mine();
    const pending = (t.pending[sid] || []).filter((n) => !(n in sess));
    return {
      tools: t.tools.map((x) => {
        const g = t.global[x.name];
        const s = sess[x.name];
        const on = s !== undefined ? s : g !== undefined ? g : true;
        return { ...x, global: g, session: s, enabled: on, pending: pending.includes(x.name) || undefined };
      }),
      pending,
    };
  };
  switch (cmd) {
    case "toolsList":
      return overview();
    case "toolsSet": {
      if (args.scope === "session" && !sid) throw new Error("toolsSet: session scope needs a sessionId");
      const m = args.scope === "global" ? t.global : mine();
      for (const n of args.names) {
        if (args.enabled === null || args.enabled === undefined) delete m[n];
        else m[n] = args.enabled;
      }
      if (args.scope === "session" && args.enabled)
        t.pending[sid] = (t.pending[sid] || []).filter((n) => !args.names.includes(n));
      return overview();
    }
    case "toolsAccept": {
      const names = args.names && args.names.length ? args.names : t.pending[sid] || [];
      t.pending[sid] = (t.pending[sid] || []).filter((n) => !names.includes(n));
      return overview();
    }
  }
  throw new Error("unknown " + cmd);
};

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
      onBudget: (budget) => chat(turn, "chat/budget", { budget }),
      onHook: (hook) => chat(turn, "chat/hook", { hook }),
      onReasoning: (reasoning) => chat(turn, "chat/reasoning", { reasoning }),
      onThinkingDropped: (thinkingDropped) => chat(turn, "chat/thinkingDropped", { thinkingDropped }),
    };
    Promise.resolve()
      .then(() => window.__e2eRunTurn({ ...turn.params }, handlers))
      .then((res) => finish(turn, { reply: res?.reply || "", stopped: !!res?.stopped }))
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
    const { params: _params, ...rest } = t;
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
    // steer mirrors steerChat: it joins the session's running turn at
    // once (the real agent waits for the next model call).
    steer(args) {
      const running = turns.find((t) => t.sessionId === args.sessionId && t.state === "running");
      if (!running) throw new Error(`session ${args.sessionId} has no running turn to steer`);
      window.__e2eSteers = (window.__e2eSteers || []).concat([JSON.parse(JSON.stringify(args))]);
      const steer = { message: args.message };
      if (args.display && args.display !== args.message) steer.display = args.display;
      chat(running, "chat/steer", { steer });
      return info(running);
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

// __e2eHooksInvoke backs the hooks* RPCs from window.__e2eHooks
// ({sources}); every call is logged to __e2eHooksCalls.
window.__e2eHooksInvoke = (cmd, args) => {
  const cwd = args.cwd || "/home/tester/project";
  const h = (window.__e2eHooks = window.__e2eHooks || {});
  if (!h.sources) {
    h.sources = [
      {
        path: "/data/config/hooks.json",
        scope: "global",
        format: "paladin",
        exists: false,
        editable: true,
        trusted: true,
        text: "",
        handlers: [],
      },
      {
        path: cwd + "/.paladin/hooks.json",
        scope: "folder",
        format: "paladin",
        exists: false,
        editable: true,
        trusted: false,
        text: "",
        handlers: [],
      },
    ];
  }
  window.__e2eHooksCalls = (window.__e2eHooksCalls || []).concat([{ cmd, args: JSON.parse(JSON.stringify(args)) }]);
  const overview = () => ({
    sources: JSON.parse(JSON.stringify(h.sources)),
    events: ["PreToolUse", "PostToolUse", "Stop"],
  });
  switch (cmd) {
    case "hooksList":
      return overview();
    case "hooksSetConfig": {
      const src = h.sources.find((s) => s.scope === args.scope && s.format === "paladin");
      if (!args.text.trim()) {
        Object.assign(src, { exists: false, text: "", handlers: [] });
        return overview();
      }
      let parsed;
      try {
        parsed = JSON.parse(args.text);
      } catch (e) {
        throw new Error("invalid JSON: " + e.message, { cause: e });
      }
      const handlers = [];
      for (const [event, groups] of Object.entries(parsed.hooks || {})) {
        groups.forEach((g, gi) =>
          (g.hooks || []).forEach((x, hi) =>
            handlers.push({
              key: `${src.path}#${event}/${gi}/${hi}`,
              event,
              matcher: g.matcher || "",
              command: x.command,
              enabled: true,
            }),
          ),
        );
      }
      Object.assign(src, { exists: true, text: args.text, handlers, trusted: src.scope === "global" });
      return overview();
    }
    case "hooksTrust": {
      const src = h.sources.find((s) => s.path === args.path);
      src.trusted = !!args.trust;
      return overview();
    }
    case "hooksToggle": {
      for (const s of h.sources) for (const x of s.handlers) if (x.key === args.key) x.enabled = !!args.enabled;
      return overview();
    }
  }
  throw new Error("unknown " + cmd);
};

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
    else if (ev.event === "chat/budget" && handlers.onBudget) handlers.onBudget(ev.budget);
    else if (ev.event === "chat/hook" && handlers.onHook) handlers.onHook(ev.hook);
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
