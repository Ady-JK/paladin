---
name: writing-a-plugin-ui
description: Give a Paladin extension a sandboxed side-pane panel or inline result card - manifest, scripts, bridge, permissions and testing.
---
# Writing a plugin UI

Use this when a Paladin extension tool needs a face: a board, a log, a
form, a chart, a detailed result card. UI ships inside the plugin folder
as static files; Paladin never builds it.

There are two mounts:

| Mount | Manifest key | Appearance |
|---|---|---|
| Panel | `views` | A side-pane tab beside the conversation, full height. |
| Card | `cards` | An inline renderer for one tool result in the transcript. |

A panel can talk to Paladin through `window.paladin.plugins`. A card is a
pure renderer with no bridge. Start with the tool skill (`writing-an-extension`)
if the plugin does not exist yet.

## 0. What the user sees

- **Plugins** menu in the title bar lists panels with badges: "runs tools",
  "reads files", "watches chat", "opens links", or the red "changes files".
- On an SSH core the menu explains that plugin UI is local-only.
- Two panels per session and four across the app; opening another closes
  the oldest plugin panel.
- A panel reload shows a "Plugin updated" banner with **Reload**.
- A card falls back to the raw result if its script is missing, broken,
  slow, or over the 20-live-frame cap.

## 1. Choose the mount

- **Panel** for anything interactive, long-lived, or bigger than one
  result: browsing, filtering, editing, a dashboard.
- **Card** for one tool call's result: a lookup summary, a chart, a diff.
- **Both** when the card is a summary and the panel is the detail view.
  The card can render a compact result; a click cannot open the panel, so
  tell the user to open it from the Plugins menu.

Do not put a full app in a card; cards are capped, isolated and short-lived.

## 2. Lay out the files

```
<data>/config/extensions/my-plugin/
  paladin-plugin.json
  index.ts
  ui/
    board.html
    board.js
    board.css
    card.js
```

Every file a panel loads must be under `ui/`. An absolute path, `..`, a
dotfile, a symlink that leaves the folder, `paladin-plugin.json`,
`package.json` and `node_modules` all return 404.

Entries are size-capped:

| Entry | Extension | Cap |
|---|---|---|
| View `entry` | `.html` | 2 MiB |
| Card `entry` | `.js` | 512 KiB |
| Other `ui/` assets | allow-listed MIME | 8 MiB |

## 3. Declare the UI in the manifest

`paladin-plugin.json`:

```jsonc
{
  "entry": "index.ts",
  "views": [
    {
      "id": "board",                    // ^[a-z][a-z0-9_-]{0,31}$, unique per plugin
      "title": "Ticket Board",          // 1..48 plain-text characters
      "entry": "ui/board.html",         // must be under ui/
      "mount": "side-pane",             // only side-pane is supported
      "permissions": ["invokeTool", "events", "readFile", "openExternal"]
    }
  ],
  "cards": [
    {
      "tool": "lookup",                 // this plugin's tool name
      "entry": "ui/card.js",            // under ui/, classic script
      "inline": true                    // only inline cards exist
    }
  ]
}
```

Limits: at most 4 views and 16 cards. Ids and card tools are unique.
An invalid entry is reported in Settings → Tools and skipped; the
plugin's tools keep working. Unknown manifest fields are an error.

View permissions:

| Permission | Grants |
|---|---|
| `invokeTool` | the plugin's own tools plus `read`, `grep`, `glob`, `list` |
| `invokeToolDestructive` | also `write`, `edit`, `shell`, MCP, other plugins' tools |
| `readFile` | `readFile` inside the session workspace |
| `events` | `subscribe` / `onEvent` for the panel's own session |
| `openExternal` | `openExternal` with `https:` URLs |

A view with only `invokeToolDestructive` may still invoke. Store calls
need no permission.

## 4. Write the panel

### HTML

Scripts must be **external**: the panel CSP is
`script-src 'self' plugin:` with no `'unsafe-inline'`, because plugin
HTML is untrusted. Styles may be inline or a relative CSS file.

`ui/board.html`:

```html
<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <link rel="stylesheet" href="./board.css" />
    <title>Ticket Board</title>
  </head>
  <body>
    <header>
      <strong>Ticket Board</strong>
      <span id="status">ready</span>
      <button id="refresh" type="button">Refresh</button>
    </header>
    <main id="rows" aria-live="polite"></main>
    <script src="./board.js" type="module"></script>
  </body>
</html>
```

### Script

`window.paladin.plugins` is injected into the frame before the page
runs. The methods are asynchronous and every one can fail; show the
error, do not assume success.

`ui/board.js`:

