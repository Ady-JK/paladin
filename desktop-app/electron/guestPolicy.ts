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

// Guest views load credential-free https pages only; the same check runs
// on every navigation and redirect, not just the first load.
export function allowedGuestUrl(value: unknown): value is string {
  return typeof value === "string" && allowedPersonalPopup(value) && value !== "about:blank";
}

// The renderer names views "web:<key>" and puts them on a persist:paladin-*
// partition. Anything else (the default session, an empty name, "agent")
// must not reach session.fromPartition or the view table.
export function validGuestPartition(value: unknown): value is string {
  return typeof value === "string" && /^persist:paladin-[a-z0-9_-]{1,64}$/.test(value);
}

export function validViewName(value: unknown): value is string {
  return typeof value === "string" && /^web:[\w.-]{1,96}$/.test(value);
}

// Session ids become part of the agent browser's in-memory partition name.
export function validSessionId(value: unknown): value is string {
  return typeof value === "string" && /^[\w.-]{1,128}$/.test(value);
}
