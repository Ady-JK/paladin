// Personal browsing supports common OAuth flows, including a blank popup
// which the site subsequently navigates. Never allow local files or URL credentials.
export function allowedPersonalPopup(url: string): boolean {
  if (url === "about:blank") return true;
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" && !parsed.username && !parsed.password;
  } catch {
    return false;
  }
}

// GUEST_PERMISSIONS are all a chat-site pane may use. Electron grants
// every permission when no handler says otherwise, which would let any
// page in a site tab read the clipboard (copied API keys), use the
// microphone or camera, open other apps by URL scheme or probe local
// services without a prompt.
const GUEST_PERMISSIONS = new Set(["clipboard-sanitized-write", "fullscreen"]);

export function guestPermissionAllowed(permission: string): boolean {
  return GUEST_PERMISSIONS.has(permission);
}
