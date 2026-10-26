// Settings: provider list + add/update.
import { api } from "./api";
import { escapeHtml } from "./format";
import { loadProviders } from "./models";
import { state } from "./state";
import { toast } from "./ui";

export function initSettings(): void {
  document.querySelector("#settingsBtn")!.addEventListener("click", () => {
    render();
    document.querySelector("#settingsModal")!.classList.add("on");
  });
  document.querySelector("#settingsClose")!.addEventListener("click", () =>
    document.querySelector("#settingsModal")!.classList.remove("on"),
  );
  document.querySelector("#provAdd")!.addEventListener("click", async () => {
    const p = {
      name: (document.querySelector("#provName") as HTMLInputElement).value.trim(),
      apiUrl: (document.querySelector("#provUrl") as HTMLInputElement).value.trim(),
      apiKey: (document.querySelector("#provKey") as HTMLInputElement).value,
      apiType: (document.querySelector("#provType") as HTMLSelectElement).value,
    };
    if (!p.name || !p.apiUrl) {
      toast("name and api url are required", "error");
      return;
    }
    try {
      const exists = state.providers.some((x) => x.name === p.name);
      if (exists) await api.updateProvider(p);
      else await api.addProvider(p);
      await loadProviders();
      render();
    } catch (e) {
      toast(String(e), "error");
    }
  });
  state.onProvidersChanged = render;
}

function render(): void {
  const box = document.querySelector("#providerList") as HTMLElement;
  box.innerHTML = state.providers
    .map(
      (p) => `<div class="row" style="justify-content:space-between;padding:6px 0;border-bottom:1px solid var(--line)">
        <div><div style="font-weight:600">${escapeHtml(p.name)}${p.default ? " (default)" : ""}</div>
        <div style="font-size:11px;color:var(--muted)">${escapeHtml(p.apiUrl)} · ${p.models.length} models · ${p.hasKey ? "key saved" : "no key"}</div></div>
        <button class="btn" data-mkdefault="${escapeHtml(p.name)}">Default</button></div>`,
    )
    .join("");
  box.querySelectorAll("[data-mkdefault]").forEach((b) =>
    (b as HTMLElement).addEventListener("click", async () => {
      try {
        await api.setDefaultProvider((b as HTMLElement).dataset.mkdefault!);
        await loadProviders();
      } catch (e) {
        toast(String(e), "error");
      }
    }),
  );
}
