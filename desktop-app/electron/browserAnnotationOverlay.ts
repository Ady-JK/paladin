// This function runs in the inspected localhost page, inside a shadow
// root so the page's styles do not reshape Paladin's annotation controls.
export type BrowserAnnotation = {
  id: string;
  url: string;
  selector: string;
  tag: string;
  label: string;
  text: string;
  comment: string;
  color: number;
};

export function renderBrowserAnnotationOverlay(input: { notes: BrowserAnnotation[]; binding: string }): void {
  const global = window as unknown as Record<string, unknown>;
  const previousCleanup = global.__paladinAnnotationCleanup;
  if (typeof previousCleanup === "function") previousCleanup();
  document.querySelector("[data-paladin-annotation-root]")?.remove();

  const current = input.notes.filter((note) => note.url === location.href);
  if (!current.length) return;
  const host = document.createElement("div");
  host.dataset.paladinAnnotationRoot = "";
  host.style.cssText = "position:fixed;inset:0;z-index:2147483647;pointer-events:none";
  const shadow = host.attachShadow({ mode: "open" });
  const styles = document.createElement("style");
  styles.textContent = `
    * { box-sizing: border-box; }
    .mark { position: fixed; pointer-events: none; border: 2px solid var(--mark); border-radius: 6px;
      box-shadow: 0 0 0 2px color-mix(in srgb, var(--mark) 16%, transparent); }
    .badge { position: absolute; top: -13px; right: -13px; display: grid; place-items: center;
      width: 25px; height: 25px; border: 1px solid #fff; border-radius: 8px;
      background: var(--mark); color: #fff; box-shadow: 0 2px 8px #0004;
      pointer-events: auto; cursor: pointer; }
    .badge svg { width: 15px; height: 15px; fill: none; stroke: currentColor; stroke-width: 1.8;
      stroke-linecap: round; stroke-linejoin: round; }
    .badge:focus-visible, .card button:focus-visible, .card textarea:focus-visible { outline: 2px solid #fff; outline-offset: 2px; }
    .card { position: fixed; width: min(300px, calc(100vw - 24px)); padding: 10px;
      border: 1px solid #44434c; border-radius: 13px; background: #202024; color: #f6f6f7;
      box-shadow: 0 12px 40px #0007; font: 12px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      pointer-events: auto; }
    .head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
    .dot { flex: none; width: 8px; height: 8px; border-radius: 50%; background: var(--mark); }
    .name { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
    .card textarea { display: block; width: 100%; min-height: 82px; padding: 8px; resize: vertical;
      border: 1px solid #51515a; border-radius: 8px; background: #2b2b31; color: inherit; font: inherit; }
    .actions { display: flex; align-items: center; justify-content: flex-end; gap: 5px; margin-top: 8px; }
    .card button { border: 0; border-radius: 7px; background: transparent; color: #ceced3;
      padding: 5px 8px; font: inherit; cursor: pointer; }
    .card button:hover { background: #3a3a42; color: #fff; }
    .card .save { background: var(--mark); color: #fff; font-weight: 600; }
    .card .save:disabled { opacity: .45; cursor: not-allowed; }
    .error { min-height: 0; color: #ffc2c2; font-size: 11px; }
  `;
  shadow.append(styles);
  document.documentElement.append(host);

  const colors = ["#8c78d8", "#198f91", "#b77718", "#ca6179", "#5a82ca"];
  const invoke = async (action: "save" | "remove", id: string, comment = ""): Promise<void> => {
    const binding = global[input.binding];
    if (typeof binding !== "function") throw new Error("Comment connection is unavailable");
    await (binding as (message: { action: string; id: string; comment: string }) => Promise<unknown>)({
      action,
      id,
      comment,
    });
  };
  let card: HTMLElement | null = null;
  const marks: Array<{ note: BrowserAnnotation; mark: HTMLElement; element: Element | null }> = [];
  const closeCard = (): void => {
    card?.remove();
    card = null;
  };

  for (const note of current) {
    const mark = document.createElement("div");
    mark.className = "mark";
    mark.style.setProperty("--mark", colors[note.color % colors.length]);
    const badge = document.createElement("button");
    badge.className = "badge";
    badge.type = "button";
    badge.dataset.noteId = note.id;
    badge.title = note.comment ? "Edit comment" : "Add comment";
    badge.setAttribute("aria-label", `${note.comment ? "Edit" : "Add"} comment on ${note.label || note.tag}`);
    badge.innerHTML =
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h16v11H8l-4 3V5Z"/><path d="M8 9h8m-8 3h5"/></svg>';
    mark.append(badge);
    shadow.append(mark);
    marks.push({ note, mark, element: null });
    badge.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      closeCard();
      const editor = document.createElement("div");
      editor.className = "card";
      editor.style.setProperty("--mark", colors[note.color % colors.length]);
      const head = document.createElement("div");
      head.className = "head";
      const dot = document.createElement("span");
      dot.className = "dot";
      const name = document.createElement("span");
      name.className = "name";
      name.textContent = `<${note.tag}> ${note.label || note.text || note.selector}`;
      name.title = note.selector;
      head.append(dot, name);
      const area = document.createElement("textarea");
      area.setAttribute("aria-label", `Comment on ${note.label || note.tag}`);
      area.placeholder = "What should change?";
      area.value = note.comment;
      const error = document.createElement("div");
      error.className = "error";
      const actions = document.createElement("div");
      actions.className = "actions";
      const remove = document.createElement("button");
      remove.type = "button";
      remove.textContent = "Remove";
      const cancel = document.createElement("button");
      cancel.type = "button";
      cancel.textContent = "Close";
      const save = document.createElement("button");
      save.type = "button";
      save.className = "save";
      save.textContent = "Save";
      save.disabled = !area.value.trim();
      actions.append(remove, cancel, save);
      editor.append(head, area, error, actions);
      shadow.append(editor);
      card = editor;
      const rect = mark.getBoundingClientRect();
      editor.style.left = `${Math.max(12, Math.min(rect.left, innerWidth - 312))}px`;
      editor.style.top = `${Math.max(12, Math.min(rect.bottom + 12, innerHeight - 180))}px`;
      area.addEventListener("input", () => {
        save.disabled = !area.value.trim();
      });
      area.addEventListener("keydown", (key) => {
        if (key.key === "Escape") {
          key.stopPropagation();
          closeCard();
        }
      });
      cancel.addEventListener("click", closeCard);
      save.addEventListener("click", () => {
        save.disabled = true;
        void invoke("save", note.id, area.value.trim()).then(closeCard, (cause: unknown) => {
          error.textContent = String(cause instanceof Error ? cause.message : cause);
          save.disabled = false;
        });
      });
      remove.addEventListener("click", () => {
        void invoke("remove", note.id).then(closeCard, (cause: unknown) => {
          error.textContent = String(cause instanceof Error ? cause.message : cause);
        });
      });
      area.focus();
    });
  }

  let frame = 0;
  const position = (): void => {
    frame = 0;
    for (const item of marks) {
      let element: Element | null = null;
      try {
        element = document.querySelector(item.note.selector);
      } catch {
        /* stale selector */
      }
      if (element?.tagName.toLowerCase() !== item.note.tag) element = null;
      item.element = element;
      const rect = element?.getBoundingClientRect();
      item.mark.style.display = rect && rect.width && rect.height ? "block" : "none";
      if (rect)
        Object.assign(item.mark.style, {
          left: `${rect.left}px`,
          top: `${rect.top}px`,
          width: `${rect.width}px`,
          height: `${rect.height}px`,
        });
    }
  };
  const schedule = (): void => {
    if (!frame) frame = requestAnimationFrame(position);
  };
  const observer = new MutationObserver(schedule);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener("scroll", schedule, true);
  window.addEventListener("resize", schedule);
  position();
  global.__paladinAnnotationCleanup = () => {
    observer.disconnect();
    window.removeEventListener("scroll", schedule, true);
    window.removeEventListener("resize", schedule);
    if (frame) cancelAnimationFrame(frame);
    closeCard();
    host.remove();
  };
}
