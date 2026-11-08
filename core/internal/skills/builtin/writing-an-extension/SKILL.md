---
name: writing-an-extension
description: Create and test JavaScript or TypeScript tools in Paladin's Creator workspace.
---
# Writing a Paladin extension tool

Use this skill in Creator mode when the user wants an agent tool. The open workspace is the Creator workspace: place each tool plugin in a direct child of `core/`, with a `paladin-plugin.json` and a JavaScript or TypeScript entry. Use the workspace Paladin opened rather than guessing its path. Do not modify Paladin's source tree or the legacy `extensions.json` to create a Creator plugin.

For example, `core/calculator/paladin-plugin.json`:

```json
{"entry":"index.js"}
```

`core/calculator/index.js`:

```js
export default {
  tools: [{
    name: "add",
    description: "Add two numbers.",
    parameters: {
      type: "object",
      properties: { a: { type: "number" }, b: { type: "number" } },
      required: ["a", "b"],
      additionalProperties: false,
    },
    async execute({ a, b }) {
      if (!Number.isFinite(a) || !Number.isFinite(b)) throw new Error("a and b must be numbers");
      return { result: a + b };
    },
  }],
};
```

The model-facing name is `extension__creator_core_calculator__add`. Electron supplies Node for local plugins. A plugin may implement multiple tools; name them clearly, validate arguments at runtime, return useful bounded results, and handle errors explicitly. Tool calls can overlap, so shared state must be safe for concurrent requests. A timeout or cancellation stops the runner and its other in-flight calls; the next call starts a fresh runner.

`execute(args, context)` receives the tool arguments and a context containing `workspace`, `project`, and `sessionId`. Use the context rather than `process.cwd()` for user workspace paths: the runner's working directory is the plugin directory. The optional manifest `tools` array can declare each tool's name, description, and parameters ahead of time; these declarations must match the exported tools. If omitted, the runner discovers them from the entry module. A plugin can include a local `package.json` with `"type": "module"` when using ES module syntax.

Creator plugins are discovered and reloaded as files change. In Settings → Tools check for load errors and enable the tool if needed. New tools become available to a running session on a subsequent turn. Exercise both successful and failed calls, and verify the requested behavior end to end. Extension tools run with the user's operating-system permissions; normal Paladin approvals still apply. Do not install dependencies or change package registries unless the user's instructions allow it.

For a panel, canvas, or tool-result card, also load `creating-paladin-ui` or `writing-a-plugin-ui`. Plugin UIs are isolated from Node and access operations through the host's tool bridge. Built-in Paladin UI cannot be replaced.
