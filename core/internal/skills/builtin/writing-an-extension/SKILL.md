---
name: writing-an-extension
description: Add a local Paladin extension tool: scaffold a plugin, define safe schemas and execute(), enable it, approve calls and debug failures.
---
# Writing a Paladin extension tool

Use this when the user wants Paladin to gain a tool that is not built in: a
lookup, a wrapper around a local script, a company API, a file format reader.
The tool lives in a **local extension plugin**, in the user's data directory.
Do not add it to `core/internal/tools` for this: that is Paladin's own code and
every user would ship it in the binary.

An extension is trusted local code. You are writing a small Node module that
Paladin imports once and calls over a long-lived process. The model sees each
tool like a normal tool, and every call passes the permission gate.

## 0. What you are building

```
<data>/config/extensions.json              # {"enabled": ["my-plugin"]}
<data>/config/extensions/
  my-plugin/                               # the plugin directory (the name matters)
    paladin-plugin.json                    # manifest (optional)
    index.ts                               # entry module (default)
    paladin.d.ts                           # type definitions (generated)
    package.json                           # optional, for dependencies
    node_modules/                          # yours, installed by you
```

The plugin's tool is exposed to the model as
`extension__<pluginDirectoryName>__<toolName>`. The plugin itself only uses its
own short tool name. Permission rules use the short form `<plugin>/<tool>`,
`<plugin>/*` or `*`.

One plugin gets **one warm Node process**. It starts on the first call, imports
the module once, and serves later calls from the same process. Calls are
multiplexed by the Go side but the runner handles them in arrival order, one at
a time, so a slow `execute()` blocks the plugin's other calls.

Prefer an extension when the tool is personal, local, or specific to one user.
Use MCP when an external server already speaks MCP. Use core tools only when
changing Paladin itself.

## 1. Find the data directory

The data directory is **not** the repo. Resolve it in this order:

1. `$PALADIN_DATA_DIR` when set.
2. The OS config directory plus `/paladin`:
   - Linux: `~/.config/paladin`
   - macOS: `~/Library/Application Support/paladin`
   - Windows: `%AppData%\paladin`
3. The sidecar's stderr line `agent-host ready dir=…` names the exact path.
4. In the app, Settings → Tools shows a plugin's full path on hover.

```sh
DATA="${PALADIN_DATA_DIR:-$HOME/.config/paladin}"   # Linux example
ls "$DATA/config/extensions"
```

Never put a plugin in the Paladin source tree; the app never reads it there.

## 2. Scaffold the plugin

From a Paladin checkout, build the sidecar once and run its init command:

```sh
cd core
go build -o bin/agent-host ./cmd/agent-host
./bin/agent-host extensions init --example-plugin
```

It prints the extensions directory. `--example-plugin` writes the full
directory example. Without it you get only the loose-file example.

| File | Purpose |
|---|---|
| `config/extensions.json` | the enabled list; starts as `{"enabled": []}` |
| `config/extensions/paladin.d.ts` | types for loose files |
| `config/extensions/hello.ts` | a loose-file example |
| `config/extensions/hello/index.ts` | the directory-plugin example |
| `config/extensions/hello/paladin-plugin.json` | an example manifest |
| `config/extensions/hello/paladin.d.ts` | types for the plugin |

Init never overwrites an existing file and **never enables the plugin**. That
is deliberate: enabling is the user's explicit trust decision.

If you cannot run the CLI, create the directory and files by hand; the layout
above is the whole requirement.

## 3. Write the plugin module

Start from this working plugin. It is read-only, validates its input, confines
reads to the session workspace, and returns a plain object.

`<data>/config/extensions/file-stats/index.ts`:

