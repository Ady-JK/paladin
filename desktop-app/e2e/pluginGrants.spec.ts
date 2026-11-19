import { expect, test } from "@playwright/test";
import type { WebFrameMain } from "electron";
import { allowRate, describeFrame, newGrant, pathInside, pluginViewUrl } from "../electron/pluginGrants";

const frame = (url: string): WebFrameMain => ({ url }) as unknown as WebFrameMain;

test("describeFrame names a caller without its query or credentials", () => {
  expect(describeFrame(frame("plugin://board/panel/ui/index.html?panel=SECRET-ID"))).toBe(
    "plugin://board/panel/ui/index.html",
  );
  expect(describeFrame(frame("https://user:pw@example.com/a/b?q=1#h"))).toBe("https://example.com/a/b");
  expect(describeFrame(null)).toBe("an unknown frame");
  expect(describeFrame(frame("not a url"))).toBe("a frame with no URL");
  expect(describeFrame(frame(`https://example.com/${"a".repeat(500)}`)).length).toBe(200);
});

test("allowRate counts per key inside a minute and resets after it", () => {
  const grant = newGrant("id", "plugin", "view", "s", "/w", ["readFile"]);
  expect([1, 2, 3].map(() => allowRate(grant, "external", 3))).toEqual([true, true, true]);
  expect(allowRate(grant, "external", 3)).toBe(false);
  expect(allowRate(grant, "file", 3)).toBe(true); // other keys are separate
  grant.rates.external.start -= 61_000;
  expect(allowRate(grant, "external", 3)).toBe(true);
});

test("pathInside accepts descendants only", () => {
  expect(pathInside("/w", "/w/a/b")).toBe(true);
  for (const target of ["/w", "/other", "/w/../x", "/wx"]) expect(pathInside("/w", target), target).toBe(false);
});

test("pluginViewUrl encodes each path part and the panel id", () => {
  expect(pluginViewUrl("board", "main", "ui/a b/index.html", "id&x=1")).toBe(
    "plugin://board/main/ui/a%20b/index.html?panel=id%26x%3D1",
  );
});

test("newGrant starts idle with the permissions it was given", () => {
  const grant = newGrant("id", "plugin", "view", "s", "/w", ["readFile", "events"]);
  expect([...grant.permissions].sort()).toEqual(["events", "readFile"]);
  expect(grant).toMatchObject({ inflight: 0, reloading: false, frame: null });
});
