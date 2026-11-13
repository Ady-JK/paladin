import { escapeAttr, escapeHtml } from "../../core/format";
import { state } from "../../core/state";
import { liveActivity } from "../live";
import { openSession } from "../sessions";
import { hasAgentBrowser } from "../sidePane";

export function initIDEActivity(): void {
  const button = document.querySelector<HTMLButtonElement>("#ideActivityToggle")!;
  const list = document.createElement("div");
  list.id = "ideActivityList";
  list.className = "ide-activity-list";
  list.hidden = true;
  list.setAttribute("aria-label", "Session activity");
  button.parentElement!.append(list);
  let lastRows = "";
  const paint = (): void => {
    const rows = state.sessions
      .filter((session) => !session.subagent && (session.cwd === state.cwd || session.worktree?.origin === state.cwd))
      .map((session) => ({
        session,
        status: liveActivity(session.sessionId),
        browser: hasAgentBrowser(session.sessionId),
      }))
      .filter((item) => item.status || item.browser);
    button.querySelector(".rail-activity-label")!.textContent = rows.length ? `Activity ${rows.length}` : "Activity";
    button.setAttribute("aria-label", rows.length ? `Activity ${rows.length}` : "Activity");
    const html = rows.length
      ? rows
          .map(
            ({ session, status, browser }) =>
              `<button type="button" data-activity-session="${escapeAttr(session.sessionId)}"><strong>${escapeHtml(session.title || "Untitled chat")}</strong><small>${escapeHtml([status, browser ? "Browser open" : ""].filter(Boolean).join(" · "))}</small></button>`,
          )
          .join("")
      : `<p>No active sessions in this workspace.</p>`;
    if (html !== lastRows) {
      list.innerHTML = html;
      lastRows = html;
    }
  };
  const close = (): void => {
    list.hidden = true;
    button.setAttribute("aria-expanded", "false");
  };
  button.addEventListener("click", () => {
    window.dispatchEvent(new Event("paladin:ide-hide-explorer"));
    list.hidden = !list.hidden;
    button.setAttribute("aria-expanded", String(!list.hidden));
    if (!list.hidden) paint();
  });
  list.addEventListener("click", (event) => {
    const id = (event.target as HTMLElement).closest<HTMLElement>("[data-activity-session]")?.dataset.activitySession;
    if (!id) return;
    close();
    window.dispatchEvent(new Event("paladin:ide-show-agent"));
    void openSession(id);
  });
  document.addEventListener("click", (event) => {
    if (!list.hidden && !list.contains(event.target as Node) && !button.contains(event.target as Node)) close();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !list.hidden) {
      close();
      button.focus();
    }
  });
  window.addEventListener("paladin:sessions-changed", paint);
  window.addEventListener("paladin:workspace-changed", paint);
  paint();
}