```ts
import { readFile, realpath, stat } from "node:fs/promises";
import { isAbsolute, relative, resolve } from "node:path";
import type { PaladinExtension, ToolContext } from "./paladin.d.ts";

// inside reports whether target is under root (not root itself).
function inside(root: string, target: string): boolean {
  const rel = relative(root, target);
  return rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
}

// resolveInside turns the model's path into a real path under the workspace.
// The model's arguments are untrusted: schema validation is guidance, not a
// security boundary.
async function resolveInside(ctx: ToolContext, input: unknown): Promise<string> {
  if (typeof input !== "string" || !isAbsolute(input)) {
    throw new Error("path must be an absolute path");
  }
  if (!ctx.workspace) {
    throw new Error("no workspace for this session");
  }
  const root = await realpath(ctx.workspace);
  const target = await realpath(resolve(input));
  if (target !== root && !inside(root, target)) {
    throw new Error("path is outside the workspace");
  }
  return target;
}

export default {
  tools: [
    {
      name: "file_stats",
      description:
        "Report a workspace file's byte size, line count and modification time. " +
        "Read-only. Use it before reading a large file.",
      parameters: {
        type: "object",
        properties: {
          path: { type: "string", description: "Absolute path to a file inside the workspace." },
        },
        required: ["path"],
        additionalProperties: false,
      },
      async execute(args: { path: string }, ctx: ToolContext) {
        const path = await resolveInside(ctx, args?.path);
        const info = await stat(path);
        if (!info.isFile()) {
          throw new Error("not a regular file");
        }
        if (info.size > 2 * 1024 * 1024) {
          throw new Error("file is larger than 2 MiB");
        }
        const text = await readFile(path, "utf8");
        const lines = text === "" ? 0 : text.split("\n").length;
        return { path, bytes: info.size, lines, modified: info.mtime.toISOString() };
      },
    },
  ],
} satisfies PaladinExtension;
```

What each part does:

- **`export default`** must be an object with a `tools` array. Anything else
  makes the plugin fail to load with `default export needs a tools array`.
- **`name`** is the plugin-side name: `^[a-zA-Z][a-zA-Z0-9_-]*$`. It must be
  unique inside the plugin. Paladin prefixes it, so a short name is best.
- **`description`** is what the model reads to decide when to call it. Say what
  it does, that it is read-only when it is, and when not to use it.
- **`parameters`** must be a JSON Schema object with `"type": "object"`. The
  model can still send extra keys or wrong types; validate again in `execute`.
- **`execute(args, context)`** receives the parsed arguments and
  `{workspace, project, sessionId}`. It may be async.
- **Return** a string (sent verbatim), or an object/array (JSON-stringified), or
  `undefined` (becomes `null`). Return structured objects, not prose, when the
  model needs to reason about fields.
- **Throw** an `Error` to fail the call. The model sees the error text and the
  plugin stays alive. The runner includes the stack, so make the first line
  the useful message and never put secrets in it.

### Context and process facts

- `ctx.workspace` is the session's workspace (absolute). `ctx.project` is the
  folder project state keys on, which differs for worktree sessions.
  `ctx.sessionId` identifies the chat.
- `process.cwd()` is the **plugin directory**, not the workspace. This is what
  makes bare imports resolve from the plugin's own `node_modules`. Use
  `ctx.workspace` and absolute paths for files.
- The plugin process has the user's environment, network, and filesystem
  access. There is no sandbox around it.
- `console.log`, `info`, `debug` and `warn` are redirected to stderr.
  **Never write to stdout**: stdout is the JSONL protocol between Paladin and
  the runner, and a stray write corrupts replies. Use `console.error` for
  diagnostics; its last 10 KiB is kept and shown in Settings → Tools.
- Calls to one plugin run in arrival order, one at a time in the runner. Do not
  depend on two calls of the same plugin overlapping.
- There is no cancellation signal in `execute`. A call that exceeds the
  timeout fails for the model, but the plugin keeps running; keep work bounded.
- Multiple tools are fine: put each object in the `tools` array. A tool that
  throws only fails that call.

### Optional lifecycle hooks

```ts
export default {
  // Runs once when this plugin's process starts.
  onActivate(ctx: { pluginDir: string; pid: number; workspace: string }) {
    // Read a config file from ctx.pluginDir, warm a cache, etc.
  },
  // Runs once before the process stops.
  onDeactivate() {
    // Close handles.
  },
  tools: [/* ... */],
} satisfies PaladinExtension;
```

`onActivate` runs after the module imports and before the first call. It gets
`pluginDir` and `pid`; its `workspace` comes from `PALADIN_WORKSPACE`, which
Paladin does not set today, so treat it as empty and use `ctx.workspace` inside
`execute` instead. If `onActivate` throws, the process exits and the plugin is
reported as failed.

