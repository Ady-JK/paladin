// Workspace picker: home + browse + pick.
import { api } from "./api";
import { escapeAttr, escapeHtml } from "./format";
import { state } from "./state";
import { toast } from "./ui";

export function initWorkspace(): void {
  document.querySelector("#workspaceBtn")!.addEventListener("click", async () => {
    try {
      const home = await api.fsHome();
      (document.querySelector("#newSessionPath") as HTMLInputElement).value = state.cwd || home;
      await browse((document.querySelector("#newSessionPath") as HTMLInputElement).value);
      document.querySelector("#sessionModal")!.classList.add("on");
    } catch (e) {
      toast(String(e), "error");
    }
  });
  document.querySelector("#newSessionBtn")!.addEventListener("click", async () => {
    (document.querySelector("#newSessionPath") as HTMLInputElement).value = state.cwd;
    await browse(state.cwd || (await api.fsHome().catch(() => "")));
    document.querySelector("#sessionModal")!.classList.add("on");
  });
  document.querySelector("#newSessionClose")!.addEventListener("click", () =>
    document.querySelector("#sessionModal")!.classList.remove("on"),
  );
  document.querySelector("#browseBtn")!.addEventListener("click", () =>
    browse((document.querySelector("#newSessionPath") as HTMLInputElement).value).catch((e) =>
      toast(String(e), "error"),
    ),
  );
}

async function browse(path: string): Promise<void> {
  const list = await api.fsList(path || null);
  (document.querySelector("#newSessionPath") as HTMLInputElement).value = list.currentPath;
  const box = document.querySelector("#browserList") as HTMLElement;
  let html = `<button class="file-row" data-browse="${escapeAttr(list.parentPath)}">.. parent</button>`;
  for (const d of list.directories.slice(0, 200)) {
    html += `<button class="file-row" data-browse="${escapeAttr(d.path)}">${escapeHtml(d.name)}</button>`;
  }
  box.innerHTML = html;
  box.querySelectorAll("[data-browse]").forEach((b) =>
    (b as HTMLElement).addEventListener("click", () => {
      void browse((b as HTMLElement).dataset.browse!);
    }),
  );
}
