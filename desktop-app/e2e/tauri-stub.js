window.__TAURI_INTERNALS__ = {
  metadata: {
    currentWindow: { label: "main" },
    currentWebview: { windowLabel: "main", label: "main" },
  },
  invoke: async (cmd, args) => {
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
      case "chat_stream": {
        const inner = args && args.args ? args.args : args;
        window.__e2eChats = window.__e2eChats || [];
        window.__e2eChats.push({
          sessionId: inner.sessionId,
          message: inner.message,
          resume: !!inner.resume,
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
      case "show_external_page":
      case "navigate_external_page":
      case "reload_external_page":
      case "hide_external_page":
        return null;
      case "move_external_page":
        window.__e2eMoves = (window.__e2eMoves || 0) + 1;
        return null;
      case "fs_home":
        return "/home/tester";
      case "fs_list": {
        const at = (args && args.path) || "/home/tester";
        return {
          currentPath: at,
          parentPath: "/home",
          directories: [{ name: "project", path: `${at}/project` }],
        };
      }
      default:
        if (String(cmd).startsWith("plugin:")) {
          return 1;
        }
        throw new Error(`e2e stub: unhandled command ${cmd}`);
    }
  },
  transformCallback: () => 1,
  unregisterCallback: () => {},
  convertFileSrc: (p) => p,
};

window.__TAURI_EVENT_PLUGIN_INTERNALS__ = {
  unregisterListener: () => {},
};
