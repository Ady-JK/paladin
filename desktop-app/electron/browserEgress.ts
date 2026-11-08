// browserEgress.ts is the session browser's only way out. Each pane's
// session sends every request through its own local proxy (see
// BrowserManager.ensure), which resolves the name on this machine,
// checks every address it gets and connects only to an address it
// checked. So a public-looking name cannot lead a page to this machine
// or a private network: not from the start, not after a redirect and
// not after its DNS changes, since each new connection is resolved and
// checked again. browserPolicy still decides which page may load what by
// URL; this decides where a connection may actually go. The proxy always
// connects directly and ignores the system proxy settings.

import { lookup as dnsLookup, type LookupAddress } from "node:dns";
import { Agent, createServer, request, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { BlockList, connect, isIP, type AddressInfo, type LookupFunction, type Socket } from "node:net";
import type { Duplex } from "node:stream";

export type Resolved = { address: string; family: 4 | 6 };
type Lookup = (host: string) => Promise<Resolved[]>;
type Decision = { ok: true; addresses: Resolved[] } | { ok: false; reason: string };
type Refusal = { host: string; reason: string };

type EgressOptions = {
  // allowPrivate is the user's "Private and internal networks" switch.
  allowPrivate: () => boolean;
  // lookup resolves a name; tests and the smoke replace it.
  lookup?: Lookup;
  // trusted names skip the address check: the smoke's fixture sites,
  // which stand in for public sites on the loopback server.
  trusted?: ReadonlySet<string>;
  // The agent browser must not reach Chromium's debugging endpoint.
  protectedPort?: () => number;
};

const LOOKUP_TIMEOUT_MS = 10_000;
const CONNECT_TIMEOUT_MS = 15_000;
const MAX_REFUSALS = 20;

const loopback = new BlockList();
loopback.addSubnet("127.0.0.0", 8, "ipv4");
loopback.addAddress("::1", "ipv6");

// reserved is never reachable: unspecified, link-local (cloud metadata
// lives at 169.254.169.254), multicast and the old class E range. A
// BlockList matches IPv4-mapped IPv6 addresses (::ffff:7f00:1) against
// the IPv4 rules, in either spelling.
const reserved = new BlockList();
reserved.addSubnet("0.0.0.0", 8, "ipv4");
reserved.addSubnet("169.254.0.0", 16, "ipv4");
reserved.addSubnet("224.0.0.0", 3, "ipv4");
reserved.addAddress("::", "ipv6");
reserved.addSubnet("fe80::", 10, "ipv6");
reserved.addSubnet("ff00::", 8, "ipv6");

const privateNet = new BlockList();
privateNet.addSubnet("10.0.0.0", 8, "ipv4");
privateNet.addSubnet("172.16.0.0", 12, "ipv4");
privateNet.addSubnet("192.168.0.0", 16, "ipv4");
privateNet.addSubnet("100.64.0.0", 10, "ipv4");
privateNet.addSubnet("fc00::", 7, "ipv6");

// localName is a name for this machine given on purpose.
function localName(host: string): boolean {
  const h = bare(host);
  return h === "localhost" || h === "127.0.0.1" || h === "::1";
}

function bare(host: string): string {
  return host
    .toLowerCase()
    .replace(/^\[|\]$/g, "")
    .replace(/\.$/, "");
}

// addressProblem says why host may not be reached at address, or "".
// It matches the core's web_fetch check (tools/webfetch.go blockedIP).
export function addressProblem(host: string, address: string, allowPrivate: boolean): string {
  const ip = bare(address);
  const family = isIP(ip);
  if (!family) return "not an IP address";
  const type = family === 4 ? "ipv4" : "ipv6";
  if (reserved.check(ip, type)) return "a link-local or reserved address";
  if (loopback.check(ip, type)) {
    return localName(host) || allowPrivate ? "" : "this machine (only localhost and 127.0.0.1 may name it)";
  }
  if (privateNet.check(ip, type)) {
    return allowPrivate
      ? ""
      : "a private network address (turn on Private and internal networks in Settings → Tools to reach it)";
  }
  return "";
}

function systemLookup(host: string): Promise<Resolved[]> {
  return new Promise((resolve, reject) => {
    dnsLookup(host, { all: true, verbatim: true }, (error, addresses: LookupAddress[]) => {
      if (error) reject(error);
      else resolve(addresses.map((a) => ({ address: a.address, family: a.family === 6 ? 6 : 4 })));
    });
  });
}

// pinned is a lookup that answers with the addresses already checked,
// so the connection cannot resolve the name a second time.
function pinned(addresses: Resolved[]): LookupFunction {
  return (_hostname, options, callback) => {
    if (options.all) callback(null, addresses);
    else callback(null, addresses[0].address, addresses[0].family);
  };
}

// HOP_BY_HOP headers belong to one connection and are not forwarded.
const HOP_BY_HOP = new Set([
  "connection",
  "proxy-connection",
  "keep-alive",
  "proxy-authorization",
  "proxy-authenticate",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

function forwardable(raw: string[]): Record<string, string | string[]> {
  const named = new Set<string>();
  for (let i = 0; i < raw.length; i += 2) {
    if (raw[i].toLowerCase() === "connection") {
      for (const token of raw[i + 1].split(",")) named.add(token.trim().toLowerCase());
    }
  }
  const out: Record<string, string | string[]> = Object.create(null) as Record<string, string | string[]>;
  for (let i = 0; i < raw.length; i += 2) {
    const key = raw[i];
    const lower = key.toLowerCase();
    if (HOP_BY_HOP.has(lower) || named.has(lower)) continue;
    const prev = out[key];
    out[key] = prev === undefined ? raw[i + 1] : [...(Array.isArray(prev) ? prev : [prev]), raw[i + 1]];
  }
  return out;
}

// connectTarget parses CONNECT's host:port.
function connectTarget(raw: string): { host: string; port: number } | null {
  const m = /^(\[[0-9a-fA-F:.]+\]|[^\s:/[\]]+):(\d{1,5})$/.exec(raw);
  if (!m) return null;
  const port = Number(m[2]);
  if (port < 1 || port > 65535) return null;
  return { host: bare(m[1]), port };
}

// EgressGuard is one session browser's proxy.
export class EgressGuard {
  private server: Server | null = null;
  private listening: Promise<number> | null = null;
  private readonly sockets = new Set<Socket | Duplex>();
  private readonly agent = new Agent({ keepAlive: true, maxSockets: 16 });
  private refusals: Refusal[] = [];
  private readonly lookup: Lookup;

  constructor(private readonly opts: EgressOptions) {
    this.lookup = opts.lookup ?? systemLookup;
  }

  // listen starts the proxy on a free loopback port.
  listen(): Promise<number> {
    if (this.listening) return this.listening;
    const server = createServer((req, res) => void this.forward(req, res));
    server.on("connect", (req: IncomingMessage, client: Duplex, head: Buffer) => void this.tunnel(req, client, head));
    server.on("connection", (socket: Socket) => this.track(socket));
    // Upgrades arrive only as CONNECT through a proxy; anything else is
    // not a proxy request.
    server.on("upgrade", (_req, socket: Duplex) => socket.destroy());
    this.server = server;
    this.listening = new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", () => resolve((server.address() as AddressInfo).port));
    });
    return this.listening;
  }

  async close(): Promise<void> {
    const server = this.server;
    this.server = null;
    this.listening = null;
    for (const socket of this.sockets) socket.destroy();
    this.sockets.clear();
    this.agent.destroy();
    if (server) await new Promise<void>((resolve) => server.close(() => resolve()));
  }

  // takeRefusals returns (and forgets) the connections refused since the
  // last call, so the agent hears why part of a page did not load.
  takeRefusals(): Refusal[] {
    const out = this.refusals;
    this.refusals = [];
    return out;
  }

  // check resolves host and decides whether it may be reached.
  async check(host: string): Promise<Decision> {
    const name = bare(host);
    let addresses: Resolved[];
    if (isIP(name)) {
      addresses = [{ address: name, family: isIP(name) === 6 ? 6 : 4 }];
    } else {
      try {
        addresses = await withTimeout(this.lookup(name), LOOKUP_TIMEOUT_MS);
      } catch (error) {
        return { ok: false, reason: `cannot resolve ${name} on this machine (${errorText(error)})` };
      }
    }
    if (!addresses.length) return { ok: false, reason: `cannot resolve ${name} on this machine` };
    if (this.opts.trusted?.has(name)) return { ok: true, addresses };
    const allowPrivate = this.opts.allowPrivate();
    for (const a of addresses) {
      const problem = addressProblem(name, a.address, allowPrivate);
      if (problem) return { ok: false, reason: `${name} resolves to ${a.address}, ${problem}` };
    }
    return { ok: true, addresses };
  }

  private track(socket: Socket | Duplex): void {
    this.sockets.add(socket);
    socket.on("close", () => this.sockets.delete(socket));
    socket.on("error", () => undefined);
  }

  private refuse(host: string, reason: string): void {
    this.refusals.push({ host: bare(host), reason });
    if (this.refusals.length > MAX_REFUSALS) this.refusals.shift();
  }

  private protectedTarget(port: number, addresses: Resolved[]): boolean {
    return (
      port === this.opts.protectedPort?.() &&
      addresses.some((address) => loopback.check(address.address, address.family === 6 ? "ipv6" : "ipv4"))
    );
  }

  private async forward(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const reply = (status: number, text: string) => {
      if (res.headersSent) {
        res.destroy();
        return;
      }
      res.writeHead(status, { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" });
      res.end(`Paladin's browser did not load this: ${text}\n`);
    };
    let url: URL;
    try {
      url = new URL(req.url ?? "");
    } catch {
      reply(400, "only proxy requests for absolute http:// URLs are forwarded");
      return;
    }
    if (url.protocol !== "http:" || url.username || url.password) {
      reply(400, "only proxy requests for absolute http:// URLs are forwarded");
      return;
    }
    const decision = await this.check(url.hostname);
    if (!decision.ok) {
      this.refuse(url.hostname, decision.reason);
      reply(403, decision.reason);
      return;
    }
    if (this.protectedTarget(Number(url.port || 80), decision.addresses)) {
      const reason = "Paladin's debugging endpoint is not available to browser pages";
      this.refuse(url.hostname, reason);
      reply(403, reason);
      return;
    }
    const upstream = request({
      host: bare(url.hostname),
      port: Number(url.port || 80),
      method: req.method,
      path: `${url.pathname}${url.search}`,
      headers: forwardable(req.rawHeaders),
      lookup: pinned(decision.addresses),
      agent: this.agent,
      timeout: CONNECT_TIMEOUT_MS,
    });
    upstream.on("response", (answer) => {
      upstream.setTimeout(0);
      res.writeHead(answer.statusCode ?? 502, answer.statusMessage, forwardable(answer.rawHeaders));
      answer.pipe(res);
    });
    upstream.on("timeout", () => upstream.destroy(new Error("timed out")));
    upstream.on("error", (error) => reply(502, errorText(error)));
    res.on("close", () => {
      if (!res.writableFinished) upstream.destroy();
    });
    req.pipe(upstream);
  }

  private async tunnel(req: IncomingMessage, client: Duplex, head: Buffer): Promise<void> {
    this.track(client);
    const fail = (status: string) => {
      if (!client.destroyed) client.end(`HTTP/1.1 ${status}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n`);
    };
    const target = connectTarget(req.url ?? "");
    if (!target) {
      fail("400 Bad Request");
      return;
    }
    const decision = await this.check(target.host);
    if (!decision.ok) {
      this.refuse(target.host, decision.reason);
      fail("403 Forbidden");
      return;
    }
    if (this.protectedTarget(target.port, decision.addresses)) {
      this.refuse(target.host, "Paladin's debugging endpoint is not available to browser pages");
      fail("403 Forbidden");
      return;
    }
    if (client.destroyed) return;
    const upstream = connect({
      host: target.host,
      port: target.port,
      lookup: pinned(decision.addresses),
      autoSelectFamily: true,
      timeout: CONNECT_TIMEOUT_MS,
    });
    this.track(upstream);
    let open = false;
    upstream.once("connect", () => {
      open = true;
      upstream.setTimeout(0);
      client.write("HTTP/1.1 200 Connection Established\r\n\r\n");
      if (head.length) upstream.write(head);
      upstream.pipe(client);
      client.pipe(upstream);
    });
    upstream.on("timeout", () => upstream.destroy(new Error("timed out")));
    upstream.on("error", () => {
      if (!open) fail("502 Bad Gateway");
    });
    upstream.on("close", () => client.destroy());
    client.on("close", () => upstream.destroy());
  }
}

// refuseAll starts a proxy that refuses every request and returns its
// port. The app window's session points at it (main.ts): the window
// itself never needs the network, so nothing running in it, a visual
// included, can reach it.
export async function refuseAll(): Promise<number> {
  const server = createServer((_req, res) => {
    res.writeHead(403, { "Content-Length": "0", Connection: "close" });
    res.end();
  });
  server.on("connect", (_req: IncomingMessage, socket: Duplex) =>
    socket.end("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"),
  );
  server.on("upgrade", (_req: IncomingMessage, socket: Duplex) => socket.destroy());
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  server.unref();
  return (server.address() as AddressInfo).port;
}

function withTimeout<T>(work: Promise<T>, ms: number): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  return Promise.race([
    work,
    new Promise<T>((_, reject) => {
      timer = setTimeout(() => reject(new Error("timed out")), ms);
    }),
  ]).finally(() => clearTimeout(timer));
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
