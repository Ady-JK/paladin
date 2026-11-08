import type { SnapshotProtection } from "../core/types";

// Details are built as text nodes because paths can contain arbitrary text.
export function paintProtection(row: HTMLElement, protection: SnapshotProtection): void {
  let details = row.querySelector<HTMLDetailsElement>(".snapshot-protection");
  if (protection.state !== "unavailable") {
    details?.remove();
    return;
  }
  if (!details) {
    details = document.createElement("details");
    details.className = "snapshot-protection";
    row.append(details);
  }
  details.dataset.state = protection.state;
  const summary = document.createElement("summary");
  summary.textContent = protection.reason
    ? `Undo snapshot unavailable: ${protection.reason}`
    : "Undo snapshot unavailable";
  const body = document.createElement("div");
  body.className = "snapshot-protection-details";
  const noFiles = document.createElement("p");
  noFiles.textContent = "File changes from this turn cannot be restored using its undo snapshot.";
  body.append(noFiles);
  if (protection.excluded?.length) {
    const description = document.createElement("p");
    description.textContent = "These files or folders are not included in the snapshot:";
    body.append(description);
    const list = document.createElement("ul");
    for (const path of protection.excluded) {
      const item = document.createElement("li");
      item.textContent = path;
      list.append(item);
    }
    body.append(list);
  }
  if (protection.truncated) {
    const more = document.createElement("p");
    more.textContent = "More excluded paths are not shown.";
    body.append(more);
  }
  const note = document.createElement("p");
  note.textContent = "Undo covers the last three turns. New or ignored files may not be captured.";
  body.append(note);
  details.replaceChildren(summary, body);
}