## 4. Write the manifest

`<data>/config/extensions/file-stats/paladin-plugin.json`:

```json
{
  "entry": "index.ts",
  "timeoutMs": 15000,
  "idleMs": 300000,
  "permissions": []
}
```

Every field is optional, but unknown fields are an error (to catch typos), so
only use names from this table:

| Field | Meaning |
|---|---|
| `entry` | Module path relative to the plugin directory. Default `index.ts`, then `index.js`. Must stay inside the directory and end in `.ts`, `.mts`, `.js`, `.mjs` or `.cjs`. |
| `tools` | A static array of `{name, description, parameters}`. When set and not `null`, Paladin never asks the plugin for its list, so loading costs no process. The entries must match the exported tools or calls fail with `tool not found`. Leave it out when unsure. |
| `timeoutMs` | Per-call timeout. Default `120000`; clamped to `1000…300000`. |
| `idleMs` | Stop the warm process after this long unused. Default `300000`; clamped to `0…3600000`. `0` keeps it for the life of the app, which is handy while developing. |
| `env` | Extra environment variables for the process. `PATH` is refused; the user's environment is inherited. |
| `node` | Absolute path to a Node binary for this plugin only. A bad value fails the plugin loudly instead of falling through. |
| `permissions` | Advisory capabilities: `"network"`, `"spawn"`, `"fs-outside-workspace"`. Paladin displays them; it does not enforce them. |

Static declarations are a fast path, not documentation. This shape:

```json
{
  "tools": [
    {
      "name": "file_stats",
      "description": "Report a workspace file's size and line count.",
      "parameters": {
        "type": "object",
        "properties": { "path": { "type": "string" } },
        "required": ["path"],
        "additionalProperties": false
      }
    }
  ]
}
```

must agree with the `tools` array in the module. If you add a tool to the
module, add it here too or remove `tools` and let Paladin list the module.

The plugin directory name must match `^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`, must be a
real directory (not a symlink escaping `config/extensions`), and the manifest
must be at most 64 KiB.

## 5. Enable it

Edit `<data>/config/extensions.json`:

```json
{
  "enabled": ["file-stats"]
}
```

Rules:

- Use the **directory name** for a plugin, or a filename like `legacy.ts` for
  the old loose-file path.
- Names are matched exactly and duplicates are skipped.
- An enabled name that does not exist produces an error row, not a crash.
- The scaffolded `hello/` is not enabled for you.

Then:

1. In the app, open **Settings → Tools**.
2. Press **Reload plugins** (or restart the app). This re-reads
   `extensions.json` and every plugin, and stops the warm processes.
3. The new tool applies to the **next turn**. A turn already running keeps the
   tool set it started with. Nothing else needs restarting.

### New tools in an existing chat

A session remembers the tools it has seen. A tool that appears after the
session's first turn is **held back as pending** so the model's prompt cache is
not invalidated without consent. In an existing chat you will see an "N new
tools" banner with **Add now / Review / Later**. Press **Add now** before
expecting the model to use it. A brand-new chat has no pending step and sees
the tool immediately.

The tool also appears in **Settings → Tools → Extension tools** and can be
switched off like any other tool.

## 6. Make the first call

1. In a new chat, say what you want in plain language and name the tool, e.g.
   "Use `file_stats` on `/home/me/project/README.md`". The model sees the
   exposed name `extension__file-stats__file_stats`.
2. The first call opens an approval card under the default (Auto) permission
   preset: extension code runs without the sandbox, so each call asks until a
   rule exists. (Full access allows extension calls without a card; that is a
   deliberate user choice.) The card names the plugin and tool and shows the
   arguments.
3. Choose the narrowest grant that is still useful:
   - **Once** — this call only.
   - **This session** — this chat.
   - **This folder** — this workspace.
   - **Everywhere** (global) — all workspaces; use sparingly.
   - **Deny** — block it.

The card remembers `file-stats/file_stats` by default. A rule saved as
`file-stats/*` allows every tool of the plugin, and `*` allows every extension
tool. Deny always wins, even at full access. Rules can also be edited in
**Settings → Permissions**, where the pattern field completes installed
plugins.

