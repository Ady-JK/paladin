import * as api from "../core/api";
import { toast } from "../ui/toast";

const images = new Map<string, string[]>();
const cache = new Map<string, string>();
const maxCachedImages = 12;
let shown: { sessionId: string; index: number } | null = null;

function cacheImage(path: string, data: string): void {
  cache.delete(path);
  cache.set(path, data);
  while (cache.size > maxCachedImages) cache.delete(cache.keys().next().value!);
}

function paths(sessionId: string): string[] {
  let list = images.get(sessionId);
  if (!list) {
    list = [];
    images.set(sessionId, list);
  }
  return list;
}

export function registerImage(sessionId: string, path: string): void {
  const list = paths(sessionId);
  if (!list.includes(path)) list.push(path);
}

function overlay(): HTMLElement {
  let root = document.querySelector<HTMLElement>("#sessionImageViewer");
  if (root) return root;
  root = document.createElement("div");
  root.id = "sessionImageViewer";
  root.className = "image-viewer";
  root.hidden = true;
  root.innerHTML =
    '<button class="image-viewer-close" aria-label="Close image viewer">×</button><button class="image-viewer-prev" aria-label="Previous image">‹</button><img alt="Session image"><button class="image-viewer-next" aria-label="Next image">›</button><span class="image-viewer-count"></span>';
  root.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest(".image-viewer-prev")) void move(-1);
    else if (target.closest(".image-viewer-next")) void move(1);
    else if (target === root || target.closest(".image-viewer-close")) close();
  });
  document.addEventListener("keydown", (event) => {
    if (!shown) return;
    if (event.key === "Escape") close();
    else if (event.key === "ArrowLeft") void move(-1);
    else if (event.key === "ArrowRight") void move(1);
  });
  document.body.append(root);
  return root;
}

function close(): void {
  shown = null;
  overlay().hidden = true;
}

async function show(): Promise<void> {
  if (!shown) return;
  const { sessionId, index } = shown;
  const path = paths(sessionId)[index];
  const root = overlay();
  root.hidden = false;
  root.querySelector<HTMLElement>(".image-viewer-count")!.textContent = `${index + 1} / ${paths(sessionId).length}`;
  try {
    let data = cache.get(path);
    if (!data) {
      data = await api.sessionImage(sessionId, path);
      cacheImage(path, data);
    }
    if (shown?.sessionId === sessionId && shown.index === index)
      root.querySelector<HTMLImageElement>("img")!.src = data;
  } catch (error) {
    toast(String(error), "error");
    close();
  }
}

async function move(delta: number): Promise<void> {
  if (!shown) return;
  const count = paths(shown.sessionId).length;
  if (!count) return;
  shown.index = (shown.index + delta + count) % count;
  await show();
}

// mountUserImage shows a sent image above the user's message bubble: the
// thumbnails live in a strip placed before the bubble in the row.
export function mountUserImage(row: HTMLElement, sessionId: string, path: string, name: string): void {
  let strip = row.querySelector<HTMLElement>(":scope > .user-images");
  if (!strip) {
    strip = document.createElement("div");
    strip.className = "user-images";
    const bubble = row.querySelector<HTMLElement>(":scope > .bubble-user");
    if (bubble) bubble.before(strip);
    else row.append(strip);
  }
  mountImage(strip, sessionId, path, name);
}

export function mountImage(target: HTMLElement, sessionId: string, path: string, name: string): void {
  registerImage(sessionId, path);
  const button = document.createElement("button");
  button.type = "button";
  button.className = "session-image-thumb";
  button.dataset.imagePath = path;
  button.title = name;
  button.setAttribute("aria-label", `Open ${name}`);
  const image = document.createElement("img");
  image.alt = name;
  button.append(image);
  target.append(button);
  button.addEventListener("click", () => {
    shown = { sessionId, index: paths(sessionId).indexOf(path) };
    void show();
  });
  const cached = cache.get(path);
  if (cached) image.src = cached;
  else
    void api
      .sessionImage(sessionId, path)
      .then((data) => {
        cacheImage(path, data);
        if (button.isConnected) image.src = data;
      })
      .catch(() => {
        button.remove();
      });
}
