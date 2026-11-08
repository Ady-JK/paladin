---
name: writing-a-plugin-ui
description: Build a Paladin Creator extension panel, overlay canvas, or tool-result card.
---
# Writing a plugin UI

Use `creating-paladin-ui` for the Creator workspace layout, manifest and available surfaces. This skill adds the practical UI checklist.

- Put each extension in a direct child of the Creator workspace's `ui/` or `core/` directory. The plugin's `paladin-plugin.json` declares its `views` and optional tool-result `cards`. A UI-only plugin uses `"tools": []` and needs no Node entry.
- Use `"mount": "side-pane"` for a tab and `"mount": "overlay"` for a movable, resizable overlay that can expand to the full window. Cards render individual tool results. None replaces built-in Paladin UI.
- Put HTML, scripts, and other assets under that plugin's `ui/` directory. Use external scripts; inline scripts are blocked. Render untrusted text as text, and handle loading, empty, and error states.
- A view uses `window.paladin.plugins` for host-provided operations. Declare only the permissions needed: `invokeTool` for its own tools and supported read-only tools; `invokeToolDestructive` for changing tools or another plugin's tools; `readFile`, `events`, `openExternal`, and `sendPrompt` for their respective bridge methods. Tool calls go through the normal approval flow. Cards render data without the panel bridge.
- Names of Creator plugins include their folder kind: for example, `core/calculator` exposes `extension__creator_core_calculator__add`. A separate `ui/` plugin calling it needs `invokeToolDestructive`.
- UI is isolated: it has no direct filesystem, Node, or network access. Use a plugin tool via the bridge when such work is needed. Never place secrets in UI assets or logs.
- Changes in the Creator workspace reload live. Verify the actual Electron UI, its keyboard interaction, sizing, tool result and error paths. For reference, see the app's plugin UI documentation where available.

For an interactive view calling another plugin, declare `"permissions": ["invokeToolDestructive"]` and put the code in a separate script under `ui/`:

```js
const ui = window.paladin.plugins;
const context = await ui.getContext(); // workspace, sessionId, plugin, view, theme
const response = await ui.invokeTool("extension__creator_core_calculator__add", { a: 2, b: 3 });
if (!response.ok) document.querySelector("output").textContent = response.error;
else document.querySelector("output").textContent = response.result;
```

The example calls a different plugin and therefore needs `invokeToolDestructive` rather than `invokeTool`. The bridge also provides per-plugin `readStore` and `writeStore`; the `events` permission enables session history and subscriptions. Use external HTML script tags such as `<script type="module" src="./index.js"></script>`.
