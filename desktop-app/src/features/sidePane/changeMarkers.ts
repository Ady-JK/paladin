import * as api from "../../core/api";
import { $ } from "../../core/dom";
import { escapeHtml } from "../../core/format";
import type { ChangedFile, SessionChanges } from "../../core/types";
import { icon } from "../../icons";

type ChangeTotal = { added: number; removed: number; files?: Record<string, { added: number; removed: number }> };

function countPatch(patch: string): ChangeTotal {
  let added = 0;
  let removed = 0;
  for (const line of patch.split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) added++;
    else if (line.startsWith("-") && !line.startsWith("---")) removed++;
  }
  return { added, removed };
}

type CachedTotal = { signature: string; pending: Promise<ChangeTotal | null> };
const changeTotals = new Map<string, Map<number, CachedTotal>>();

export function clearChangeTotals(sessionIds: string[]): void {
  for (const sessionId of sessionIds) changeTotals.delete(sessionId);
}

export function retainChangeTotals(sessionId: string, turns: SessionChanges["turns"]): void {
  const session = changeTotals.get(sessionId);
  if (!session) return;
  const retained = new Set(turns.map((turn) => turn.turn));
  for (const turn of session.keys()) if (!retained.has(turn)) session.delete(turn);
  if (session.size === 0) changeTotals.delete(sessionId);
}

export async function turnTotal(sessionId: string, turn: SessionChanges["turns"][number]): Promise<ChangeTotal | null> {
  const signature = JSON.stringify([turn.files, turn.callIds]);
  let session = changeTotals.get(sessionId);
  if (!session) {
    session = new Map();
    changeTotals.set(sessionId, session);
  }
  const cached = session.get(turn.turn);
  if (cached?.signature === signature) return cached.pending;
  const entries = session;
  const pending = (async (): Promise<ChangeTotal | null> => {
    try {
      const patches = await Promise.all(
        turn.files.map((file) => api.sessionDiff(sessionId, null, turn.turn, file.path)),
      );
      if (patches.some((patch) => patch.truncated)) return null;
      const total: ChangeTotal = { added: 0, removed: 0, files: {} };
      for (const patch of patches) {
        const count = countPatch(patch.patch);
        total.added += count.added;
        total.removed += count.removed;
        total.files![patch.path] = count;
      }
      return total;
    } catch {
      return null;
    }
  })();
  const entry = { signature, pending };
  entries.set(turn.turn, entry);
  void pending.then((total) => {
    if (!total && entries.get(turn.turn) === entry) entries.delete(turn.turn);
  });
  return pending;
}

// paintedMarker remembers what a card currently shows. A refresh that
// changed nothing then writes nothing: rebuilding the whole card on every
// settling tool restarted layout and re-created one node per file, which
// is what made the collapsed card flicker while a turn ran.
const paintedMarker = new WeakMap<HTMLElement, { heading: string; files: string }>();

// turnAnchor is the last row of the turn that starts at row. It stops at
// the next turn's user row, and at a live root (which holds another
// turn's rows), so a turn's card can never end up under a later turn.
function turnAnchor(row: HTMLElement): HTMLElement {
  let anchor = row;
  let next = anchor.nextElementSibling as HTMLElement | null;
  while (
    next &&
    !next.hasAttribute("data-turn-index") &&
    !next.hasAttribute("data-turn-id") &&
    !next.classList.contains("live-root") &&
    !next.classList.contains("turn-change-card")
  ) {
    anchor = next;
    next = anchor.nextElementSibling as HTMLElement | null;
  }
  return anchor;
}

export function paintTurnMarker(
  row: HTMLElement,
  turn: number,
  files: ChangedFile[],
  total?: ChangeTotal | null,
): void {
  let button = row.parentElement?.querySelector<HTMLElement>(`.turn-changes[data-change-turn="${turn}"]`);
  if (!files.length) {
    button?.remove();
    return;
  }
  const fresh = !button;
  if (!button) {
    button = document.createElement("div");
    button.className = "turn-changes turn-change-card turn-change-collapsed";
    button.innerHTML = `<span class="turn-change-heading"></span><span class="turn-change-files"></span>`;
    // One delegated listener, so a repaint of the heading cannot leak the
    // undo button's handler or lose it.
    button.addEventListener("click", (event) => {
      if (!(event.target as HTMLElement).closest(".turn-change-undo")) return;
      event.stopPropagation();
      row.querySelector<HTMLElement>("[data-revert]")?.click();
    });
  }
  // Keep the card at the end of its own turn: after the turn's user row
  // and its agent rows, before the next turn's user row. The walk stops
  // at the next user row and never leaves the turn's container, because
  // drifting past either one put the card under a turn it does not
  // belong to (and, from the live root, at the bottom of the
  // transcript), where every repaint could move it again.
  const anchor = turnAnchor(row);
  if (anchor !== button && anchor.nextElementSibling !== button) anchor.after(button);
  button.dataset.changeTurn = String(turn);
  button.setAttribute("role", "button");
  button.tabIndex = 0;
  button.setAttribute("aria-expanded", String(!button.classList.contains("turn-change-collapsed")));

  const heading =
    `${icon("rl-chevron", "h-3.5 w-3.5 turn-change-caret")}${icon("tl-file", "h-4 w-4")}` +
    `<span>Edited ${files.length} ${files.length === 1 ? "file" : "files"}</span>` +
    (total ? `<span class="tool-add">+${total.added}</span><span class="tool-del">-${total.removed}</span>` : "") +
    `<button type="button" class="turn-change-undo">Undo ${icon("tl-undo", "h-3.5 w-3.5")}</button>` +
    `<button type="button" class="turn-change-view">View changes</button>`;
  const list = files
    .map((file) => {
      const count = total?.files?.[file.path];
      return `<span class="turn-change-file"><span>${escapeHtml(file.path)}</span><span>${count ? `<b class="tool-add">+${count.added}</b> <b class="tool-del">-${count.removed}</b>` : escapeHtml(file.status)}</span></span>`;
    })
    .join("");

  const shown = paintedMarker.get(button);
  if (fresh || !shown) {
    button.querySelector<HTMLElement>(".turn-change-heading")!.innerHTML = heading;
    button.querySelector<HTMLElement>(".turn-change-files")!.innerHTML = list;
  } else {
    // Only the part that changed is rewritten: a second settling tool
    // that touched nothing visible leaves the card untouched.
    if (shown.heading !== heading) button.querySelector<HTMLElement>(".turn-change-heading")!.innerHTML = heading;
    if (shown.files !== list) button.querySelector<HTMLElement>(".turn-change-files")!.innerHTML = list;
  }
  paintedMarker.set(button, { heading, files: list });
}

// runningTurn is the change turn a live row belongs to. While a turn
// runs its tool calls are not in the transcript yet, so callIds cannot
// match; the newest change turn is the running one as long as its index
// is past every persisted user row. That also avoids attaching the
// previous turn's card to a turn that has not changed anything yet.
export function runningTurn(turns: SessionChanges["turns"]): SessionChanges["turns"][number] | null {
  const last = turns[turns.length - 1];
  if (!last) return null;
  let persisted = -1;
  for (const row of Array.from($("#messages").querySelectorAll<HTMLElement>("[data-turn-index]"))) {
    persisted = Math.max(persisted, Number(row.dataset.turnIndex));
  }
  return last.turn > persisted ? last : null;
}
