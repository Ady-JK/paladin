import { expect, test } from "@playwright/test";
import {
  localBrowserRequest,
  localBrowserUrl,
  navigationAllowed,
  privateHost,
  remoteOpenRefusal,
  requestAllowed,
  siteKey,
  webBrowserUrl,
} from "../electron/browserPolicy";

test("local apps are loopback HTTP; web pages are http(s) without credentials", () => {
  for (const url of ["http://localhost:8080", "http://127.0.0.1:3000/path?q=1"])
    expect(localBrowserUrl(url)).toBe(true);
  for (const url of ["https://localhost", "http://example.com", "http://localhost.evil.test", "http://[::1]"]) {
    expect(localBrowserUrl(url)).toBe(false);
  }
  for (const url of ["https://example.com/a", "http://intranet.corp/x", "http://localhost:8080"]) {
    expect(webBrowserUrl(url)).toBe(true);
  }
  for (const url of [
    "file:///tmp/a",
    "javascript:alert(1)",
    "http://user@example.com",
    "ftp://example.com",
    "about:blank",
  ]) {
    expect(webBrowserUrl(url)).toBe(false);
  }
});

test("a local page cannot load remote resources", () => {
  expect(localBrowserRequest("ws://127.0.0.1:8080/socket")).toBe(true);
  expect(localBrowserRequest("https://cdn.example.com/app.js")).toBe(false);
  expect(requestAllowed("https://cdn.example.com/app.js", "http://localhost:5173/")).toBe(false);
  expect(requestAllowed("http://localhost:5173/main.js", "http://localhost:5173/")).toBe(true);
});

test("a web page loads public resources but never this machine or a private address", () => {
  const top = "https://chatgpt.com/";
  for (const url of ["https://cdn.oaistatic.com/app.js", "wss://ws.chatgpt.com/socket", "data:image/png;base64,AA"]) {
    expect(requestAllowed(url, top)).toBe(true);
  }
  for (const url of [
    "http://localhost:3000/api",
    "http://127.0.0.1:8080/",
    "http://10.0.0.5/admin",
    "http://192.168.1.1/",
    "http://169.254.169.254/latest",
    "http://[::1]:8080/",
    "http://172.20.1.1/",
    "file:///etc/passwd",
    "https://user:pw@example.com/",
  ]) {
    expect(requestAllowed(url, top)).toBe(false);
  }
  // A page the user opened on a private address may load from itself.
  expect(requestAllowed("http://10.1.2.3/app.js", "http://10.1.2.3/")).toBe(true);
  expect(requestAllowed("http://127.0.0.1/app.js", "http://10.1.2.3/")).toBe(false);
});

test("navigation stays on the sites opened in the session", () => {
  const sites = ["chatgpt.com", siteKey("http://127.0.0.1:5173/")];
  expect(navigationAllowed("https://chatgpt.com/c/123", sites)).toBe(true);
  expect(navigationAllowed("https://auth.chatgpt.com/login", sites)).toBe(true);
  // A local service counts by port, whichever name the URL used.
  expect(siteKey("http://127.0.0.1:5173/x")).toBe("localhost:5173");
  expect(siteKey("http://localhost/")).toBe("localhost:80");
  expect(siteKey("https://Docs.Example.com/a")).toBe("docs.example.com");
  expect(navigationAllowed("http://localhost:5173/other", sites)).toBe(true);
  expect(navigationAllowed("http://127.0.0.1:5173/", sites)).toBe(true);
  expect(navigationAllowed("http://localhost:4000/", sites)).toBe(false);
  expect(navigationAllowed("http://localhost:8080", sites)).toBe(false);
  expect(navigationAllowed("https://evil.test/", sites)).toBe(false);
  expect(navigationAllowed("https://chatgpt.com.evil.test/", sites)).toBe(false);
  expect(navigationAllowed("file:///etc/passwd", sites)).toBe(false);
});

test("private hosts", () => {
  for (const h of [
    "localhost",
    "app.localhost",
    "127.0.0.1",
    "10.2.3.4",
    "172.16.0.1",
    "192.168.0.1",
    "169.254.1.1",
    "100.64.0.1",
    "::1",
    "fd00::1",
    "fe80::1",
  ]) {
    expect(privateHost(h)).toBe(true);
  }
  for (const h of ["example.com", "8.8.8.8", "172.32.0.1", "100.128.0.1", "2001:db8::1"]) {
    expect(privateHost(h)).toBe(false);
  }
});

test("a remote core cannot open this computer's own services", () => {
  for (const url of [
    "http://localhost:3000/",
    "http://127.0.0.1:4000/v1",
    "http://[::1]/",
    "http://192.168.1.1/",
    "http://10.0.0.5/",
  ]) {
    expect(remoteOpenRefusal(url)).not.toBe("");
  }
  for (const url of ["https://docs.example.com/", "https://chatgpt.com/"]) expect(remoteOpenRefusal(url)).toBe("");
});
