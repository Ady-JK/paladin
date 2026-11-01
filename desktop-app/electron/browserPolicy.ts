// The session browser opens local apps and, since the agent can browse,
// web sites. Which site may be opened is decided before a page loads:
// the core's permission gate asks the user for the agent's `open`, and a
// URL the user types in the pane is their own choice. These checks keep
// everything after that inside those sites:
//
// - top-level navigation stays on a site already opened in this session
//   (same host or a subdomain either way) or a local service on a port
//   already opened; other links are refused and reported to the agent;
// - a local page loads only local resources (no remote code in a local
//   app under test);
// - a web page may load public resources (CDNs, APIs) but never this
//   machine or a private network address, so a site cannot reach the
//   user's local services through the pane.

// localBrowserUrl is a local app's address.
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

// webBrowserUrl is any page the browser may open: http(s), no
// credentials in the URL.
export function webBrowserUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    return (u.protocol === "http:" || u.protocol === "https:") && !!u.hostname && !u.username && !u.password;
  } catch {
    return false;
  }
}

// localBrowserRequest is a request a local page may make.
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

// privateHost reports a name or address on this machine or a private
// network: localhost, loopback, RFC 1918, link-local, CGNAT, unique-local
// IPv6. Names that merely resolve there cannot be seen here; the core
// checks those before the agent opens a site.
export function privateHost(hostname: string): boolean {
  const h = hostname
    .toLowerCase()
    .replace(/^\[|\]$/g, "")
    .replace(/\.$/, "");
  if (h === "localhost" || h.endsWith(".localhost") || h === "0.0.0.0" || h === "::" || h === "::1") return true;
  const v4 = h.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    return (
      a === 10 ||
      a === 127 ||
      a === 0 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 169 && b === 254) ||
      (a === 100 && b >= 64 && b <= 127)
    );
  }
  if (h.includes(":")) {
    return /^f[cd]/.test(h) || /^fe[89ab]/.test(h) || h.startsWith("::ffff:");
  }
  return false;
}

// siteKey is what a session remembers having opened: a web site by host,
// a service on this machine by port (localhost:PORT, whichever name the
// URL used), matching the core's site rules.
export function siteKey(raw: string): string {
  try {
    const u = new URL(raw);
    const host = u.hostname.toLowerCase().replace(/^\[|\]$/g, "");
    if (host === "localhost" || host === "127.0.0.1" || host === "::1") {
      return `localhost:${u.port || (u.protocol === "https:" ? "443" : "80")}`;
    }
    return host;
  } catch {
    return "";
  }
}

// sameSite: equal hosts, or one a subdomain of the other.
function sameSite(a: string, b: string): boolean {
  const x = a.toLowerCase().replace(/\.$/, "");
  const y = b.toLowerCase().replace(/\.$/, "");
  return x === y || x.endsWith(`.${y}`) || y.endsWith(`.${x}`);
}

// navigationAllowed: may the page itself go to raw, given the sites
// opened in this session?
export function navigationAllowed(raw: string, sites: Iterable<string>): boolean {
  if (!webBrowserUrl(raw)) return false;
  const key = siteKey(raw);
  const local = key.startsWith("localhost:");
  for (const site of sites) {
    if (local ? site === key : !site.startsWith("localhost:") && sameSite(key, site)) return true;
  }
  return false;
}

// requestAllowed: may a page whose top-level address is topUrl load raw?
export function requestAllowed(raw: string, topUrl: string): boolean {
  if (!topUrl || localBrowserUrl(topUrl) || topUrl === "about:blank") return localBrowserRequest(raw);
  if (raw === "about:blank" || raw.startsWith("data:") || raw.startsWith("blob:")) return true;
  try {
    const u = new URL(raw);
    if (!["http:", "https:", "ws:", "wss:"].includes(u.protocol) || u.username || u.password) return false;
    if (!privateHost(u.hostname)) return true;
    // A site the user opened on a private address may load from itself.
    const top = new URL(topUrl).hostname;
    return privateHost(top) && sameSite(u.hostname, top) && !["localhost", "127.0.0.1", "::1"].includes(u.hostname);
  } catch {
    return false;
  }
}

// remoteOpenRefusal explains why a core running on another machine (SSH
// mode) may not open url in this computer's pane, or "". The pane runs
// here, so the remote agent's "localhost" would be this computer, and
// this computer's network is not the remote's to reach.
export function remoteOpenRefusal(raw: string): string {
  try {
    const host = new URL(raw).hostname;
    if (privateHost(host)) {
      return "the core runs on another machine; the browser pane runs on this computer and does not open its local or private addresses for a remote agent";
    }
  } catch {
    return "invalid URL";
  }
  return "";
}
