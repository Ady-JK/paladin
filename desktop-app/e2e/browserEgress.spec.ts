// browserEgress.spec.ts covers the browser pane's egress guard: the
// local proxy every pane request goes through, which resolves names on
// this machine and connects only to addresses it checked.
import { createServer, request, type Server } from "node:http";
import { connect, type AddressInfo } from "node:net";
import { expect, test } from "@playwright/test";
import { addressProblem, EgressGuard, refuseAll, type Resolved } from "../electron/browserEgress";

test("addresses: this machine only by name, private only when allowed, reserved never", () => {
  expect(addressProblem("docs.example.com", "93.184.216.34", false)).toBe("");
  expect(addressProblem("localhost", "127.0.0.1", false)).toBe("");
  expect(addressProblem("localhost", "::1", false)).toBe("");
  expect(addressProblem("127.0.0.1", "127.0.0.1", false)).toBe("");
  expect(addressProblem("rebind.example.com", "127.0.0.1", false)).toContain("this machine");
  expect(addressProblem("rebind.example.com", "127.5.6.7", false)).toContain("this machine");
  expect(addressProblem("rebind.example.com", "::ffff:127.0.0.1", false)).toContain("this machine");
  expect(addressProblem("rebind.example.com", "::1", false)).toContain("this machine");
  for (const ip of ["10.0.0.5", "172.16.3.4", "192.168.1.1", "100.64.0.9", "fd12::1"]) {
    expect(addressProblem("intranet.example.com", ip, false)).toContain("private network");
    expect(addressProblem("intranet.example.com", ip, true)).toBe("");
  }
  expect(addressProblem("rebind.example.com", "127.0.0.1", true)).toBe("");
  for (const ip of ["169.254.169.254", "0.0.0.0", "224.0.0.1", "255.255.255.255", "::", "fe80::1", "ff02::1"]) {
    expect(addressProblem("x.example.com", ip, true)).toContain("reserved");
  }
  // IPv4-mapped IPv6 counts as the IPv4 address, in either spelling.
  expect(addressProblem("x.example.com", "::ffff:7f00:1", false)).toContain("this machine");
  expect(addressProblem("x.example.com", "::ffff:a00:1", false)).toContain("private network");
  expect(addressProblem("x.example.com", "::ffff:a9fe:a9fe", true)).toContain("reserved");
  expect(addressProblem("x.example.com", "not-an-ip", true)).not.toBe("");
});

type Fixture = { origin: Server; originPort: number; hits: string[]; guard: EgressGuard; port: number };

async function fixture(
  answers: Record<string, string[] | (() => string[])>,
  opts: {
    allowPrivate?: () => boolean;
    trusted?: string[];
    protectedPort?: () => number;
    unusualHeader?: boolean;
  } = {},
): Promise<Fixture> {
  const hits: string[] = [];
  const origin = createServer((req, res) => {
    hits.push(`${req.headers.host} ${req.url}`);
    if (opts.unusualHeader) res.setHeader("constructor", "from-origin");
    res.writeHead(200, { "Content-Type": "text/plain", "X-Origin": "yes" });
    res.end(`origin saw ${req.headers.host}`);
  });
  await new Promise<void>((resolve) => origin.listen(0, "127.0.0.1", resolve));
  const lookup = async (host: string): Promise<Resolved[]> => {
    const a = answers[host];
    if (!a) throw new Error(`getaddrinfo ENOTFOUND ${host}`);
    return (typeof a === "function" ? a() : a).map((address) => ({
      address,
      family: address.includes(":") ? 6 : 4,
    }));
  };
  const guard = new EgressGuard({
    allowPrivate: opts.allowPrivate ?? (() => false),
    lookup,
    trusted: new Set(opts.trusted ?? []),
    protectedPort: opts.protectedPort,
  });
  const port = await guard.listen();
  return { origin, originPort: (origin.address() as AddressInfo).port, hits, guard, port };
}

