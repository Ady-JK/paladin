// tags.ts owns the composer's tagged items: a file, folder or image
// picked with `@` (or the attach picker) becomes a preview card above
// the input instead of a raw absolute path in the text. The card is
// only the composer preview; sending still puts the resolved absolute
// path in the outgoing message, which is what the agent reads.

import * as api from "../core/api";
import { inputEl, tagCardsEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { FileEntry, FilePreview } from "../core/types";
import { icon } from "../icons";

type TaggedItem = {
  key: number;
  path: string;
  rel: string;
  name: string;
  isDir: boolean;
  outside: boolean;
  // token is the compact @name left in the composer, removed with the
  // card so a deleted tag cannot linger in the text.
  token?: string;
};

let items: TaggedItem[] = [];
let nextKey = 1;
// previews caches per path: a tag stays on screen while the picker is
// used again, and re-reading the same file would be wasteful.
const previews = new Map<string, FilePreview | null>();

// baseName is the card's title: the last path segment, or the folder
// name for a trailing-slash path.
function baseName(path: string): string {
  const clean = path.replace(/[\\/]+$/, "");
  const cut = Math.max(clean.lastIndexOf("/"), clean.lastIndexOf("\\"));
  return cut < 0 ? clean : clean.slice(cut + 1);
}

function add(item: TaggedItem): void {
  if (items.some((tag) => tag.path === item.path)) {
    paint();
    return;
  }
  items.push(item);
  paint();
  void loadPreview(item);
}

// addTag records a mention-picker row.
export function addTag(entry: FileEntry): void {
  add({
    key: nextKey++,
    path: entry.resolvedPath,
    rel: entry.path.replace(/[\\/]+$/, ""),
    name: baseName(entry.resolvedPath) || entry.display,
    isDir: entry.isDir,
    outside: entry.outside,
    token: `@${entry.display.replace(/[\\/]+$/, "")}`,
  });
}

// addTagPath records a path picked from the attach picker.
export function addTagPath(path: string, isDir: boolean): void {
  add({
    key: nextKey++,
    path,
    rel: baseName(path),
    name: baseName(path),
    isDir,
    outside: false,
  });
}

async function loadPreview(item: TaggedItem): Promise<void> {
  if (item.isDir || previews.has(item.path)) {
    paint();
    return;
  }
  const preview = await api.filePreview(state.cwd || null, item.path).catch(() => null);
  previews.set(item.path, preview);
  if (items.some((tag) => tag.key === item.key)) paint();
}

function previewBody(item: TaggedItem): string {
  if (item.isDir) return `<div class="tag-card-note">${icon("tl-folder", "h-4 w-4")} folder</div>`;
  const preview = previews.get(item.path);
  if (preview === undefined) return `<div class="tag-card-note">reading…</div>`;
  if (!preview) return "";
  if (preview.kind === "image" && preview.dataUrl) {
    return `<img class="tag-card-thumb" src="${escapeHtml(preview.dataUrl)}" alt="" />`;
  }
  if (preview.kind === "text" && preview.lines?.length) {
    return preview.lines.map((line) => `<div class="tag-card-line">${escapeHtml(line)}</div>`).join("");
  }
  const note =
    preview.kind === "outside"
      ? "outside the workspace"
      : preview.kind === "missing"
        ? "not found"
        : preview.kind === "binary"
          ? "binary preview"
          : "";
  return note ? `<div class="tag-card-note">${escapeHtml(note)}</div>` : "";
}

function paint(): void {
  tagCardsEl.replaceChildren();
  for (const item of items) {
    const card = document.createElement("div");
    card.className = "tag-card";
    card.dataset.tagKey = String(item.key);
    card.title = item.path;
    card.innerHTML =
      `<div class="tag-card-head"><span class="tag-card-icon">${icon(item.isDir ? "tl-folder" : "tl-file", "h-3.5 w-3.5")}</span>` +
      `<span class="tag-card-name">${escapeHtml(item.name)}</span>` +
      `<button type="button" class="tag-card-x" aria-label="Remove ${escapeHtml(item.name)}">×</button></div>` +
      (item.rel && item.rel !== item.name ? `<div class="tag-card-rel">${escapeHtml(item.rel)}</div>` : "") +
      `<div class="tag-card-body">${previewBody(item)}</div>`;
    card.querySelector<HTMLButtonElement>(".tag-card-x")!.addEventListener("click", () => {
      if (item.token) {
        const at = inputEl.value.indexOf(item.token);
        if (at >= 0) {
          inputEl.value = `${inputEl.value.slice(0, at)}${inputEl.value.slice(at + item.token.length)}`.trim();
        }
      }
      items = items.filter((tag) => tag.key !== item.key);
      paint();
    });
    tagCardsEl.append(card);
  }
}

// hasTags reports whether a send would carry at least one tagged item.
export function hasTags(): boolean {
  return items.length > 0;
}

// tagPaths is the resolved absolute paths to append to the outgoing
// message, oldest tag first.
export function tagPaths(): string[] {
  return items.map((item) => item.path);
}

// Include tagged names in the visible message even when the user typed
// a separate prompt after choosing files from the picker.
export function withTagLabels(text: string): string {
  const missing = items.map((item) => `@${item.name}${item.isDir ? "/" : ""}`).filter((label) => !text.includes(label));
  return [text, missing.join(" ")].filter(Boolean).join("\n");
}

// Tagged images are copied to the session so they survive changes to
// the original file and are available to both the model and the viewer.
export async function uploadTaggedImages(sessionId: string, cwd: string): Promise<api.SessionAttachment[]> {
  const tagged = items.filter((item) => !item.isDir);
  const saved: api.SessionAttachment[] = [];
  for (const item of tagged) {
    const image = await api.saveTaggedImage(sessionId, cwd, item.path);
    if (image) saved.push(image);
  }
  return saved;
}

// clearTags empties the strip after a send.
export function clearTags(): void {
  items = [];
  paint();
}

// tagsLabel names the tags for an otherwise empty message, so the user
// bubble still shows what was sent.
export function tagsLabel(): string {
  return items.map((item) => `@${item.name}${item.isDir ? "/" : ""}`).join(" ");
}

// initTags paints the (empty) strip once the composer exists.
export function initTags(): void {
  paint();
}
