import * as api from "../../core/api";

type OpenFile = { cwd: string; path: string; version: string; content: string; saving: boolean };
type FileStatus = { path: string; state: "same" | "changed" | "unavailable"; version?: string };

// The core checks all open files with cached metadata. Only the active file's
// changed content is read for comparison; stale replies never replace a draft.
export function monitorDiskChanges(
  current: () => OpenFile | undefined,
  files: () => OpenFile[],
  onStatuses: (statuses: FileStatus[]) => void,
  reload: () => void,
): void {
  const notice = document.createElement("div");
  notice.className = "ide-disk-notice";
  notice.hidden = true;
  notice.innerHTML = `<span>This file changed on disk.</span><button type="button" data-compare>Compare changes</button><button type="button" data-reload>Reload</button>`;
  const compare = document.createElement("section");
  compare.id = "ideDiskCompare";
  compare.className = "ide-disk-compare";
  compare.hidden = true;
  compare.innerHTML = `<div class="ide-disk-compare-head"><strong>File comparison</strong><button type="button" aria-label="Close comparison">×</button></div><div class="ide-disk-columns"><div><strong>Your editor</strong><pre></pre></div><div><strong>On disk</strong><pre></pre></div></div>`;
  document.querySelector("#ideEditorContent")!.before(notice);
  document.querySelector("#ideEditorContent")!.after(compare);
  const message = notice.querySelector("span")!;
  const compareButton = notice.querySelector<HTMLButtonElement>("[data-compare]")!;
  let disk: { file: OpenFile; content: string; version: string } | undefined;
  let serial = 0;
  let busy = false;
  let pending = false;
  const clear = (): void => {
    disk = undefined;
    notice.hidden = true;
    compare.hidden = true;
  };
  const check = async (): Promise<void> => {
    const opened = files().filter((item) => !item.saving);
    if (!opened.length || document.hidden) {
      if (!current()) clear();
      return;
    }
    if (busy) {
      pending = true;
      return;
    }
    busy = true;
    const request = ++serial;
    const file = current();
    const checks = opened.map((item) => ({ path: item.path, version: item.version }));
    try {
      const statuses: FileStatus[] = [];
      for (let i = 0; i < checks.length; i += 128) {
        statuses.push(...(await api.ideFileStatuses(opened[0].cwd, checks.slice(i, i + 128))));
      }
      if (request !== serial) return;
      const stillOpen = new Set(files());
      const byPath = new Map(opened.map((item, index) => [item.path, { file: item, version: checks[index].version }]));
      const fresh = statuses.filter((status) => {
        const item = byPath.get(status.path);
        return !!item && item.file.version === item.version && stillOpen.has(item.file);
      });
      onStatuses(fresh);
      if (!file || current() !== file || file.saving) {
        clear();
        return;
      }
      const status = fresh.find((item) => item.path === file.path);
      if (!status || status.state === "same") {
        clear();
        return;
      }
      if (status.state === "unavailable") {
        disk = undefined;
        message.textContent = "This file is unavailable on disk.";
        compareButton.hidden = true;
        compare.hidden = true;
        notice.hidden = false;
        return;
      }
      if (disk?.file !== file || disk.version !== status.version) {
        const result = await api.ideReadFile(file.cwd, file.path);
        if (
          request !== serial ||
          current() !== file ||
          file.saving ||
          file.version !== checks.find((item) => item.path === file.path)?.version
        )
          return;
        disk = { file, content: result.content, version: result.version };
        if (!compare.hidden) compare.querySelectorAll("pre")[1].textContent = result.content;
      }
      message.textContent = "This file changed on disk.";
      compareButton.hidden = false;
      notice.hidden = false;
    } catch {
      // Save still performs its own full version check if monitoring fails.
    } finally {
      busy = false;
      if (pending) {
        pending = false;
        void check();
      }
    }
  };
  compareButton.addEventListener("click", () => {
    if (!disk || current() !== disk.file) return;
    const columns = compare.querySelectorAll("pre");
    columns[0].textContent = disk.file.content;
    columns[1].textContent = disk.content;
    compare.hidden = false;
  });
  notice.querySelector("[data-reload]")!.addEventListener("click", reload);
  compare.querySelector("button")!.addEventListener("click", () => (compare.hidden = true));
  window.addEventListener("focus", () => void check());
  window.addEventListener("paladin:ide-disk-check", () => void check());
  window.addEventListener("paladin:ide-buffer-changed", () => {
    const snapshot = disk;
    if (!compare.hidden && snapshot && snapshot.file === current())
      compare.querySelectorAll("pre")[0].textContent = snapshot.file.content;
  });
  window.setInterval(() => void check(), 1000);
  window.addEventListener("paladin:ide-file-changed", () => {
    serial++;
    clear();
    void check();
  });
}
