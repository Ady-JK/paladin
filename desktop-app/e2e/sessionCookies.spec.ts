import { test, expect } from "@playwright/test";
import type { Cookie } from "electron";
import { KEEP_DAYS, persistentCookie } from "../electron/sessionCookies";

const NOW = 1_700_000_000;

function cookie(over: Partial<Cookie> = {}): Cookie {
  return {
    name: "sid",
    value: "v",
    domain: "example.com",
    hostOnly: true,
    path: "/app",
    secure: true,
    httpOnly: true,
    session: true,
    sameSite: "lax",
    ...over,
  };
}

test("a session cookie gets an expiry so it survives a restart", () => {
  const set = persistentCookie(cookie(), NOW)!;
  expect(set.expirationDate).toBe(NOW + KEEP_DAYS * 86400);
  expect(set).toMatchObject({
    url: "https://example.com/app",
    name: "sid",
    value: "v",
    path: "/app",
    secure: true,
    httpOnly: true,
    sameSite: "lax",
  });
});

test("a cookie that already persists is left alone", () => {
  expect(persistentCookie(cookie({ session: false, expirationDate: NOW + 5 }), NOW)).toBeNull();
});

test("a host-only cookie stays host-only; a domain cookie keeps its domain", () => {
  expect(persistentCookie(cookie(), NOW)).not.toHaveProperty("domain");
  const shared = persistentCookie(cookie({ domain: ".example.com", hostOnly: false }), NOW)!;
  expect(shared.domain).toBe(".example.com");
  expect(shared.url).toBe("https://example.com/app");
});

test("an insecure cookie is written back over http, and an empty host is skipped", () => {
  expect(persistentCookie(cookie({ secure: false }), NOW)!.url).toBe("http://example.com/app");
  expect(persistentCookie(cookie({ domain: "" }), NOW)).toBeNull();
});
