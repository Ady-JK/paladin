// Chromium forgets a cookie that has no expiry (a "session cookie") when the
// app quits, so every site that logs you in that way asked again on the next
// start. A browser's "continue where you left off" keeps them. This does the
// same for the personal browser's profile, and only for it: the agent's
// per-chat browsers are in memory and stay throwaway.
import type { Cookie, CookiesSetDetails, Session } from "electron";

// KEEP_DAYS bounds how long a login cookie we re-persisted lives. Sites still
// enforce their own server-side expiry; this only stops a quit from wiping it.
export const KEEP_DAYS = 30;

// persistentCookie is the write that gives a session cookie an expiry, or
// null when the cookie needs nothing (it already persists, or it is gone).
export function persistentCookie(cookie: Cookie, nowSeconds: number): CookiesSetDetails | null {
  if (!cookie.session) return null;
  const host = (cookie.domain ?? "").replace(/^\./, "");
  if (!host) return null;
  const path = cookie.path || "/";
  return {
    url: `${cookie.secure ? "https" : "http"}://${host}${path}`,
    name: cookie.name,
    value: cookie.value,
    // A host-only cookie must stay host-only: passing a domain would widen it
    // to every subdomain.
    ...(cookie.hostOnly ? {} : { domain: cookie.domain }),
    path,
    secure: cookie.secure,
    httpOnly: cookie.httpOnly,
    sameSite: cookie.sameSite,
    expirationDate: nowSeconds + KEEP_DAYS * 86400,
  };
}

const watched = new WeakSet<Session>();

// keepLogins re-sets every session cookie the profile receives with an expiry.
// The re-set is a non-session cookie, so it does not trigger itself again. A
// site that deletes a cookie (sign-out) is left alone: only additions count.
export function keepLogins(jar: Session): void {
  if (watched.has(jar)) return;
  watched.add(jar);
  jar.cookies.on("changed", (_event, cookie, _cause, removed) => {
    if (removed) return;
    const details = persistentCookie(cookie, Date.now() / 1000);
    if (details) void jar.cookies.set(details).catch(() => undefined);
  });
}
