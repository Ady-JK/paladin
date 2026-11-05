// pluginHost.ts serves sandboxed plugin UI from `plugin://` URLs.
//
// It deliberately re-reads extensions.json and each manifest instead of
// trusting the sidecar: the renderer's plugin frames are untrusted, and
// the files they load must never escape the plugin's ui/ folder. The
// validation here mirrors core/internal/extension/ui.go; fixture tests
// keep the two in step.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// paladinDataDir mirrors config.DataDir in the Go core so main can find
// the same extensions without an RPC.
export function paladinDataDir(
  env: NodeJS.ProcessEnv = process.env,
  platform: NodeJS.Platform = process.platform,
  home: string = os.homedir(),
): string {
  const override = (env.PALADIN_DATA_DIR ?? "").trim();
  if (override) return override;
  if (platform === "win32") {
    const base = env.APPDATA || path.join(home, "AppData", "Roaming");
    return path.join(base, "paladin");
  }
  if (platform === "darwin") return path.join(home, "Library", "Application Support", "paladin");
  return path.join(env.XDG_CONFIG_HOME || path.join(home, ".config"), "paladin");
}

const MAX_MANIFEST = 64 << 10;
const MAX_VIEW = 2 << 20;
const MAX_CARD = 512 << 10;
const MAX_ASSET = 8 << 20;
const VIEW_ID = /^[a-z][a-z0-9_-]{0,31}$/;
const PLUGIN_NAME = /^[a-zA-Z][a-zA-Z0-9_-]{0,63}$/;
const TOOL_NAME = /^[a-zA-Z][a-zA-Z0-9_-]*$/;
const VIEW_PERMS = new Set(["invokeTool", "invokeToolDestructive", "events", "readFile", "openExternal"]);

const MIME: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
};

const CSP =
  "default-src 'none'; script-src 'self' plugin:; style-src 'self' 'unsafe-inline' plugin:; " +
  "img-src 'self' data: blob: plugin:; font-src 'self' data: plugin:; media-src 'self' data: blob: plugin:; " +
  "connect-src 'none'; frame-src 'none'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'none'";

export type HostedView = {
  plugin: string;
  id: string;
  title: string;
  entry: string;
  dir: string;
  permissions: string[];
  destructive: boolean;
};

export type HostedCard = {
  plugin: string;
  tool: string;
  exposed: string;
  entry: string;
  dir: string;
};

type ViewInput = { id?: unknown; title?: unknown; entry?: unknown; mount?: unknown; permissions?: unknown };
type CardInput = { tool?: unknown; entry?: unknown; inline?: unknown };
type ManifestInput = { views?: unknown; cards?: unknown };

// PluginHost is the in-memory allow-list of servable plugin UI. refresh()
// is cheap: it reads json and stats a handful of files.
export class PluginHost {
  private views = new Map<string, HostedView>();
  private cards = new Map<string, HostedCard>();
  private errors: string[] = [];
  private statCache = new Map<string, { at: number; ok: boolean }>();

  constructor(private readonly dataDir: string = paladinDataDir()) {
    this.refresh();
  }

  // refresh re-reads extensions.json and every enabled plugin manifest.
  refresh(): void {
    this.views.clear();
    this.cards.clear();
    this.errors = [];
    this.statCache.clear();
    const root = path.join(this.dataDir, "config", "extensions");
    let enabled: unknown;
    try {
      const raw = fs.readFileSync(path.join(this.dataDir, "config", "extensions.json"));
      if (raw.length > MAX_MANIFEST) throw new Error("extensions.json is too large");
      enabled = (JSON.parse(raw.toString("utf8")) as { enabled?: unknown }).enabled;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") this.errors.push(String(error));
      return;
    }
    if (!Array.isArray(enabled)) return;
    const seen = new Set<string>();
    for (const name of enabled) {
      if (typeof name !== "string" || seen.has(name)) continue;
      seen.add(name);
      // Legacy loose files have no manifest and no UI.
      if (!PLUGIN_NAME.test(name)) continue;
      try {
        this.loadPlugin(root, name);
      } catch (error) {
        this.errors.push(`plugin ${name}: ${message(error)}`);
      }
    }
  }

  listViews(): HostedView[] {
    return [...this.views.values()];
  }

  listCards(): HostedCard[] {
    return [...this.cards.values()];
  }

  listErrors(): string[] {
    return [...this.errors];
  }

