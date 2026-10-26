// Model picker: debounced search, visibility-gated trees, 300-row cap.
import { api } from "./api";
import { escapeAttr, escapeHtml, splitModel } from "./format";
import { state } from "./state";
import { toast } from "./ui";

function labsOf(provider: string): Map<string, string[]> {
  const p = state.providers.find((x) => x.name === provider);
  const map = new Map<string, string[]>();
  for (const m of p?.models ?? []) {
    const { lab } = splitModel(m);
    if (!map.has(lab)) map.set(lab, []);
    map.get(lab)!.push(m);
  }
  return map;
}

function treeHTML(filter: string): string {
  const q = filter.trim().toLowerCase();
  const CAP = 300;
  let shown = 0;
  let total = 0;
  let out = "";
  for (const p of state.providers) {
    const labs = [...labsOf(p.name).entries()].sort((a, b) => a[0].localeCompare(b[0]));
    let pHtml = "";
    for (const [lab, models] of labs) {
      const entries = models.filter((m) => !q || m.toLowerCase().includes(q));
      if (entries.length === 0) continue;
      total += entries.length;
      pHtml += `<div class="day">${escapeHtml(p.name)} / ${escapeHtml(lab)}</div>`;
      for (const m of entries.sort()) {
        if (shown >= CAP) break;
        shown++;
        const sel = m === state.selectedModel && p.name === state.selectedProvider;
        pHtml += `<div class="pick-row"><button data-mpick="${escapeAttr(m)}" data-prov="${escapeAttr(p.name)}"
          style="display:block;width:100%;text-align:left;background:${sel ? "#16213a" : "none"};border:0;color:var(--text);padding:5px 8px;border-radius:6px;cursor:pointer;font:inherit;font-size:12px">${escapeHtml(m)}</button></div>`;
      }
    }
    out += pHtml;
  }
  if (!out) return `<p style="color:var(--muted);font-size:12px">no match</p>`;
  if (shown < total) out += `<p style="color:var(--muted);font-size:11px">…and ${total - shown} more — refine the search</p>`;
  return out;
}

function renderTrees(): void {
  if (!document.querySelector("#modelModal")!.classList.contains("on")) {
    // Cheap: skip hidden tree.
  } else {
    document.querySelector("#modelTree")!.innerHTML = treeHTML(
      (document.querySelector("#modelSearch") as HTMLInputElement).value,
    );
  }
  if ((document.querySelector("#sessionModal") as HTMLElement).classList.contains("on")) {
    document.querySelector("#newSessionTree")!.innerHTML = treeHTML(
      (document.querySelector("#newSessionSearch") as HTMLInputElement).value,
    );
  }
  const label = state.selectedModel ? `${state.selectedProvider} / ${state.selectedModel}` : "model";
  (document.querySelector("#modelBtn") as HTMLElement).textContent = label;
}

function pick(provider: string, model: string): void {
  state.selectedProvider = provider;
  state.selectedModel = model;
  renderTrees();
}

export async function loadProviders(): Promise<void> {
  try {
    state.providers = await api.listProviders();
  } catch (e) {
    toast(String(e), "error");
  }
  if (!state.selectedProvider && state.providers.length > 0) {
    const d = state.providers.find((p) => p.default) ?? state.providers[0];
    state.selectedProvider = d.name;
    state.selectedModel = d.model || d.models[0] || "";
  }
  renderTrees();
  state.onProvidersChanged();
}

export function initModels(): void {
  let timer = 0;
  const deb = (): void => {
    window.clearTimeout(timer);
    timer = window.setTimeout(renderTrees, 150);
  };
  (document.querySelector("#modelSearch") as HTMLInputElement).addEventListener("input", deb);
  (document.querySelector("#newSessionSearch") as HTMLInputElement).addEventListener("input", deb);
  document.querySelector("#modelBtn")!.addEventListener("click", () => {
    document.querySelector("#modelModal")!.classList.add("on");
    renderTrees();
  });
  document.querySelector("#modelClose")!.addEventListener("click", () =>
    document.querySelector("#modelModal")!.classList.remove("on"),
  );
  document.addEventListener("click", (e) => {
    const b = (e.target as HTMLElement).closest<HTMLElement>("[data-mpick]");
    if (b?.dataset.mpick && b.dataset.prov) pick(b.dataset.prov, b.dataset.mpick);
  });
  document.querySelector("#newSessionCreate")!.addEventListener("click", async () => {
    const path = (document.querySelector("#newSessionPath") as HTMLInputElement).value.trim() || state.cwd;
    try {
      const sess = await api.openSession("", path);
      state.sessionId = sess.sessionId;
      state.cwd = sess.cwd;
      (document.querySelector("#workspaceLabel") as HTMLElement).textContent = state.cwd;
      document.querySelector("#sessionModal")!.classList.remove("on");
      (document.querySelector("#messages") as HTMLElement).innerHTML = "";
      const { refreshSessions } = await import("./sessions");
      await refreshSessions();
    } catch (e) {
      toast(String(e), "error");
    }
  });
}
