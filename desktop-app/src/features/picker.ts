// picker.ts owns the shared native-like file browser used by the
// new-session folder step and the composer attach dialog. Both pickers
// render the same Finder/Explorer shape: clickable breadcrumbs on top,
// a search box plus a "show hidden" checkbox, a single-select list, and
// explicit Select/Cancel buttons at the bottom. The checkbox defaults
// off and persists across opens via localStorage.

import { escapeHtml } from "../core/format";
import type { FsList } from "../core/types";

export type PickerKind = "file" | "folder";

export type PickerSelection = {
  path: string;
  kind: PickerKind;
};

const SHOW_HIDDEN_KEY = "paladin.showHidden";

// getShowHidden reads the persisted hidden-file toggle. Dotfiles and
// dotfolders stay hidden unless the user opted in; anything unparseable
// falls back to the default off.
export function getShowHidden(): boolean {
  try {
    return window.localStorage.getItem(SHOW_HIDDEN_KEY) === "1";
  } catch {
    return false;
  }
}

// setShowHidden persists the hidden-file toggle for both pickers.
export function setShowHidden(show: boolean): void {
  try {
    window.localStorage.setItem(SHOW_HIDDEN_KEY, show ? "1" : "0");
  } catch {
    // Private mode: the current open still respects the choice.
  }
}

export type CrumbSegment = {
  label: string;
  path: string;
};

// splitCrumbs breaks an absolute path into clickable breadcrumb
// segments. "/" yields a single root segment; every segment carries the
// absolute prefix it navigates to so clicks never rebuild paths by hand.
export function splitCrumbs(currentPath: string): CrumbSegment[] {
  const raw = (currentPath || "").replace(/\\/g, "/");
  if (!raw || raw === "/") {
    return [{ label: "/", path: "/" }];
  }
  const parts = raw.split("/").filter((part) => part.length > 0);
  // Windows drive roots ("C:") keep their trailing slash for display.
  const rooted = raw.startsWith("/");
  const segments: CrumbSegment[] = [];
  let prefix = rooted ? "" : "";
  parts.forEach((part, index) => {
    prefix += `${rooted && index === 0 ? "/" : index === 0 ? "" : "/"}${part}`;
    segments.push({ label: index === 0 && /^[A-Za-z]:$/.test(part) ? `${part}/` : part, path: prefix || "/" });
  });
  if (rooted) {
    segments.unshift({ label: "/", path: "/" });
  }
  return segments;
}

// isHiddenName reports dotfiles and dotfolders (".git", ".env").
export function isHiddenName(name: string): boolean {
  return name.startsWith(".") && name !== "." && name !== "..";
}

// filterEntries applies the hidden toggle plus a case-insensitive
// substring search over the current directory only. Folders sort before
// files, each alphabetically, so the list feels stable while typing.
export function filterEntries(
  list: FsList,
  showHidden: boolean,
  query: string,
): Pick<FsList, "directories" | "files"> {
  const needle = query.trim().toLowerCase();
  const keep = (name: string): boolean => {
    if (!showHidden && isHiddenName(name)) {
      return false;
    }
    if (!needle) {
      return true;
    }
    return name.toLowerCase().includes(needle);
  };
  const directories = list.directories
    .filter((dir) => keep(dir.name))
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name));
  const files = list.files
    .filter((file) => keep(file.name))
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name));
  return { directories, files };
}

// crumbsHTML renders the breadcrumb trail. Every segment except the
// current directory is a button; the leaf is an aria-current label.
export function crumbsHTML(currentPath: string): string {
  const segments = splitCrumbs(currentPath);
  return segments
    .map((segment, index) => {
      const last = index === segments.length - 1;
      const sep = index > 0 ? `<span class="picker-crumb-sep" aria-hidden="true">/</span>` : "";
      if (last) {
        return `${sep}<span class="picker-crumb-current" aria-current="page" title="${escapeHtml(segment.path)}">${escapeHtml(segment.label)}</span>`;
      }
      return `${sep}<button type="button" data-fb-crumb="${escapeHtml(segment.path)}" class="picker-crumb" title="${escapeHtml(segment.path)}">${escapeHtml(segment.label)}</button>`;
    })
    .join("");
}

// rowsHTML renders the selectable list: a parent row, folder rows that
// select on click and open on double-click (or via the chevron), then
// file rows. The selected row gets aria-selected plus a check mark.
export function rowsHTML(
  list: FsList,
  selection: PickerSelection | null,
  options?: { filesEnabled?: boolean },
): string {
  const filesEnabled = options?.filesEnabled ?? true;
  const rows: string[] = [];

  if (list.parentPath && list.parentPath !== list.currentPath) {
    rows.push(`
      <button
        type="button"
        data-fb-open="${escapeHtml(list.parentPath)}"
        class="picker-row"
        title="Go to the parent folder"
      >
        <span data-icon="angles-left" class="picker-row-icon"></span>
        <span class="picker-row-label">..</span>
      </button>`);
  }

  for (const dir of list.directories) {
    const selected = selection?.path === dir.path && selection.kind === "folder";
    rows.push(`
      <button
        type="button"
        data-fb-select="${escapeHtml(dir.path)}"
        data-fb-kind="folder"
        data-fb-open-path="${escapeHtml(dir.path)}"
        class="picker-row"
        ${selected ? `aria-selected="true"` : ""}
        title="${escapeHtml(dir.path)}"
      >
        <span data-icon="folder" class="picker-row-icon picker-row-folder"></span>
        <span class="picker-row-label">${escapeHtml(dir.name)}</span>
        <span class="picker-row-check" data-icon="check" aria-hidden="true"></span>
        <span class="picker-row-open" data-fb-open="${escapeHtml(dir.path)}" title="Open ${escapeHtml(dir.name)}" role="button" tabindex="-1" aria-label="Open ${escapeHtml(dir.name)}" data-icon="chevron-right"></span>
      </button>`);
  }

  if (filesEnabled) {
    for (const file of list.files) {
      const selected = selection?.path === file.path && selection.kind === "file";
      rows.push(`
        <button
          type="button"
          data-fb-select="${escapeHtml(file.path)}"
          data-fb-kind="file"
          class="picker-row"
          ${selected ? `aria-selected="true"` : ""}
          title="${escapeHtml(file.path)}"
        >
          <span data-icon="file-code" class="picker-row-icon picker-row-file"></span>
          <span class="picker-row-label">${escapeHtml(file.name)}</span>
          <span class="picker-row-check" data-icon="check" aria-hidden="true"></span>
        </button>`);
    }
  }

  if (rows.length === 0 || (list.directories.length === 0 && (!filesEnabled || list.files.length === 0))) {
    rows.push(`<p class="picker-empty">This folder is empty</p>`);
  }

  return rows.join("");
}
