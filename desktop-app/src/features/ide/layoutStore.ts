import * as api from "../../core/api";
import type { EditorState } from "@codemirror/state";

export type IDEBuffer = {
  cwd: string;
  path: string;
  content: string;
  saved: string;
  version: string;
  saving: boolean;
  lineEnding: "\n" | "\r\n";
  editorState: EditorState;
};

export const bufferKey = (root: string, path: string): string => JSON.stringify([root, path]);
export const bufferDirty = (buffer: IDEBuffer): boolean => buffer.content !== buffer.saved;

export function snapshotBuffers(root: string, buffers: Map<string, IDEBuffer>): api.IDELayout["files"] {
  return [...buffers.entries()]
    .filter(([, buffer]) => buffer.cwd === root)
    .map(([key, buffer]) => {
      const path = (JSON.parse(key) as [string, string])[1];
      return bufferDirty(buffer)
        ? {
            path,
            draft: {
              content: buffer.content,
              saved: buffer.saved,
              version: buffer.version,
              lineEnding: buffer.lineEnding,
            },
          }
        : { path };
    });
}

export function validateLayout(root: string, layout: api.IDELayout): void {
  if (!Array.isArray(layout.files) || layout.files.length > 500) throw new Error("Invalid saved file list");
  for (const item of layout.files) {
    if (!item || typeof item.path !== "string" || !inWorkspace(root, item.path))
      throw new Error("Invalid saved file path");
    if (
      item.draft &&
      (typeof item.draft.content !== "string" ||
        typeof item.draft.saved !== "string" ||
        typeof item.draft.version !== "string")
    )
      throw new Error("Invalid saved draft");
  }
}

export async function restoreBuffers(
  root: string,
  layout: api.IDELayout,
  buffers: Map<string, IDEBuffer>,
  current: () => boolean,
  editorState: (path: string, content: string) => EditorState,
): Promise<void> {
  for (const item of layout.files) {
    if (!current()) return;
    const key = bufferKey(root, item.path);
    if (buffers.has(key)) continue;
    try {
      const draft = item.draft;
      if (draft) {
        buffers.set(key, {
          cwd: root,
          path: item.path,
          content: draft.content,
          saved: draft.saved,
          version: draft.version,
          lineEnding: draft.lineEnding === "\r\n" ? "\r\n" : "\n",
          saving: false,
          editorState: editorState(item.path, draft.content),
        });
      } else {
        const file = await api.ideReadFile(root, item.path);
        if (!current() || buffers.has(key)) continue;
        const content = file.content.replace(/\r\n/g, "\n");
        buffers.set(key, {
          cwd: root,
          path: file.path,
          content,
          saved: content,
          version: file.version,
          lineEnding: file.content.includes("\r\n") ? "\r\n" : "\n",
          saving: false,
          editorState: editorState(file.path, content),
        });
      }
    } catch {
      // A moved or unreadable clean file is skipped; a saved draft is
      // restored above without touching the workspace file.
    }
  }
}

// Writes are serialized so a slow earlier save cannot replace a newer draft.
export class LayoutStore {
  private readonly pending = new Map<string, api.IDELayout>();
  private running = false;
  constructor(private readonly onError: (error: unknown) => void) {}

  save(cwd: string, layout: api.IDELayout): void {
    this.pending.set(cwd, layout);
    void this.drain();
  }

  private async drain(): Promise<void> {
    if (this.running) return;
    this.running = true;
    try {
      while (this.pending.size) {
        const [cwd, layout] = this.pending.entries().next().value!;
        this.pending.delete(cwd);
        try {
          await api.ideLayoutSet(cwd, layout);
        } catch (error) {
          this.onError(error);
        }
      }
    } finally {
      this.running = false;
    }
  }
}

// Saved layouts are data, not trusted instructions. Only paths beneath the
// workspace are restored, and all disk reads and writes still go through Go.
export function inWorkspace(cwd: string, path: string): boolean {
  const root = cwd.replace(/\\/g, "/").replace(/\/+$/, "") || "/";
  const candidate = path.replace(/\\/g, "/");
  const prefix = root === "/" ? "/" : `${root}/`;
  return candidate.startsWith(prefix) && !candidate.split("/").some((part) => part === ".." || part === ".");
}
