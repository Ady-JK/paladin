# Local extensions

Paladin can load tools the user writes themselves. They live in the data
directory, not in the repository, and are enabled explicitly:

```
<data>/config/extensions.json
<data>/config/extensions/
  my-plugin/
    paladin-plugin.json     # optional manifest
    package.json            # optional: gives the plugin its own node_modules
    node_modules/           # yours, installed by you, never touched by Paladin
    index.ts                # the entry (or index.js)
    paladin.d.ts            # types (written by `agent-host extensions init`)
  legacy.ts                 # a loose file, still supported
```

`agent-host extensions init` writes `extensions.json`, `paladin.d.ts` and
a `hello.ts` example (add `--example-plugin` for a plugin folder instead).
Nothing runs until its name is in `extensions.json`:

```jsonc
{"enabled": ["my-plugin", "legacy.ts"]}
```

Find the data directory with `PALADIN_DATA_DIR`, or look at the path the
sidecar logs at start (`agent-host ready dir=…`); the default is the OS
config directory plus `/paladin`.

## The module

A plugin's default export is an object with a `tools` array and optional
lifecycle hooks:

```ts
import type { PaladinExtension } from "./paladin.d.ts";

export default {
  // Called once, when the plugin's process starts.
  onActivate(ctx) {
    // ctx: { pluginDir, pid, workspace }
  },
  // Called when the process is asked to stop.
  onDeactivate() {},
  tools: [
    {
      name: "lookup",
      description: "Look up an issue by key.",
      parameters: {
        type: "object",
        properties: { key: { type: "string" } },
        required: ["key"],
        additionalProperties: false,
      },
      execute(args, ctx) {
        // args is the model's arguments; ctx is { workspace, project, sessionId }
        return { key: args.key, title: "…" };
      },
    },
  ],
} satisfies PaladinExtension;
```

Rules the loader enforces: `tools` must be an array, every tool needs a
`name` matching `^[a-zA-Z][a-zA-Z0-9_-]*$`, a non-empty `description`, an
object `parameters` schema, and an `execute` function. A tool name that
collides with another enabled extension is reported and skipped.

## One process per plugin

Each enabled plugin folder gets **one** long-lived Node process, started
on its first call (or its first load, when it has no static tool list) and
reused for every later call in that session and across turns. Calls are
multiplexed by id, so two tool calls at once do not queue behind each
other.

* A cold start misses the call deadline (20 s) and the call fails; the
  process stays up for the next one.
* A call that throws fails that call only. The plugin keeps running.
* A process that crashes fails the calls waiting on it; the next call
  starts a new one (ids are fenced by a generation counter, so a late
  reply from the dead process cannot resolve a live call).
* At most 16 calls run at once; the 17th fails fast with
  `extension busy` instead of queueing inside the plugin.
* An idle process is stopped after `idleMs` (default 5 minutes) unless
  calls are in flight. `idleMs: 0` keeps it for the life of the app.
* Stopping the app sends a `shutdown` line, gives the plugin three
  seconds to run `onDeactivate`, then kills it. On Linux the process also
  dies with the app (`Pdeathsig`), so a crash cannot leave an orphan.

Loose files (`legacy.ts`) are the old path: a `node` process per call, no
warm process, no dependencies. They log a deprecation notice; moving the
file into a folder of the same name keeps the exposed tool names.

Standard output is the protocol. `console.log` and friends are redirected
to stderr by the runner; whatever a plugin prints there is kept (the last
10 KiB) and shown as the `stderr` tail of its runner in Settings → Tools.

## The manifest

`paladin-plugin.json` is optional. Every field is optional; unknown fields
are an error, to catch typos.