test("the browser cannot connect to Paladin's local debugging port", async () => {
  let protectedPort = 0;
  const f = await fixture(
    { localhost: ["127.0.0.1"], "alias.test": ["127.0.0.1"] },
    {
      allowPrivate: () => true,
      protectedPort: () => protectedPort,
    },
  );
  try {
    protectedPort = f.originPort;
    expect((await get(f, `http://localhost:${f.originPort}/json/version`)).status).toBe(403);
    expect((await get(f, `http://alias.test:${f.originPort}/json/list`)).status).toBe(403);
    expect((await tunnel(f, `127.0.0.1:${f.originPort}`)).status).toBe("HTTP/1.1 403 Forbidden");
    expect(f.hits).toEqual([]);
    protectedPort = 0;
    expect((await get(f, `http://localhost:${f.originPort}/`)).status).toBe(200);
  } finally {
    await done(f);
  }
});

test("proxy handles HTTP headers that match object prototype properties", async () => {
  const f = await fixture({ localhost: ["127.0.0.1"] }, { unusualHeader: true });
  try {
    const response = await new Promise<{ status: number; value: string | undefined; header: unknown }>(
      (resolve, reject) => {
        const req = request(
          {
            host: "127.0.0.1",
            port: f.port,
            path: `http://localhost:${f.originPort}/`,
            headers: { constructor: "safe" },
          },
          (res) => {
            res.resume();
            res.on("end", () =>
              resolve({
                status: res.statusCode ?? 0,
                value: res.headers["x-origin"] as string | undefined,
                header: res.headers["constructor"],
              }),
            );
          },
        );
        req.on("error", reject);
        req.end();
      },
    );
    expect(response).toEqual({ status: 200, value: "yes", header: "from-origin" });
  } finally {
    await done(f);
  }
});

async function done(f: Fixture): Promise<void> {
  await f.guard.close();
  await new Promise<void>((resolve) => f.origin.close(() => resolve()));
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return `127.0.0.1`;
  }
}

// get sends an absolute-form request through the proxy, as Chromium does
// for http:// URLs.
function get(f: Fixture, url: string): Promise<{ status: number; body: string }> {
  return new Promise((resolve, reject) => {
    const req = request(
      {
        host: "127.0.0.1",
        port: f.port,
        method: "GET",
        path: url,
        headers: { Host: hostOf(url), Connection: "close" },
      },
      (res) => {
        let body = "";
        res.setEncoding("utf8");
        res.on("data", (chunk: string) => (body += chunk));
        res.on("end", () => resolve({ status: res.statusCode ?? 0, body }));
      },
    );
    req.on("error", reject);
    req.end();
  });
}

// tunnel sends CONNECT, as Chromium does for https:// and ws(s)://, then
// (when it opens) a plain request through it.
function tunnel(f: Fixture, target: string): Promise<{ status: string; body: string }> {
  return new Promise((resolve, reject) => {
    const socket = connect(f.port, "127.0.0.1");
    let data = "";
    let sent = false;
    socket.setEncoding("utf8");
    socket.on("connect", () => socket.write(`CONNECT ${target} HTTP/1.1\r\nHost: ${target}\r\n\r\n`));
    socket.on("data", (chunk: string) => {
      data += chunk;
      if (!sent && data.includes("\r\n\r\n")) {
        sent = true;
        if (data.startsWith("HTTP/1.1 200")) {
          socket.write(`GET /through HTTP/1.1\r\nHost: ${target}\r\nConnection: close\r\n\r\n`);
          return;
        }
        socket.end();
      }
    });
    socket.on("close", () => resolve({ status: data.split("\r\n")[0], body: data }));
    socket.on("error", reject);
  });
}

test("http through the guard: local names reach the machine, public-looking names that resolve here do not", async () => {
  const f = await fixture({
    localhost: ["127.0.0.1"],
    "rebind.example.com": ["127.0.0.1"],
    "lan.example.com": ["192.168.1.20"],
  });
  try {
    const ok = await get(f, `http://localhost:${f.originPort}/app.js`);
    expect(ok.status).toBe(200);
    expect(ok.body).toBe(`origin saw localhost:${f.originPort}`);
    const rebind = await get(f, `http://rebind.example.com:${f.originPort}/admin`);
    expect(rebind.status).toBe(403);
    expect(rebind.body).toContain("this machine");
    const lan = await get(f, `http://lan.example.com:${f.originPort}/router`);
    expect(lan.status).toBe(403);
    expect(lan.body).toContain("private network");
    expect(f.hits).toEqual([`localhost:${f.originPort} /app.js`]);
    expect(f.guard.takeRefusals().map((r) => r.host)).toEqual(["rebind.example.com", "lan.example.com"]);
    expect(f.guard.takeRefusals()).toEqual([]);
  } finally {
    await done(f);
  }
});

