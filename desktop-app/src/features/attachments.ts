import * as api from "../core/api";
import { toast } from "../ui/toast";

type Pending = {
  file: File;
  preview: string | null;
  saved: api.SessionAttachment | null;
  savedSessionId: string | null;
};
const pending: Pending[] = [];
const cards = document.querySelector<HTMLElement>("#attachmentCards")!;
const composer = document.querySelector<HTMLElement>(".composer-float")!;

function paint(): void {
  cards.replaceChildren();
  for (const entry of pending) {
    const card = document.createElement("div");
    card.className = "attachment-card";
    if (entry.preview) {
      const image = document.createElement("img");
      image.src = entry.preview;
      image.alt = "";
      card.append(image);
    } else {
      const icon = document.createElement("span");
      icon.className = "attachment-file-icon";
      icon.textContent = "⌑";
      card.append(icon);
    }
    const label = document.createElement("span");
    label.textContent = entry.file.name;
    label.title = entry.file.name;
    card.append(label);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.setAttribute("aria-label", `Remove ${entry.file.name}`);
    remove.textContent = "×";
    remove.addEventListener("click", () => {
      pending.splice(pending.indexOf(entry), 1);
      if (entry.preview) URL.revokeObjectURL(entry.preview);
      paint();
    });
    card.append(remove);
    cards.append(card);
  }
}

function addFiles(files: FileList | File[]): void {
  for (const file of Array.from(files)) {
    if (pending.length >= 12) {
      toast("Up to 12 files per message", "error");
      break;
    }
    if (file.size === 0 || file.size > 10 * 1024 * 1024) {
      toast(`${file.name}: file must be 1 byte to 10 MiB`, "error");
      continue;
    }
    const previewable = ["image/png", "image/jpeg", "image/gif", "image/webp"].includes(file.type);
    pending.push({ file, preview: previewable ? URL.createObjectURL(file) : null, saved: null, savedSessionId: null });
  }
  paint();
}

function base64(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 8192)
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  return btoa(binary);
}

export function hasAttachments(): boolean {
  return pending.length > 0;
}

export async function uploadAttachments(
  sessionId: string,
  cwd: string,
): Promise<{ ids: string[]; consume: () => void }> {
  const batch = [...pending];
  const ids: string[] = [];
  for (const entry of batch) {
    if (!entry.saved || entry.savedSessionId !== sessionId) {
      const bytes = new Uint8Array(await entry.file.arrayBuffer());
      entry.saved = await api.saveAttachment(sessionId, cwd, entry.file.name, base64(bytes));
      entry.savedSessionId = sessionId;
    }
    ids.push(entry.saved.id);
  }
  return {
    ids,
    consume: () => {
      for (const entry of batch) {
        const index = pending.indexOf(entry);
        if (index >= 0) pending.splice(index, 1);
        if (entry.preview) URL.revokeObjectURL(entry.preview);
      }
      paint();
    },
  };
}

export function initAttachments(): void {
  let depth = 0;
  composer.addEventListener("dragenter", (event) => {
    if (!event.dataTransfer?.types.includes("Files")) return;
    event.preventDefault();
    depth++;
    composer.classList.add("attachment-dragging");
  });
  composer.addEventListener("dragover", (event) => {
    if (event.dataTransfer?.types.includes("Files")) event.preventDefault();
  });
  composer.addEventListener("dragleave", () => {
    depth = Math.max(0, depth - 1);
    if (!depth) composer.classList.remove("attachment-dragging");
  });
  composer.addEventListener("drop", (event) => {
    event.preventDefault();
    depth = 0;
    composer.classList.remove("attachment-dragging");
    if (event.dataTransfer?.files) addFiles(event.dataTransfer.files);
  });
}