Extension tools are excluded from read-only mode and plan mode: they run local
code that could change anything.

## 7. Iterate without fighting the cache

- Declarations are cached per process. A change to `paladin-plugin.json`, the
  entry file, or `package.json` invalidates the cache automatically.
- A change to a **helper module** the entry imports does **not** invalidate it.
  Press **Reload plugins** after editing helpers, or restart the app.
- **Reload plugins** stops warm processes. In-flight calls finish on their
  process; new calls start a fresh one. A running turn keeps the old tool set.
- While developing, set `"idleMs": 0` so the process stays warm between calls,
  and lower `timeoutMs` so a broken call fails fast.
- Declarations cost no process when `tools` is static, so a static manifest is
  the fastest cold start.

## 8. Dependencies and TypeScript

A plugin may keep npm packages in its own directory:

```sh
cd "$DATA/config/extensions/file-stats"
npm init -y
npm install some-package
```

The runner starts with the plugin directory as its working directory, so
`import "some-package"` resolves from `file-stats/node_modules`. Paladin never
copies or snapshots that folder; it is the author's responsibility.

TypeScript needs no build step. Node 24 strips erasable type syntax when it
imports the `.ts` entry, so ship the source:

- Use `import type` for types and avoid runtime-only TypeScript features.
- Do not use `enum`, `namespace`, parameter properties (`constructor(private x)`)
  or `import =`; they are not erasable and Node cannot strip them.
- Set `"type": "module"` in the plugin's `package.json` if you add one, and use
  ESM `import`/`export`.

Node 24 or newer is required. Resolution order is: manifest `node`, then
`ELECTRON_NODE` (the desktop app runs its own Electron binary in Node mode),
then `PALADIN_NODE`, then `node` on `PATH` if its major version is at least 24.

## 9. Test the plugin

Test the module outside Paladin first. Node can import the entry directly:

```sh
cd "$DATA/config/extensions/file-stats"
node --input-type=module -e '
  const mod = (await import("./index.ts")).default;
  const tool = mod.tools.find((t) => t.name === "file_stats");
  console.log(JSON.stringify(await tool.execute(
    { path: process.argv[1] },
    { workspace: process.cwd(), project: process.cwd(), sessionId: "test" }
  ), null, 2));
' "$PWD/index.ts"
```

Then test through Paladin:

1. Reload plugins and check the extension row shows **idle** or **running**, its
   declared capabilities and its tool count.
2. Start a turn that calls it and approve the card.
3. Check the result in the transcript. Extension results use Paladin's generic
   tool card; returning an object gives a readable key/value body.
4. Force the failure paths and confirm each one is understandable: missing
   argument, relative path, path outside the workspace, a file that does not
   exist, a file over the size cap, and a thrown error.

For repeatable tests, put a `node:test` file next to the plugin and run
`node --test`; the module is a plain ESM module, so no Paladin harness is
needed.

## 10. Limits and failure behavior

| Limit | Value |
|---|---|
| One reply | 1 MiB |
| Concurrent calls per plugin | 16 on the Go side; the runner still handles them one at a time |
| Call timeout | manifest `timeoutMs`, default 120 s, max 300 s |
| Cold start | 20 s, then the call proceeds under its own timeout |
| Plugins starting at once | 4; the rest queue for up to 30 s |
| Manifest / `extensions.json` | 64 KiB |
| Runner stderr kept | 10 KiB |
| Runner memory (soft) | 512 MiB, logged; reaped once idle |

- A **cold start** that misses its budget fails that call; the process may stay
  up for the next one.
- A **thrown error** fails that call only.
- A **crash** fails the calls waiting on it. The next call starts a fresh
  process; ids are fenced by a generation counter so a late reply cannot
  resolve a live call.
- The **17th concurrent call** fails fast with `extension busy`.
- **`idleMs`** stops only idle processes, never one with calls in flight.
- Stopping the app sends `shutdown`, gives `onDeactivate` three seconds, then
  kills the process. On Linux it also dies with the app.

## 11. Security checklist

