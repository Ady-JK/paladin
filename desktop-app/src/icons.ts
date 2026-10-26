// Minimal inline SVG icons (no emoji, no deps).
const paths: Record<string, string> = {
  robot: '<circle cx="12" cy="12" r="9"/><circle cx="9" cy="10" r="1" fill="currentColor"/><circle cx="15" cy="10" r="1" fill="currentColor"/><path d="M8 15h8"/>',
  user: '<circle cx="12" cy="8" r="4"/><path d="M4 21c0-4 4-6 8-6s8 2 8 6"/>',
  send: '<path d="M4 12l16-7-7 16-2-7z"/>',
  stop: '<rect x="7" y="7" width="10" height="10" rx="1"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  trash: '<path d="M4 7h16M9 7V5h6v2m-8 0l1 13h8l1-13"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3m0 14v3M2 12h3m14 0h3M5 5l2 2m10 10l2 2M19 5l-2 2M7 17l-2 2"/>',
  folder: '<path d="M3 6h6l2 2h10v12H3z"/>',
  check: '<path d="M4 12l5 5L20 6"/>',
  x: '<path d="M6 6l12 12M18 6L6 18"/>',
  brain: '<circle cx="12" cy="12" r="8"/><path d="M12 8v8M8 12h8"/>',
  refresh: '<path d="M20 12a8 8 0 1 1-2-5.5M20 4v5h-5"/>',
  chev: '<path d="M9 6l6 6-6 6"/>',
};

export function icon(name: string, cls = "ic"): string {
  const body = paths[name] ?? paths.robot;
  return `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;
}

export function paintIcons(root: ParentNode = document): void {
  root.querySelectorAll<HTMLElement>("[data-icon]").forEach((holder) => {
    if (holder.dataset.painted) return;
    const name = holder.dataset.icon || "robot";
    holder.innerHTML = icon(name, "ic");
    holder.dataset.painted = "1";
  });
}
