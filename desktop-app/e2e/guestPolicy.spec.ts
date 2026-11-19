import { expect, test } from "@playwright/test";
import {
  allowedGuestUrl,
  allowedPersonalPopup,
  guestPermissionAllowed,
  validGuestPartition,
  validSessionId,
  validViewName,
} from "../electron/guestPolicy";

test("chat-site panes get only clipboard write and fullscreen", () => {
  for (const p of ["clipboard-sanitized-write", "fullscreen"]) expect(guestPermissionAllowed(p)).toBe(true);
  for (const p of [
    "media",
    "clipboard-read",
    "openExternal",
    "geolocation",
    "notifications",
    "local-network-access",
    "loopback-network",
    "midi",
    "hid",
    "serial",
    "usb",
    "display-capture",
    "unknown-new-permission",
  ]) {
    expect(guestPermissionAllowed(p)).toBe(false);
  }
});

test("personal browser allows secure sign-in popups but refuses unsafe URLs", () => {
  for (const url of ["about:blank", "https://login.example.com/authorize", "https://accounts.example.org/"]) {
    expect(allowedPersonalPopup(url)).toBe(true);
  }
  for (const url of ["http://example.com/", "file:///etc/passwd", "javascript:alert(1)", "https://u:p@example.com/"]) {
    expect(allowedPersonalPopup(url)).toBe(false);
  }
});

test("guest views navigate only to credential-free https URLs", () => {
  for (const url of ["https://chat.example.com/", "https://a.example.org/x?y=1#z"]) {
    expect(allowedGuestUrl(url)).toBe(true);
  }
  for (const url of [
    "http://example.com/",
    "about:blank",
    "file:///etc/passwd",
    "javascript:alert(1)",
    "https://u:p@example.com/",
    "not a url",
    42,
    null,
  ]) {
    expect(allowedGuestUrl(url)).toBe(false);
  }
});

test("view names and partitions are validated before they reach Electron", () => {
  expect(validGuestPartition("persist:paladin-sites")).toBe(true);
  for (const p of ["", "persist:", "default", "persist:other", "paladin-sites", "persist:paladin-", 7, null]) {
    expect(validGuestPartition(p)).toBe(false);
  }
  expect(validViewName("web:abc-123")).toBe(true);
  for (const n of ["", "agent", "web:", "web:a b", `web:${"x".repeat(200)}`, "../x", 3, undefined]) {
    expect(validViewName(n)).toBe(false);
  }
});

test("browser IPC accepts only id-shaped session ids", () => {
  expect(validSessionId("51f578a8-861b-460a-ad97-ebad85905943")).toBe(true);
  for (const id of ["", "a b", "../x", "x".repeat(200), 5, null, undefined]) expect(validSessionId(id)).toBe(false);
});
