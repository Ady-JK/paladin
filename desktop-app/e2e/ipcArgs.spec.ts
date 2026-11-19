import { expect, test } from "@playwright/test";
import { absolutePath, errText, externalUrl, field, secretPath, withinBytes } from "../electron/ipcArgs";

test("field reads a string property and nothing else", () => {
  expect(field({ rel: "a/b" }, "rel")).toBe("a/b");
  for (const value of [null, undefined, 5, "x", [], { rel: 5 }, { other: "x" }]) expect(field(value, "rel")).toBe("");
});

test("errText unwraps Error and stringifies the rest", () => {
  expect(errText(new Error("boom"))).toBe("boom");
  expect(errText("plain")).toBe("plain");
  expect(errText(7)).toBe("7");
});

test("withinBytes bounds the serialized size and survives unserializable values", () => {
  expect(withinBytes({ a: "x".repeat(10) }, 100)).toBe(true);
  expect(withinBytes({ a: "x".repeat(200) }, 100)).toBe(false);
  expect(withinBytes(undefined, 10)).toBe(true);
  const cycle: Record<string, unknown> = {};
  cycle.self = cycle;
  expect(withinBytes(cycle, 1000)).toBe(false);
  expect(withinBytes({ n: 1n }, 1000)).toBe(false);
});

test("secretPath flags credentials, keys and VCS internals but not ordinary files", () => {
  for (const rel of [
    ".env",
    "app/.env.local",
    ".env.production",
    ".git/config",
    "src/.git/HEAD",
    ".ssh/id_rsa",
    "home/.aws/credentials",
    ".npmrc",
    "deploy/server.pem",
    "keys/tls.KEY",
    "store.p12",
    "id_ed25519",
    "sub\\.env",
    ".netrc",
  ]) {
    expect(secretPath(rel), rel).toBe(true);
  }
  for (const rel of [
    "README.md",
    "src/environment.ts",
    ".env.example",
    ".env.sample",
    "docs/keys.md",
    ".github/workflows/ci.yml",
    "id_rsa.pub",
    "gitignore.txt",
  ]) {
    expect(secretPath(rel), rel).toBe(false);
  }
});

test("externalUrl enforces scheme, length and credentials", () => {
  expect(externalUrl("https://example.com/a?b=1", {})?.href).toBe("https://example.com/a?b=1");
  expect(externalUrl("http://example.com/", {})).toBeNull();
  expect(externalUrl("http://example.com/", { http: true })?.protocol).toBe("http:");
  expect(externalUrl("mailto:a@b.example", {})).toBeNull();
  expect(externalUrl("mailto:a@b.example", { mailto: true })?.protocol).toBe("mailto:");
  for (const bad of [
    "file:///etc/passwd",
    "javascript:alert(1)",
    "https://u:p@example.com/",
    "not a url",
    `https://example.com/${"a".repeat(3000)}`,
    5,
    null,
  ]) {
    expect(externalUrl(bad, { http: true, mailto: true }), String(bad).slice(0, 30)).toBeNull();
  }
});

test("absolutePath accepts only absolute strings without control characters", () => {
  expect(absolutePath("/Users/me/work")).toBe("/Users/me/work");
  for (const bad of [null, undefined, 3, "", "relative/dir", "/tmp/a\0b", "/tmp/a\nb"]) {
    expect(absolutePath(bad)).toBeUndefined();
  }
});
