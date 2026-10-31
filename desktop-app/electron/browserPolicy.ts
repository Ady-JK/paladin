// A browser session is for local UI testing only. Apply this both to top-level
// navigation and to subresource/WebSocket requests so a local page cannot
// quietly load remote dependencies through the embedded browser.
export function localBrowserUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    return (
      u.protocol === "http:" && !u.username && !u.password && (u.hostname === "localhost" || u.hostname === "127.0.0.1")
    );
  } catch {
    return false;
  }
}

export function localBrowserRequest(raw: string): boolean {
  if (raw === "about:blank" || raw.startsWith("data:") || raw.startsWith("blob:")) return true;
  try {
    const u = new URL(raw);
    return (
      (u.protocol === "http:" || u.protocol === "ws:") &&
      !u.username &&
      !u.password &&
      (u.hostname === "localhost" || u.hostname === "127.0.0.1")
    );
  } catch {
    return false;
  }
}
