---
name: creating-paladin-ui
description: Create Paladin UI extensions as side-pane tabs, floating canvases or tool-result cards in the Creator workspace.
---
# Creating Paladin UI

Use this when the user wants to add a Paladin panel, a floating canvas or a tool-result card. Creator mode opens the user's workspace, normally `~/.config/paladin/creator`, with `ui/` for UI extensions and `core/` for logical extensions. When `PALADIN_DATA_DIR` or `PALADIN_CREATOR_DIR` is set, use the workspace that Paladin opens rather than guessing its path.

Each direct child folder of `ui/` or `core/` is a plugin when it has `paladin-plugin.json`. Names exposed to Paladin are `creator_ui_<folder>` or `creator_core_<folder>`. Keep user files in that workspace, not in Paladin's source tree. Plugin tools are trusted local Node.js code with the user's permissions. Their UI is an isolated iframe and reaches system operations through plugin tools and the host bridge.

For a UI-only plugin use a static empty tools array; no Node entry is required:

```json
{"tools":[],"views":[{"id":"canvas","title":"My canvas","entry":"ui/index.html","mount":"overlay","permissions":[]}]}
```

Put external scripts and assets under that plugin's `ui/` folder. Use `<script type="module" src="./index.js"></script>`. A logical plugin declares an `entry` such as `index.js` or `index.ts` and exports `{tools:[...]}`; see `writing-an-extension` for tool schemas and bridge calls. Paladin's Electron installation provides Node for local plugins.

Mounts are `side-pane` and `overlay`. Side-pane views appear in a tab alongside Paladin's existing panes; overlays are movable and resizable, with an expand control for a full-window canvas. `cards` render individual tool results. Extensions do not replace Paladin's built-in UI. Use the app's existing colors and accessibility patterns unless the user asks for a different design.

A view with `invokeTool` may call its own tools using `window.paladin.plugins.invokeTool`. To call another plugin's tools, request `invokeToolDestructive`. Paladin shows the host-owned approval even when no conversation is open. Request `sendPrompt` to send chat messages, and `events` to read recent `history()` and subscribe to that session's chat events.

Paladin watches the Creator workspace and refreshes changed plugins. Save files atomically where practical, wait for the refresh, and verify the real Electron UI and the intended interaction. Avoid adding network dependencies unless the user explicitly authorizes them and existing enterprise package settings permit them.