  view(plugin: string, id: string): HostedView | undefined {
    const view = this.find(this.views, `${plugin}/${id}`);
    if (!view) return undefined;
    // A manifest can change between refreshes; verify once more before
    // a frame is granted access.
    if (!this.within(view.dir, path.join(view.dir, view.entry))) return undefined;
    return view;
  }

  // find looks a key up case-insensitively: URL hosts are lower-cased,
  // while a plugin directory may not be.
  private find<T>(map: Map<string, T>, key: string): T | undefined {
    const exact = map.get(key);
    if (exact) return exact;
    const lower = key.toLowerCase();
    for (const [k, value] of map) {
      if (k.toLowerCase() === lower) return value;
    }
    return undefined;
  }

  // cardScript returns a card renderer's source for inlining into srcdoc.
  // Only the plugin's own declared card can be read.
  cardScript(plugin: string, exposed: string): string | undefined {
    const card = this.find(this.cards, `${plugin}/${exposed}`);
    if (!card) return undefined;
    const target = this.safeFile(card.dir, card.entry, MAX_CARD);
    if (!target) return undefined;
    try {
      return fs.readFileSync(target, "utf8");
    } catch {
      return undefined;
    }
  }

  // handle serves one `plugin://<plugin>/<view>/<path>` request. Every
  // failure is a plain 404: no directory listings, no manifest leaks and
  // no error detail from the plugin folder.
  async handle(request: Request): Promise<Response> {
    const target = this.resolve(request.url);
    if (!target) return notFound();
    let data: Buffer;
    try {
      data = await fs.promises.readFile(target.file);
    } catch {
      return notFound();
    }
    const ext = path.posix.extname(target.file).toLowerCase();
    const type = MIME[ext];
    if (!type) return notFound();
    return new Response(new Uint8Array(data), {
      status: 200,
      headers: {
        "content-type": type,
        "content-security-policy": CSP,
        "x-content-type-options": "nosniff",
        "cache-control": "no-store",
      },
    });
  }

  // resolve maps a URL to a real file inside the view's plugin folder.
  private resolve(raw: string): { view: HostedView; file: string } | null {
    let url: URL;
    try {
      url = new URL(raw);
    } catch {
      return null;
    }
    if (url.protocol !== "plugin:") return null;
    const plugin = url.hostname;
    const parts = splitPath(url.pathname);
    if (!parts) return null;
    const viewId = parts.shift();
    if (!viewId) return null;
    const view = this.view(plugin, viewId);
    if (!view) return null;
    const rel = parts.length ? parts.join("/") : view.entry;
    const file = this.safeFile(view.dir, rel, MAX_ASSET);
    return file ? { view, file } : null;
  }

  // safeFile resolves rel under dir and requires a regular file under
  // ui/, with no dot-segment and no symlink escape.
  private safeFile(dir: string, rel: string, max: number): string | null {
    if (!rel || path.posix.isAbsolute(rel) || rel.includes("\\")) return null;
    const clean = path.posix.normalize(rel);
    if (clean === "." || clean.startsWith("../") || clean.startsWith(".")) return null;
    if (!clean.startsWith("ui/")) return null;
    for (const part of clean.split("/")) {
      if (!part || part === "." || part === ".." || part.startsWith(".")) return null;
    }
    const target = path.resolve(dir, clean);
    if (!this.within(dir, target)) return null;
    let real: string;
    let realDir: string;
    try {
      real = fs.realpathSync(target);
      realDir = fs.realpathSync(dir);
    } catch {
      return null;
    }
    if (!this.within(realDir, real)) return null;
    const cached = this.statCache.get(real);
    if (cached && Date.now() - cached.at < 5000) {
      if (!cached.ok) return null;
    } else {
      let ok: boolean;
      try {
        const info = fs.statSync(real);
        ok = info.isFile() && info.size <= max;
      } catch {
        ok = false;
      }
      this.statCache.set(real, { at: Date.now(), ok });
      if (!ok) return null;
    }
    return real;
  }

  private within(root: string, target: string): boolean {
    const rel = path.relative(root, target);
    return rel !== "" && !rel.startsWith("..") && !path.isAbsolute(rel);
  }

