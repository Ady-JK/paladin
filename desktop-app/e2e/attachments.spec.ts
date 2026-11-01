import { expect, test } from "@playwright/test";
import { showSidebar } from "./drawer";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
});

test("dropped image appears as a removable card and is copied into the session on send", async ({ page }) => {
  await page.evaluate(() => {
    const transfer = new DataTransfer();
    transfer.items.add(
      new File([Uint8Array.from([137, 80, 78, 71, 13, 10, 26, 10])], "screen.png", { type: "image/png" }),
    );
    document
      .querySelector(".composer-float")!
      .dispatchEvent(new DragEvent("drop", { bubbles: true, dataTransfer: transfer }));
  });
  await expect(page.locator("#attachmentCards .attachment-card")).toContainText("screen.png");
  await expect(page.locator("#attachmentCards img")).toBeVisible();
  await page.locator("#promptInput").fill("Review this screenshot");
  await page.locator("#sendBtn").click();
  await expect(page.locator("#attachmentCards .attachment-card")).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eUploads?.length ?? 0)).toBe(1);
  expect(await page.evaluate(() => (window as any).__e2eSubmits[0].attachmentIds)).toEqual(["file-1"]);
});

test("a finished turn mounts the user's image from sessionImages", async ({ page }) => {
  await page.addInitScript(() => {
    const at = Math.floor(Date.now() / 1000);
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    let sent = false;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "submitChat") sent = true;
      if (cmd === "sessionHistory") {
        if (!sent) return [];
        return [
          {
            role: "user",
            content: "look",
            createdAt: at,
            index: 0,
            attachments: [
              {
                id: "a1",
                name: "look.png",
                path: "/sessions/sess-notify-1/attachments/look.png",
                size: 4,
                mimeType: "image/png",
                image: {
                  path: "/sessions/sess-notify-1/attachments/look.png",
                  mimeType: "image/png",
                  size: 4,
                  sha256: "stub",
                },
              },
            ],
          },
          { role: "assistant", content: "done", createdAt: at },
        ];
      }
      return base(cmd, args);
    };
  });
  await page.reload();
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await page.fill("#promptInput", "look");
  await page.click("#sendBtn");
  // The live row had no attachments; finishTurn asks sessionImages and
  // mounts the stored one.
  await expect(page.locator("#messages .session-image-thumb")).toHaveCount(1);
});

test("stored screenshots render small and open in the session image viewer", async ({ page }) => {
  await page.addInitScript(() => {
    const at = Math.floor(Date.now() / 1000);
    (window as any).__e2eHistory = [
      { role: "user", content: "Look at this", createdAt: at, index: 0 },
      { role: "assistant", content: "Screenshot saved.", createdAt: at },
      {
        role: "tool",
        name: "browser",
        content: '{"path":"/sessions/sess-notify-1/browser/screenshots/one.png"}',
        createdAt: at,
        status: "ok",
        image: {
          path: "/sessions/sess-notify-1/browser/screenshots/one.png",
          mimeType: "image/png",
          size: 64,
          sha256: "stub",
        },
      },
      {
        role: "tool",
        name: "browser",
        content: '{"path":"/sessions/sess-notify-1/browser/screenshots/two.png"}',
        createdAt: at,
        status: "ok",
        image: {
          path: "/sessions/sess-notify-1/browser/screenshots/two.png",
          mimeType: "image/png",
          size: 64,
          sha256: "stub",
        },
      },
    ];
  });
  await page.reload();
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#messages .session-image-thumb")).toHaveCount(2);
  await page.locator("#messages .session-image-thumb").first().click();
  await expect(page.locator("#sessionImageViewer")).toBeVisible();
  await expect(page.locator("#sessionImageViewer .image-viewer-count")).toHaveText("1 / 2");
  await page.locator("#sessionImageViewer .image-viewer-next").click();
  await expect(page.locator("#sessionImageViewer .image-viewer-count")).toHaveText("2 / 2");
  await page.keyboard.press("Escape");
  await expect(page.locator("#sessionImageViewer")).toBeHidden();
});
