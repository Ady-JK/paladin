
type Icon = {
  d: string;
  line?: boolean;
  thin?: boolean;
};

const ICONS: Record<string, Icon> = {
  bolt: { d: "M13 2 4.5 13.5H11l-1 8.5 8.5-11.5H12l1-8.5Z" },
  comments: { d: "M20 2H4a2 2 0 0 0-2 2v18l4-4h14a2 2 0 0 0 2-2V4a2 2 0 0 0-2-2Z" },
  code: { d: "m16 18 6-6-6-6M8 6l-6 6 6 6", line: true },
  folder: { d: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" },
  "folder-open": { d: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v1H6.5L3 17V7Z" },
  "folder-tree": {
    d: "M3 4h5v4H3V4Zm0 12h5v4H3v-4ZM8 6h4m-4 10h4m4-12h5v2h-5V4Zm0 12h5v2h-5v-2Z",
  },
  plus: { d: "M12 5v14M5 12h14", line: true },
  search: { d: "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm10 2-4.35-4.35", line: true },
  gear: {
    d: "M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Zm0 10.5 1.6 1.2a1 1 0 0 0 1.5-.3l1-1.7a1 1 0 0 1 1.2-.5l1.9.4a1 1 0 0 0 1.1-.8l.3-2a1 1 0 0 1 .8-1l1.9-.6a1 1 0 0 0 .7-1.3l-.7-1.8a1 1 0 0 1 0-1.2l.7-1.8a1 1 0 0 0-.7-1.3l-1.9-.6a1 1 0 0 1-.8-1l-.3-2a1 1 0 0 0-1.1-.8l-1.9.4a1 1 0 0 1-1.2-.5l-1-1.7a1 1 0 0 0-1.5-.3L12 2.5l-1.6-1.2a1 1 0 0 0-1.5.3l-1 1.7a1 1 0 0 1-1.2.5l-1.9-.4a1 1 0 0 0-1.1.8l-.3 2a1 1 0 0 1-.8 1l-1.9.6a1 1 0 0 0-.7 1.3l.7 1.8a1 1 0 0 1 0 1.2l-.7 1.8a1 1 0 0 0 .7 1.3l1.9.6a1 1 0 0 1 .8 1l.3 2a1 1 0 0 0 1.1.8l1.9-.4a1 1 0 0 1 1.2.5l1 1.7a1 1 0 0 0 1.5.3Z",
  },
  download: { d: "M12 3v12m0 0 4-4m-4 4-4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2", line: true },
  moon: { d: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" },
  sun: {
    d: "M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Zm0-13v2m0 16v-2M3 12h2m14 0h2M5.6 5.6 7 7m10 10 1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4",
    line: true,
  },
  microchip: {
    d: "M7 7h10v10H7V7Zm3 3h4v4h-4v-4ZM9 3v4m6-4v4M9 17v4m6-4v4M3 9h4m-4 6h4m12-6h4m-4 6h4",
    line: true,
  },
  "code-branch": {
    d: "M6 3a3 3 0 0 1 1 5.8V17a2 2 0 1 1-2 0V8.8A3 3 0 0 1 6 3Zm12 0a3 3 0 0 1 1 5.8V11a4 4 0 0 1-4 4h-3a3 3 0 0 0-3 3v1.2a3 3 0 1 1-2 0V18a5 5 0 0 1 5-5h3a2 2 0 0 0 2-2V8.8A3 3 0 0 1 18 3Z",
  },
  terminal: { d: "m4 17 6-6-6-6m8 12h8M2 3h20v18H2V3Z", line: true },
  "file-code": {
    d: "M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6Zm0 0v6h6M10 13l-2 2 2 2m4-4 2 2-2 2",
    line: true,
  },
  paperclip: {
    d: "M21.4 11.05 12.2 20.2a5.5 5.5 0 0 1-7.8-7.8l9.2-9.1a3.67 3.67 0 0 1 5.2 5.2l-9.2 9.1a1.83 1.83 0 0 1-2.6-2.6l8.5-8.4",
    line: true,
  },
  microphone: {
    d: "M12 15a3 3 0 0 0 3-3V6a3 3 0 1 0-6 0v6a3 3 0 0 0 3 3Zm7-3a7 7 0 0 1-14 0m7 7v3",
    line: true,
  },
  paperplane: { d: "M22 2 11 13m11-11L9 22l2-9 9-11Z" },
  pause: { d: "M8 5v14M16 5v14", line: true },
  "chevron-down": { d: "m6 9 6 6 6-6", line: true },
  "chevron-right": { d: "m9 6 6 6-6 6", line: true },
  "angles-left": { d: "m11 17-5-5 5-5m6 10 5-5-5-5", line: true },
  brain: {
    d: "M9.5 3A3.5 3.5 0 0 0 6 6.5v.3A3.5 3.5 0 0 0 5 13a3.5 3.5 0 0 0 4.5 4.3V21a2 2 0 0 0 4 0v-3.7a3.5 3.5 0 0 0 0-6.6V3.2A3.5 3.5 0 0 0 9.5 3Zm5 0A3.5 3.5 0 0 1 18 6.5v.3A3.5 3.5 0 0 1 19 13a3.5 3.5 0 0 1-4.5 4.3",
    line: true,
  },
  flask: {
    d: "M9 3h6M12 3v5.2L6.7 17.6A2 2 0 0 0 8.5 21h7a2 2 0 0 0 1.8-3.4L12 8.2M7.6 14h8.8",
    line: true,
  },
  globe: {
    d: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm-9-9h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18",
    line: true,
  },
  chart: {
    d: "M4 20V10m6 10V4m6 16v-7m4 7H2",
    line: true,
  },
  "dollar-sign": {
    d: "M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6",
    line: true,
  },
  zap: { d: "M13 2 3 14h9l-1 8 10-12h-9l1-8Z" },
  layers: {
    d: "m12 2 10 5-10 5L2 7l10-5ZM2 12l10 5 10-5M2 17l10 5 10-5",
    line: true,
  },
  sliders: { d: "M4 21v-7m0-4V3m8 18v-9m0-4V3m8 18v-5m0-4V3M1 14h6M9 8h6m2 8h6", line: true },
  xmark: { d: "M6 6l12 12M18 6 6 18", line: true },
  play: { d: "m6 3 14 9-14 9V3Z" },
  "code-compare": { d: "m16 18 6-6-6-6M8 6 2 12l6 6m8-6H4", line: true },
  "magnifying-glass-code": {
    d: "m21 21-4.35-4.35M17 11a6 6 0 1 1-12 0 6 6 0 0 1 12 0Zm-8 2 2 2 4-4",
    line: true,
  },
  repeat: { d: "m17 2 4 4-4 4M3 11V9a4 4 0 0 1 4-4h14M7 22l-4-4 4-4m14-3v2a4 4 0 0 1-4 4H3", line: true },
  robot: {
    d: "M12 2v3M7 10a5 5 0 0 1 10 0v6a5 5 0 0 1-10 0v-6ZM4 13H2v4h2m16-4h2v4h-2M9 13h.01M15 13h.01",
    line: true,
  },
  bug: {
    d: "M8 6a4 4 0 0 1 8 0M6 10h12v4a6 6 0 0 1-12 0v-4Zm0 0H3m18 0h-3M4.5 6 7 8m12.5-2L17 8M4 16l3-2m14 2-3-2M6 20l2-3m10 3-2-3",
    line: true,
  },
  check: { d: "m5 13 4 4L19 7", line: true },
  eye: { d: "M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7Zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z", line: true },
  circle: { d: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z", line: true },
  user: {
    d: "M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2m8-10a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z",
    line: true,
  },
  "window-minimize": { d: "M5 12h14", line: true },
  "window-maximize": { d: "M5 5h14v14H5V5Z", line: true },
  "window-restore": { d: "M8 8h11v11H8V8ZM5 16V5h11", line: true },
  "window-close": { d: "M6 6l12 12M18 6 6 18", line: true },
  trash: {
    d: "M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2m-8 0 1 12a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1l1-12M10 11v6M14 11v6",
    line: true,
  },
  external: {
    d: "M14 4h6v6m0-6L10 14M9 5H6a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-3",
    line: true,
  },
  "arrow-up": { d: "M12 19V5m0 0-6 6m6-6 6 6", line: true },
  // Thin outline glyphs for the tool-call rows.
  "tl-term": { d: "M4 4h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Zm3 5 3 3-3 3m5 0h5", line: true, thin: true },
  "tl-file": { d: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Zm0 0v5h5M9 13h6m-6 4h4", line: true, thin: true },
  "tl-file-plus": { d: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Zm0 0v5h5m-7 4v6m-3-3h6", line: true, thin: true },
  "tl-pencil": { d: "M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16v4Zm9.5-13.5 4 4", line: true, thin: true },
  "tl-list": { d: "M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01", line: true, thin: true },
  "tl-ask": { d: "M21 12a8.5 8.5 0 0 1-12.4 7.6L3 21l1.4-5.1A8.5 8.5 0 1 1 21 12ZM9.8 9.5a2.3 2.3 0 0 1 4.4.8c0 1.5-2.2 2-2.2 3.2m0 2.6h.01", line: true, thin: true },
  "tl-tools": { d: "M7 3v7m0 4v7m-3-11h6M17 3v4m0 4v10m-3-14h6M7 14a2 2 0 1 0 0-4 2 2 0 0 0 0 4Zm10-3a2 2 0 1 0 0-4", line: true, thin: true },
  // Thin outline glyphs for the rail and sidebar.
  "rl-agent": { d: "M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Zm2.5 5.5L10 12l-2.5 2.5M12.5 15h4", line: true, thin: true },
  "rl-chat": { d: "M7.5 18.5 4 21v-4.2A8 8 0 1 1 7.5 18.5ZM8.5 10.5h7m-7 3.5h4.5", line: true, thin: true },
  "rl-gear": { d: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm7.4-3a7.4 7.4 0 0 0-.1-1.3l2-1.6-2-3.4-2.4 1a7.4 7.4 0 0 0-2.2-1.3L14.4 3h-4l-.4 2.4A7.4 7.4 0 0 0 7.8 6.7l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 0 0 0 2.6l-2 1.6 2 3.4 2.4-1a7.4 7.4 0 0 0 2.2 1.3l.4 2.4h4l.4-2.4a7.4 7.4 0 0 0 2.2-1.3l2.4 1 2-3.4-2-1.6c.1-.4.1-.9.1-1.3Z", line: true, thin: true },
  "rl-sidebar": { d: "M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Zm4 0v16", line: true, thin: true },
  "rl-compose": { d: "M11 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-5M18.4 2.6a2 2 0 0 1 2.9 2.9L12 14.8 8 16l1.2-4 9.2-9.4Z", line: true, thin: true },
  "rl-folder": { d: "M3.5 7.5A2 2 0 0 1 5.5 5.5h3.6l2 2h7.4a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2v-9.5Z", line: true, thin: true },
  "rl-pulse": { d: "M3 12h4l2.5-6 5 12 2.5-6h4", line: true, thin: true },
  "rl-chevron": { d: "m9 6 6 6-6 6", line: true, thin: true },
  // Thin outline glyphs for the slash palette.
  "sl-compact": { d: "M4 9h16M4 15h16M9 4l3 3 3-3M9 20l3-3 3 3", line: true, thin: true },
  "tl-undo": { d: "M9 14 4 9l5-5M4 9h10.5a5.5 5.5 0 0 1 0 11H11", line: true, thin: true },
  "sl-merge": { d: "M6 3v18M6 9a6 6 0 0 0 6 6h3M18 15a2 2 0 1 0 0 .01M6 3a2 2 0 1 0 0 .01", line: true, thin: true },
  "sl-fork": { d: "M6 3v6a3 3 0 0 0 3 3h6a3 3 0 0 1 3 3v1M6 3a2 2 0 1 0 0 .01M18 21a2 2 0 1 0 0-4 2 2 0 0 0 0 4ZM6 9v12", line: true, thin: true },
  "sl-plan": { d: "M9 4h6a1 1 0 0 1 1 1v1H8V5a1 1 0 0 1 1-1ZM8 5H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M8 12l2 2 4-4M8 17h8", line: true, thin: true },
  "sl-build": { d: "M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.6 2.6-2.4-.6-.6-2.4 2.6-2.6Z", line: true, thin: true },
  "sl-memory": { d: "M9.5 3a3.5 3.5 0 0 0-3.4 4.3A3.5 3.5 0 0 0 5 14a3.5 3.5 0 0 0 4.5 6.4V3Zm5 0a3.5 3.5 0 0 1 3.4 4.3A3.5 3.5 0 0 1 19 14a3.5 3.5 0 0 1-4.5 6.4V3ZM9.5 9H8m6.5 0H16m-6.5 5H8m6.5 0H16", line: true, thin: true },
  "sl-review": { d: "M11 19a8 8 0 1 1 5.3-2L21 21.7M8 11l2 2 4-4", line: true, thin: true },
  "sl-model": { d: "M12 3 4 7.5v9L12 21l8-4.5v-9L12 3Zm0 0v18M4 7.5l8 4.5 8-4.5", line: true, thin: true },
  "sl-usage": { d: "M4 20V10m6 10V4m6 16v-7m4 7H2", line: true, thin: true },
  "sl-clear": { d: "M4 7h16M10 11v6m4-6v6M6 7l1 13h10l1-13M9 7V4h6v3", line: true, thin: true },
  "sl-help": { d: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM9.6 9.3a2.5 2.5 0 0 1 4.8.9c0 1.6-2.4 2.2-2.4 3.6m0 3h.01", line: true, thin: true },
  "sl-skill": { d: "M12 3l2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.4 6.8 19.1l1-5.8-4.3-4.1 5.9-.9L12 3Z", line: true, thin: true },
  "sl-prompt": { d: "M5 5h14a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1Zm3 5 2 2-2 2m4 0h4", line: true, thin: true },
  "sl-new": { d: "M12 5v14M5 12h14", line: true, thin: true },
  "sl-sliders": { d: "M4 21v-7m0-4V3m8 18v-9m0-4V3m8 18v-5m0-4V3M1 14h6M9 8h6m2 8h6", line: true, thin: true },
  "sl-more": { d: "M5 12h.01M12 12h.01M19 12h.01", line: true },
  "sl-shield": { d: "M12 3 5 6v5c0 4.4 3 8.3 7 9.5 4-1.2 7-5.1 7-9.5V6l-7-3Z", line: true, thin: true },
  "sl-shield-check": { d: "M12 3 5 6v5c0 4.4 3 8.3 7 9.5 4-1.2 7-5.1 7-9.5V6l-7-3Zm-3 9 2 2 4-4", line: true, thin: true },
  "sl-shield-off": { d: "M12 3 5 6v5c0 4.4 3 8.3 7 9.5 4-1.2 7-5.1 7-9.5V6l-7-3ZM4 4l16 16", line: true, thin: true },
  "sl-eye": { d: "M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12Zm10-3a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z", line: true, thin: true },
  "sl-init": { d: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Zm0 0v5h5M12 11v6m-3-3h6", line: true, thin: true },
};

export type IconName = keyof typeof ICONS;

export function icon(name: IconName, className = "h-3 w-3"): string {
  const entry = ICONS[name];
  if (!entry) {
    return "";
  }

  if (entry.line) {
    return (
      `<svg class="${className}" viewBox="0 0 24 24" fill="none" stroke="currentColor" ` +
      `stroke-width="${entry.thin ? 1.6 : 2}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` +
      `<path d="${entry.d}"/></svg>`
    );
  }

  return (
    `<svg class="${className}" viewBox="0 0 24 24" fill="currentColor" ` +
    `aria-hidden="true"><path d="${entry.d}"/></svg>`
  );
}