test("CONNECT through the guard is checked the same way, and IP literals too", async () => {
  const f = await fixture({ localhost: ["127.0.0.1"], "rebind.example.com": ["127.0.0.1"] });
  try {
    const ok = await tunnel(f, `localhost:${f.originPort}`);
    expect(ok.status).toBe("HTTP/1.1 200 Connection Established");
    expect(ok.body).toContain(`origin saw localhost:${f.originPort}`);
    expect((await tunnel(f, `127.0.0.1:${f.originPort}`)).status).toContain("200");
    expect((await tunnel(f, `[::1]:${f.originPort}`)).status).not.toContain("403");
    expect((await tunnel(f, `rebind.example.com:${f.originPort}`)).status).toBe("HTTP/1.1 403 Forbidden");
    expect((await tunnel(f, "10.0.0.1:443")).status).toBe("HTTP/1.1 403 Forbidden");
    expect((await tunnel(f, "169.254.169.254:80")).status).toBe("HTTP/1.1 403 Forbidden");
    expect((await tunnel(f, "nowhere.example.com:443")).status).toBe("HTTP/1.1 403 Forbidden");
    expect((await tunnel(f, "localhost:0")).status).toBe("HTTP/1.1 400 Bad Request");
    expect((await tunnel(f, "localhost")).status).toBe("HTTP/1.1 400 Bad Request");
    expect(f.hits.filter((h) => h.includes("rebind"))).toEqual([]);
  } finally {
    await done(f);
  }
});

test("every connection resolves again, and one bad address in an answer refuses the name", async () => {
  let calls = 0;
  const f = await fixture({
    // Public first, this machine on the next lookup (DNS rebinding).
    "flip.example.com": () => (++calls === 1 ? ["93.184.216.34"] : ["127.0.0.1"]),
    "mixed.example.com": ["93.184.216.34", "10.1.1.1"],
  });
  try {
    expect((await f.guard.check("flip.example.com")).ok).toBe(true);
    const second = await f.guard.check("flip.example.com");
    expect(second.ok).toBe(false);
    expect(!second.ok && second.reason).toContain("this machine");
    const mixed = await f.guard.check("mixed.example.com");
    expect(!mixed.ok && mixed.reason).toContain("10.1.1.1");
    expect((await get(f, `http://flip.example.com:${f.originPort}/`)).status).toBe(403);
  } finally {
    await done(f);
  }
});

test("allowing private networks lets a name reach them; trusted fixture names skip the check", async () => {
  let allow = true;
  const f = await fixture(
    { "dev.example.com": ["127.0.0.1"], "site-a.test": ["127.0.0.1"] },
    { allowPrivate: () => allow, trusted: ["site-a.test"] },
  );
  try {
    expect((await get(f, `http://dev.example.com:${f.originPort}/`)).status).toBe(200);
    allow = false;
    expect((await get(f, `http://dev.example.com:${f.originPort}/`)).status).toBe(403);
    expect((await get(f, `http://site-a.test:${f.originPort}/`)).status).toBe(200);
  } finally {
    await done(f);
  }
});

test("the guard forwards only proxy requests", async () => {
  const f = await fixture({ localhost: ["127.0.0.1"] });
  try {
    // An origin-form request (a web page fetching the proxy port itself)
    // is not a proxy request.
    const direct = await get(f, "/http://localhost/");
    expect(direct.status).toBe(400);
    expect((await get(f, `ftp://localhost:${f.originPort}/`)).status).toBe(400);
    expect(f.hits).toEqual([]);
  } finally {
    await done(f);
  }
});

test("refuseAll refuses every proxy request", async () => {
  const port = await refuseAll();
  const f = { port } as Fixture;
  expect((await get(f, "http://example.com/")).status).toBe(403);
  expect((await get(f, "http://localhost/")).status).toBe(403);
  expect((await tunnel(f, "example.com:443")).status).toBe("HTTP/1.1 403 Forbidden");
});
