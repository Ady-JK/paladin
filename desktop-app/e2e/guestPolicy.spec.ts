import { expect, test } from "@playwright/test";
import { allowedPersonalPopup, guestPermissionAllowed } from "../electron/guestPolicy";

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