| Field | Meaning |
|---|---|
| `entry` | Module path relative to the folder. Default `index.ts`, then `index.js`. Must stay inside the folder, and end in `.ts`, `.mts`, `.js`, `.mjs` or `.cjs`. |
| `tools` | Static tool declarations, so loading needs no process at all. `null` (the default) asks the plugin with a `list` request. |
| `timeoutMs` | Per-call timeout, default 120000, clamped to 1000…300000. |
| `idleMs` | Idle time before the process is stopped, default 300000, clamped to 0…3600000. `0` never stops it. |
| `env` | Extra environment variables for the process. `PATH` is refused; the user's own environment is inherited. |
| `node` | Absolute path to a Node binary for this plugin only. A broken path fails the plugin; it does not fall through. |
| `permissions` | What the plugin says it needs: `network`, `spawn`, `fs-outside-workspace`. |
| `views` | Side-pane panels: at most 4. Each is `{id,title,entry,mount,permissions}`; `entry` is under `ui/` and ends in `.html` (≤ 2 MiB), `mount` is `side-pane`, and `permissions` are `invokeTool`, `invokeToolDestructive`, `events`, `readFile`, `openExternal`. |
| `cards` | Inline result renderers: at most 16. Each is `{tool,entry,inline}`; `entry` is under `ui/`, ends in `.js` (≤ 512 KiB), and `inline` may only be true. |

`permissions` is **advisory**. Paladin shows the list in Settings → Tools;
it does not enforce it. A plugin runs as your user with your network and
filesystem access, which is why every call asks for approval until a rule
allows it. Treat a plugin like any other program you install.

## Node resolution

A warm process needs Node 24 or newer (it strips the TypeScript syntax of
the plugin itself). In order:

1. the manifest's `node`;
2. `ELECTRON_NODE`, which the desktop app sets to its own binary
   (Electron's bundled Node, run through `ELECTRON_RUN_AS_NODE`), so
   plugins work on a machine with no separate Node install;
3. `PALADIN_NODE`, an operator override;
4. `node` on `PATH`, accepted only if `node --version` reports 24 or
   newer.

A setting that is present but broken fails that plugin with a message
naming where it came from; it never silently falls through to another
runtime.

## Dependencies

A plugin that needs npm packages keeps them in its own folder:

```sh
cd <data>/config/extensions/my-plugin
npm install some-package
```

The runner starts with the plugin folder as its working directory, so
`import "some-package"` resolves from `my-plugin/node_modules`. Paladin
never copies or snapshots that folder.

## Panels and cards

A plugin can ship UI inside its folder:

```jsonc
{
  "views": [
    {"id": "board", "title": "Ticket Board", "entry": "ui/board.html",
     "mount": "side-pane", "permissions": ["invokeTool", "events"]}
  ],
  "cards": [
    {"tool": "lookup", "entry": "ui/card.js", "inline": true}
  ]
}
```

Panels open as sandboxed side-pane tabs served over `plugin://<plugin>/<view>/ui/…`;
cards render one tool result in a separate sandbox. Both are local-only.
The full authoring guide, bridge API, permissions, limits and security
model live in [`docs/PLUGIN-UI.md`](../../../docs/PLUGIN-UI.md).

## Permissions

Extension tools are gated like MCP tools. A call asks unless a rule says
otherwise, and the card remembers `jira/lookup` (one tool), `jira/*`
(every tool of the plugin) or `*` (every extension). A loose file counts
as the plugin of the same name, so `legacy.ts` and `legacy/index.ts` are
both covered by `legacy/*`. Rules are saved from
the card with its scope buttons (`session`, `folder`, `global`) or edited
in Settings → Permissions, where the pattern field completes from the
installed plugins. Deny rules always win, even at full access.

Extension tools are left out of read-only mode and plan mode: they run
local code that could change anything.

## Debugging

* **It does not load.** Settings → Tools → Local extensions shows the
  error under the plugin, and the sidecar logs it to stderr with an
  `extension` prefix. A manifest typo names the file and the field.
* **It loads but the tool fails.** The error is the plugin's own message,
  model-visible. Runner output (including a stack from a failed import) is
  the `stderr` tail shown on the plugin row and logged at exit.
* **Changes do not show up.** Declarations are cached until the manifest,
  the entry file or `package.json` changes; press **Reload plugins** in
  Settings → Tools after editing anything else. Reload also stops the warm
  processes and applies from the next turn.
* **Node is not found.** Install Node 24+, set `PALADIN_NODE`, or run the
  app (its Electron binary stands in).

## Limits

| Limit | Value |
|---|---|
| One reply | 1 MiB |
| Concurrency per plugin | 16 calls |
| Call timeout | manifest `timeoutMs`, default 120 s |
| Cold start | 20 s |
| Plugins starting at once | 4 (others queue for up to 30 s) |
| Manifest / `extensions.json` | 64 KiB |
| Runner stderr kept | 10 KiB |
| Runner memory (soft) | 512 MiB, logged; reaped once idle |