- Treat every argument as untrusted. The JSON Schema is a hint, not validation.
- Resolve and confine paths when the tool is workspace-scoped: reject relative
  paths, `..`, and symlinks that escape the root. Do not concatenate strings to
  build paths; use `resolve`/`relative`/`realpath`.
- Never pass model text to a shell. Use `execFile`/`spawn` with an argument
  array and an allow-list of commands.
- Do not read credentials or tokens unless the tool exists to do exactly that.
- Do not print secrets: thrown messages go to the model and the transcript;
  stderr is shown in Settings.
- Cap output before returning it. A 1 MiB reply fails the call, and a huge
  result burns context.
- Declare `network`, `spawn` and `fs-outside-workspace` honestly even though
  the list is advisory. It is the user's only signal in the UI.
- Write the description for the model, not for humans: state the scope, the
  side effects, and when not to call it.
- Prefer read-only tools. A destructive tool still asks every time unless the
  user grants a rule, but a clear name and description prevent accidents.

## 12. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Plugin missing from Settings → Tools | Name not in `extensions.json`, or the name does not match the directory / file. Check the enabled list and press **Reload plugins**. |
| Row says **failed** | Hover the row and read the error plus its stderr tail. Common causes: manifest typo or unknown field, missing entry, entry outside the folder, Node below 24, directory name invalid, symlink escaping `config/extensions`. |
| `duplicate extension tool` | Two enabled plugins expose the same name. Rename the tool or disable one. |
| Tool loads but a call says `tool not found` | Static `tools` in the manifest drifted from the exported array. Fix the list or remove it. |
| Edits do not appear | The edited file is a helper module, which is not fingerprinted. Press **Reload plugins**. |
| The model does not see the tool | In an existing chat it is pending: press **Add now** in the banner. Also check the tool is switched on in Settings → Tools. |
| Every call asks for approval | No rule yet, or a deny rule matches. Save a `plugin/tool` rule, and check Settings → Permissions for a deny. |
| `extension busy` | More than 16 calls are waiting on one plugin. Reduce parallelism or split plugins. |
| Timeout | A slow cold start or a slow `execute`. Raise `timeoutMs` up to 300 000, or make the operation bounded. The plugin may still be working after the call fails. |
| `extension runner exited` | The process crashed or was killed. The next call restarts it. Check the stderr tail for the real error. |
| Calls queue behind one slow call | One runner handles calls in arrival order. Make the call fast or move the slow work to its own plugin. |
| Output truncated or the call fails at 1 MiB | Return a summary or a path, not the whole payload. |
| Loose file logs a deprecation | Move it into a directory; see below. |

## 13. Migrating a loose file

Loose modules (`foo.ts` listed directly in `extensions.json`) still work on
the old spawn-per-call path: no warm process, no dependencies, a `node`
process per call. They are deprecated.

To migrate without breaking the model's habit:

1. Create `config/extensions/foo/`.
2. Move `foo.ts` to `config/extensions/foo/index.ts`.
3. Add `paladin-plugin.json` if you need `timeoutMs`, `idleMs`, `env` or
   dependencies.
4. Change `extensions.json` from `"foo.ts"` to `"foo"`.
5. Reload plugins.

The exposed name stays `extension__foo__<tool>`, so existing permission rules
and the model's tool list remain valid.

## 14. Final checklist

- [ ] Plugin lives under `<data>/config/extensions/<name>/`, never in the repo.
- [ ] Directory name matches `^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`.
- [ ] `export default` has a `tools` array; each tool has a valid name, a
      non-empty model-facing description, and an object schema.
- [ ] `execute` validates arguments, confines paths, and returns a small
      structured result.
- [ ] No direct stdout writes; diagnostics use `console.error`.
- [ ] Manifest names only known fields; `tools` (if present) matches the
      exports; timeouts and idle values are within range.
- [ ] `extensions.json` lists the plugin.
- [ ] Reload plugins, then accept the new tool in the current chat if the
      banner appears.
- [ ] Approve the first call with the narrowest useful rule.
- [ ] Test the happy path and the failure paths, including a timeout and a
      path outside the workspace.
- [ ] Report the exposed name, the rule used, and any limits the user should
      know about.