  private loadPlugin(root: string, name: string): void {
    const dir = fs.realpathSync(path.join(root, name));
    if (!this.within(fs.realpathSync(root), dir)) throw new Error("plugin links outside the extensions folder");
    if (!fs.statSync(dir).isDirectory()) throw new Error("not a directory");
    const manifestPath = path.join(dir, "paladin-plugin.json");
    let manifest: ManifestInput = {};
    try {
      const raw = fs.readFileSync(manifestPath);
      if (raw.length > MAX_MANIFEST) throw new Error("manifest is too large");
      manifest = JSON.parse(raw.toString("utf8")) as ManifestInput;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    if (manifest.views !== undefined && !Array.isArray(manifest.views)) throw new Error("views must be an array");
    if (manifest.cards !== undefined && !Array.isArray(manifest.cards)) throw new Error("cards must be an array");
    const views = (manifest.views ?? []) as ViewInput[];
    const cards = (manifest.cards ?? []) as CardInput[];
    if (views.length > 4) throw new Error("at most 4 views are allowed");
    if (cards.length > 16) throw new Error("at most 16 cards are allowed");
    const viewIds = new Set<string>();
    for (const input of views) {
      try {
        const view = this.parseView(dir, name, input);
        if (viewIds.has(view.id)) throw new Error("duplicate view id");
        viewIds.add(view.id);
        this.views.set(`${name}/${view.id}`, view);
      } catch (error) {
        this.errors.push(`plugin ${name}: ${message(error)}`);
      }
    }
    const tools = new Set<string>();
    for (const input of cards) {
      try {
        const card = this.parseCard(dir, name, input);
        if (tools.has(card.tool)) throw new Error("duplicate card tool");
        tools.add(card.tool);
        this.cards.set(`${name}/${card.exposed}`, card);
      } catch (error) {
        this.errors.push(`plugin ${name}: ${message(error)}`);
      }
    }
  }

  private parseView(dir: string, plugin: string, input: ViewInput): HostedView {
    const id = typeof input.id === "string" ? input.id : "";
    if (!VIEW_ID.test(id)) throw new Error("view id is invalid");
    const title = typeof input.title === "string" ? input.title.trim() : "";
    if (!title || [...title].length > 48) throw new Error(`view ${id}: title must be 1 to 48 characters`);
    const mount = typeof input.mount === "string" && input.mount.trim() ? input.mount.trim() : "side-pane";
    if (mount !== "side-pane") throw new Error(`view ${id}: only side-pane is supported`);
    const entry = this.entry(dir, typeof input.entry === "string" ? input.entry : "", ".html", MAX_VIEW);
    const permissions: string[] = [];
    if (input.permissions !== undefined) {
      if (!Array.isArray(input.permissions)) throw new Error(`view ${id}: permissions must be an array`);
      for (const perm of input.permissions) {
        if (typeof perm !== "string" || !VIEW_PERMS.has(perm)) {
          throw new Error(`view ${id}: unknown permission ${JSON.stringify(perm)}`);
        }
        permissions.push(perm);
      }
    }
    return {
      plugin,
      id,
      title,
      entry,
      dir,
      permissions,
      destructive: permissions.includes("invokeToolDestructive"),
    };
  }

  private parseCard(dir: string, plugin: string, input: CardInput): HostedCard {
    const tool = typeof input.tool === "string" ? input.tool : "";
    if (!TOOL_NAME.test(tool)) throw new Error("card tool name is invalid");
    if (input.inline === false) throw new Error(`card ${tool}: only inline cards are supported`);
    const entry = this.entry(dir, typeof input.entry === "string" ? input.entry : "", ".js", MAX_CARD);
    return { plugin, tool, exposed: `extension__${plugin}__${tool}`, entry, dir };
  }

  // entry validates a ui/ path and returns it normalized relative to dir.
  private entry(dir: string, rel: string, ext: string, max: number): string {
    if (!rel || path.isAbsolute(rel) || rel.includes("\\")) throw new Error("entry must be a relative path");
    const clean = path.posix.normalize(rel);
    if (clean === "." || clean.startsWith("..") || !clean.startsWith("ui/")) {
      throw new Error("entry must stay under ui/");
    }
    if (path.posix.extname(clean) !== ext) throw new Error(`entry must end in ${ext}`);
    const file = this.safeFile(dir, clean, max);
    if (!file) throw new Error(`entry ${rel} is missing or too large`);
    return clean;
  }
}

function splitPath(pathname: string): string[] | null {
  const parts: string[] = [];
  for (const raw of pathname.split("/")) {
    if (!raw) continue;
    let part: string;
    try {
      part = decodeURIComponent(raw);
    } catch {
      return null;
    }
    if (!part || part === "." || part === ".." || part.includes("/") || part.includes("\\") || part.includes("\0")) {
      return null;
    }
    parts.push(part);
  }
  return parts;
}

function notFound(): Response {
  return new Response("not found", {
    status: 404,
    headers: { "content-type": "text/plain", "x-content-type-options": "nosniff" },
  });
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
