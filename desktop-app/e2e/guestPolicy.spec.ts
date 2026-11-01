import { expect, test } from "@playwright/test";
import { allowedChatPopup, guestPermissionAllowed } from "../electron/guestPolicy";

test("chat auth popup policy is restricted to provider HTTPS origins", () => {
  expect(allowedChatPopup("https://auth.openai.com/login")).toBe(true);
  expect(allowedChatPopup("https://claude.ai/login")).toBe(true);
  expect(allowedChatPopup("https://chatgpt.com.evil.test/steal")).toBe(false);
  expect(allowedChatPopup("http://chatgpt.com/login")).toBe(false);
  expect(allowedChatPopup("file:///etc/passwd")).toBe(false);
});

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
