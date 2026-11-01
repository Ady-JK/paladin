// Only the embedded chat providers' own HTTPS pages may open a guest
// sign-in window in their persistent cookie partition.
export function allowedChatPopup(url: string): boolean {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "https:") return false;
    return ["chatgpt.com", "openai.com", "claude.ai", "anthropic.com"].some(
      (domain) => parsed.hostname === domain || parsed.hostname.endsWith(`.${domain}`),
    );
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
