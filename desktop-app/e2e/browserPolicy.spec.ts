import { expect, test } from "@playwright/test";
import { localBrowserRequest, localBrowserUrl } from "../electron/browserPolicy";

test("browser navigation is limited to loopback HTTP", () => {
  for (const url of ["http://localhost:8080", "http://127.0.0.1:3000/path?q=1"])
    expect(localBrowserUrl(url)).toBe(true);
  for (const url of [
    "https://localhost",
    "http://example.com",
    "http://localhost.evil.test",
    "http://[::1]",
    "file:///tmp/a",
    "javascript:alert(1)",
    "http://user@localhost:8080",
  ]) {
    expect(localBrowserUrl(url)).toBe(false);
  }
});

test("browser network requests cannot leave loopback", () => {
  expect(localBrowserRequest("ws://127.0.0.1:8080/socket")).toBe(true);
  expect(localBrowserRequest("https://cdn.example.com/app.js")).toBe(false);
  expect(localBrowserRequest("wss://example.com/socket")).toBe(false);
});