```js
const ui = window.paladin.plugins;
const rows = document.getElementById("rows");
const status = document.getElementById("status");

function setStatus(text) {
  status.textContent = text;
}

function row(item) {
  const el = document.createElement("button");
  el.type = "button";
  el.className = "row";
  el.textContent = `${item.key} — ${item.title}`;
  el.addEventListener("click", () => {
    el.setAttribute("aria-pressed", "true");
    setStatus(`selected ${item.key}`);
  });
  return el;
}

async function load() {
  setStatus("loading…");
  const ctx = await ui.getContext();
  document.documentElement.dataset.theme = ctx.theme;
  const result = await ui.invokeTool("extension__board__lookup", {});
  if (!result.ok) {
    setStatus(result.error);
    return;
  }
  rows.replaceChildren();
  for (const item of result.result ?? []) rows.append(row(item));
  setStatus(`${rows.children.length} tickets`);
}

document.getElementById("refresh").addEventListener("click", () => void load());
ui.onEvent((msg) => {
  if (msg.event === "theme") document.documentElement.dataset.theme = msg.data.theme;
  if (msg.event === "turn/done") void load();
});
void load();
```

`ui/board.css`:

```css
:root { color-scheme: dark; --bg: #17171a; --fg: #e9e9ed; --muted: #94a3b8; }
:root[data-theme="light"] { color-scheme: light; --bg: #fff; --fg: #0f172a; --muted: #64748b; }
html, body { margin: 0; height: 100%; background: var(--bg); color: var(--fg); font: 13px/1.45 ui-sans-serif, system-ui, sans-serif; }
header { display: flex; gap: 8px; align-items: center; padding: 10px 12px; border-bottom: 1px solid rgb(128 128 128 / .2); }
main { padding: 8px; display: grid; gap: 6px; }
.row { text-align: left; padding: 8px 10px; border: 0; border-radius: 8px; background: rgb(128 128 128 / .1); color: inherit; }
.row[aria-pressed="true"] { outline: 2px solid #8faafa; }
```

The frame is sandboxed with `allow-scripts` only: no same-origin, no
storage, no forms, no popups, no modals, no top navigation, and no
network (`connect-src 'none'`). Do not fetch; use the bridge.

## 5. Use the bridge

The surface is deliberately narrow:

| Call | Returns |
|---|---|
| `getContext()` | `{sessionId, workspace, plugin, view, theme}` |
| `invokeTool(tool, args)` | `{ok:true, result}` or `{ok:false, error}` |
| `readFile(rel)` | `{ok:true, text}` or `{ok:false, error}` |
| `readStore(key)` | the stored JSON value, or `null` |
| `writeStore(key, value)` | `{ok:true}` or `{ok:false, error}` |
| `openExternal(url)` | `{ok:true}` or `{ok:false, error}` |
| `subscribe(filter)` | a subscription id |
| `unsubscribe(id)` | `true`/`false` |
| `onEvent(cb)` | an unsubscribe function |

### invokeTool

Use the full exposed name from the tool skill:
`extension__<pluginFolder>__<tool>`. The panel may call its own tools and
the read-only built-ins without extra permission. Anything else needs
`invokeToolDestructive`.

Every call still passes Paladin's permission gate. If it needs approval,
the host draws the approval card over the conversation and the bridge
call waits. The panel never draws or answers that card. A denial comes
back as `{ok:false, error}`.

Results are the tool's text. If the tool returns an object, the text is
JSON; parse it when the shape is known. Do not assume every call succeeds.

### readFile

`rel` is relative to the session workspace. Absolute paths, `..` and
symlink escapes are refused. Text only, 1 MiB cap, 120 reads/min.

### readStore / writeStore

A per-plugin JSON store in the plugin folder (`.store.json`). Keys match
`^[a-zA-Z0-9_.-]{1,64}$`; a value is at most 100 KiB and the whole store
1 MiB. Use it for the panel's selection, column widths or saved filters;
do not put secrets there.

### openExternal

Only `https:` URLs; 3 opens per minute per panel. Use it for "open in
browser", never for navigation inside the panel.

### Events

`subscribe` defaults to the panel's own session; another session is
refused. The vocabulary is:

`chat/delta`, `chat/tool`, `turn/start`, `turn/done`, `browser/navigated`,
`plugin/updated`, `tools/pending`, plus the synthetic `theme` event.

Approvals and ask answers are **not** forwarded: a panel observes work,
it does not decide permissions. Events are capped at 240/min; a dropped
count rides on the next delivered event as `msg.dropped`.

```js
const sub = await ui.subscribe({ events: ["chat/tool", "turn/done"] });
const off = ui.onEvent((msg) => {
  if (msg.sub !== sub) return;
  if (msg.event === "chat/tool") renderTool(msg.data.tool);
  if (msg.event === "turn/done") void load();
});
// on close:
await ui.unsubscribe(sub);
off();
```

### Theme

Read `getContext().theme` at load and apply it to
`document.documentElement.dataset.theme`. The app sends a `theme` event
when the user switches; update the attribute there too.

## 6. Write a card

A card is a classic script with one global and no bridge:

```js
// ui/card.js
window.__paladin_card = {
  render(result, ctx) {
    // result is the tool result, parsed as JSON when possible
    // ctx: {tool, sessionId, theme}
    const root = document.createElement("div");
    root.className = "lookup-card";
    const title = document.createElement("strong");
    title.textContent = result && result.title ? result.title : "No result";
    root.append(title);
    if (result && result.key) {
      const key = document.createElement("code");
      key.textContent = result.key;
      root.append(key);
    }
    return root;
  },
};
```

Return an `HTMLElement` or an HTML string. Throw to show a short error
inside the card. The host gives the script 3 seconds to load; after that
the raw result is shown. The script cannot import relative modules or
load network assets, so keep it self-contained. Inline styles are fine.

A card is not interactive beyond links the browser can render itself;
put interaction in a panel.

## 7. Reload and lifecycle

- Changing the manifest, the entry file or `package.json` invalidates
  declarations; helper modules and UI assets do not.
- **Reload plugins** (Settings → Tools or the tool reload) drops warm
  runners, re-reads manifests and sends `plugin/updated`. Open panels
  show a banner; **Reload** recreates the iframe.
- Hidden tabs keep their iframe alive. Closing a tab releases the grant
  and removes the frame; a later open is a fresh load.
- Two panels per session, four app-wide; the oldest plugin panel closes.
- Cards are limited to 20 live iframes.

## 8. Security checklist

- Treat the panel as untrusted content. It cannot reach the main DOM,
  approvals, the terminal, credentials or other panels.
- Only load `ui/` assets; respect the CSP (`connect-src 'none'`).
- Use `invokeTool` for work; never fake a permission decision in UI.
- Ask for the narrowest permissions: `readFile` only when needed,
  `invokeToolDestructive` only for a real editor.
- Never put tokens in `ui/`, the store or `invokeTool` arguments; they
  reach the transcript and logs.
- Escape any text rendered with `innerHTML`; prefer `textContent`.
- Do not try to bypass the sandbox (top navigation, popups, forms,
  service workers); it is blocked by design.
- Keep cards pure and small; no bridge, no network, no timers that
  outlive the result.

## 9. Test it

1. Enable the plugin, press **Reload plugins**, and check the Plugins
   menu lists the view with the expected badges.
2. Open it; the iframe must load, `getContext` must return the session's
   workspace, and the theme must match.
3. Call each allowed bridge method and each forbidden one; the forbidden
   one must return an error, not a 404 page or a blank frame.
4. Trigger a tool call that needs approval; the host card must appear and
   the panel must wait, then receive the result or denial.
5. Resize, switch tabs, switch sessions and close the tab; the frame must
   stay alive when hidden and disappear when closed.
6. Reload plugins while the panel is open; the banner must appear and
   reload must load the new entry.
7. For a card, call the tool and check the rendered result. Break the
   script and confirm the raw fallback. Remove the card entry and confirm
   the normal tool row returns.
8. On an SSH core, the menu must explain that plugin UI is local-only.

## 10. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| View missing from the Plugins menu | Plugin not enabled, invalid manifest, bad entry, or UI errors in Settings → Tools. Press **Reload plugins**. |
| Panel blank or "failed to load" | Entry missing/nonexistent, outside `ui/`, wrong extension, too large, or a CSP-blocked inline script. Use an external `.js`. |
| 404 for an asset | Assets must live under `ui/`; no `..`, absolute paths, dotfiles, manifests or `node_modules`. |
| `window.paladin.plugins` undefined | The page is not a granted panel (opened directly, or the grant was closed). Reopen from the menu. |
| Bridge call says "may not call" | Missing permission, wrong tool name, or another plugin's tool without `invokeToolDestructive`. |
| `readFile` says outside workspace | Use a path relative to `getContext().workspace`; symlinks that leave it are refused. |
| Approval never resolves | The host card is waiting; answer it in the main window. Closing the panel does not cancel it. |
| Card shows raw JSON | No card for that tool, script failed/timed out, or the 20-frame cap was reached. |
| Theme does not change | Read `getContext().theme` and handle the `theme` event; do not rely on `prefers-color-scheme` alone. |
| Changes do not appear | Press **Reload plugins**; UI assets are not watched. |

## 11. Final checklist

- [ ] Manifest has a valid `views` or `cards` entry under `ui/`.
- [ ] Panel HTML uses external scripts only.
- [ ] Every relative asset stays under `ui/`.
- [ ] `getContext` sets the theme; the `theme` event updates it.
- [ ] Bridge calls check `ok` and show errors.
- [ ] Permissions are the narrowest that work.
- [ ] Events are unsubscribed when the panel closes; `onEvent` is released.
- [ ] Store values are small and JSON; no secrets.
- [ ] A card is self-contained and returns an element/string.
- [ ] Reload, hidden-tab, close and SSH paths were checked.
- [ ] Report the view id, permissions, and any limits the user should know.
