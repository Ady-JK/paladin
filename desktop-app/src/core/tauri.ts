// tauri.ts holds the Tauri environment probe.
// It supports the desktop app frontend.
export function hasTauri(): boolean {
  return "__TAURI_INTERNALS__" in window;
}
