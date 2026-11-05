# Plugin UI

Extension plugins can ship two kinds of UI:

- **Panel** — a full side-pane tab (`views`), for a board, a log, a form.
- **Card** — an inline renderer for one tool result (`cards`), for a
  lookup result, a diff, a chart.

Both live inside the plugin folder. Paladin never builds plugin UI: ship
static HTML/CSS/JS in `ui/`.

## Layout

```
<data>/config/extensions/board/
  paladin-plugin.json
  index.ts
  ui/
    board.html
    board.css
    board.js
    card.js
```

`paladin-plugin.json` gains optional `views` and `cards`:

```jsonc
{
  "entry": "index.ts",
  "views": [
    {
      "id": "board",                    // ^[a-z][a-z0-9_-]{0,31}$, unique
      "title": "Ticket Board",          // 1..48 plain-text characters
      "entry": "ui/board.html",         // under ui/, .html, <= 2 MiB
      "mount": "side-pane",             // only side-pane today
      "permissions": ["invokeTool", "events", "readFile", "openExternal"]
    }
  ],
  "cards": [
    {
      "tool": "lookup",                 // one of this plugin's tools
      "entry": "ui/card.js",            // under ui/, .js, <= 512 KiB
      "inline": true                    // only inline cards are supported
    }
  ]
}
```

Limits: at most 4 views and 16 cards. View ids are unique; card tools are
unique. An invalid entry is reported in Settings → Tools and skipped;
the plugin's tools keep working.

## Panels

Open a panel from the **Plugins** menu in the title bar. It asks main for
a panel grant and then loads:

```
plugin://<plugin>/<view>/ui/board.html?panel=<unguessable-id>
```

The iframe is `sandbox="allow-scripts"` with no same-origin, forms,
popups, modals or top navigation. It cannot fetch anything (`connect-src
'none'`), so all data comes through the bridge.

**Scripts must be external.** The panel CSP is
`script-src 'self' plugin:` with no `'unsafe-inline'`, so:

```html
<script src="./board.js" type="module"></script>
```

works, while an inline `<script>…</script>` is blocked. Styles may be
inline (`style-src 'unsafe-inline'`) or in a relative `.css` file.

### Bridge

```ts
const paladin = window.paladin.plugins;
const ctx = await paladin.getContext();
// {sessionId, workspace, plugin, view, theme}

const answer = await paladin.invokeTool("extension__board__lookup", { key: "PAL-1" });
if (!answer.ok) showError(answer.error);
else showResult(answer.result);

const text = await paladin.readFile("README.md");     // {ok,text} | {ok,error}
const saved = await paladin.readStore("board.selected");
await paladin.writeStore("board.selected", "PAL-1");
await paladin.openExternal("https://example.com");

const sub = await paladin.subscribe({ events: ["chat/tool", "turn/done"] });
const off = paladin.onEvent((msg) => {
  // msg: {sub, event, data, dropped?}
});
// close: await paladin.unsubscribe(sub); off();
```

`invokeTool` can call the plugin's own tools and the read-only built-ins
`read`, `grep`, `glob`, `list`. Everything else — `write`, `edit`,
`shell`, MCP, another plugin's tools — needs `invokeToolDestructive` and
is shown with a "changes files" badge in the menu. Every call still goes
through the normal permission gate. If it asks, Paladin renders the
approval card in the main window; the bridge call waits for the answer.

Panel permissions:

| Permission | Allows |
|---|---|
| `invokeTool` | own tools, `read`, `grep`, `glob`, `list` |
| `invokeToolDestructive` | the tools above plus changing tools |
| `readFile` | `readFile` inside the session workspace |
| `events` | `subscribe` / `onEvent` for the panel's own session |
| `openExternal` | `openExternal` with `https:` |

Store calls need no permission: the file is the plugin's own
`.store.json` (100 KiB per key, 1 MiB total).

### Theme

`getContext().theme` is `dark` or `light`. The app also sends a
`theme` event on the subscription channel when the user switches themes:

```ts
paladin.onEvent((msg) => {
  if (msg.event === "theme") document.documentElement.dataset.theme = msg.data.theme;
});
```

### Lifecycle

- Two panels per session, four across the app; opening another closes the
  oldest plugin panel.
- Hidden tabs keep their iframe alive; closing a tab releases the grant
  and removes the frame.
- After **Reload plugins**, an open panel shows a "Plugin updated" banner;
  reloading recreates the iframe.
- Panel UI is local-only. On an SSH core the menu explains that plugin UI
  needs a local core.

## Cards

A card renders one tool result inside the transcript. It is a classic
script (not a module) with no bridge:

```js
// ui/card.js
window.__paladin_card = {
  render(result, ctx) {
    // result is the tool's JSON result (parsed when possible)
    // ctx: {tool, sessionId, theme}
    const el = document.createElement("div");
    el.textContent = `Found ${result.count} matches`;
    return el; // HTMLElement or an HTML string
  },
};
```

The host serves the card as its own `plugin://` document with the same
`allow-scripts` sandbox and a strict CSP. The script gets 3 seconds to
load; a missing, failed or slow card falls back to the raw result text.
At most 20 live card frames are kept; older ones fall back.

Cards may not call the bridge, open windows, or load network resources.
Keep them pure: render the result; put interaction in a panel. The card
script is served as a classic script; relative imports are not served,
so keep it self-contained.

## Security model

- Plugin UI is local code the user enabled, the same trust as the plugin's
  tools. Opening a panel is an explicit action.
- Every file served by `plugin://` is resolved and checked to stay under
  the plugin's `ui/` folder; manifests, `node_modules`, dotfiles and
  symlink escapes return 404.
- Frames get an opaque origin, no storage, no network and no raw
  `agent:invoke`; a panel can only read its own session's events and
  workspace files (when allowed).
- Approval cards are host-rendered. A panel cannot answer an approval,
  use the keychain, control the terminal, or navigate the main window.
- Treat plugin UI as phishing-capable inside its own tab: only open panels
  from plugins you trust.

## Troubleshooting

| Symptom | Fix |
|---|---|
| No Plugins menu entry | The plugin must be enabled and have a valid `views` entry; press **Reload plugins**. |
| Panel shows "not available" | The manifest failed validation; check Settings → Tools → Extensions for the error. |
| Panel 404s | `entry` must be under `ui/`, a regular file, and within the size cap. |
| Bridge call fails | Check the view's `permissions` and the tool name. The error text is shown to the panel. |
| Card shows the raw result | No card is declared for that tool, the script failed, it timed out, or the 20-frame cap was reached. |
| Nothing updates after editing | Press **Reload plugins** (manifest, entry and `package.json` changes reload automatically). |
| Theme looks wrong | Read `getContext().theme` and listen for the `theme` event. |
