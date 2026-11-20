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
  // The thumbnail shows as soon as the message does, before the turn ends.
  const thumb = page.locator("#messages .session-image-thumb");
  await expect(thumb).toHaveCount(1);
  const bubble = await page.locator("#messages .bubble-user").last().boundingBox();
  const box = await thumb.boundingBox();
  expect(box && bubble && box.y + box.height <= bubble.y + 1).toBe(true);
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
  // The image sits above the message bubble, not under it.
  const thumb = await page.locator("#messages .session-image-thumb").boundingBox();
  const bubble = await page.locator("#messages .bubble-user").last().boundingBox();
  expect(thumb && bubble && thumb.y + thumb.height <= bubble.y + 1).toBe(true);
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
  // The page behind the image is blurred, not just dimmed: the blur has
  // to be visible, so the scrim stays light and the filter carries it.
  const backdrop = await page.evaluate(() => {
    const cs = getComputedStyle(document.getElementById("sessionImageViewer")!);
    return { blur: cs.backdropFilter, scrim: cs.backgroundColor };
  });
  expect(backdrop.blur).toMatch(/blur\((1[0-9]|[2-9][0-9])px\)/);
  expect(backdrop.scrim).toMatch(/0\.4|0\.5|0\.6/);
  await page.keyboard.press("Escape");
  await expect(page.locator("#sessionImageViewer")).toBeHidden();
});

test("aligns files and collapses more than three sent images into a browsable thumbnail", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eRunTurn = () => new Promise(() => {});
    const transfer = new DataTransfer();
    for (let i = 1; i <= 4; i++)
      transfer.items.add(
        new File([Uint8Array.from([137, 80, 78, 71, 13, 10, 26, 10])], `image-${i}.png`, { type: "image/png" }),
      );
    document
      .querySelector(".composer-float")!
      .dispatchEvent(new DragEvent("drop", { bubbles: true, dataTransfer: transfer }));
  });
  await page.fill("#promptInput", "@src/ma");
  await expect(page.locator("#mentionPopup")).toContainText("src/main.go");
  await page.keyboard.press("Enter");
  await page.fill("#promptInput", "Review these");
  await page.click("#sendBtn");
  const strip = page.locator("#messages .user-images");
  const visibleImages = strip.locator(".session-image-thumb:visible");
  await expect(visibleImages).toHaveCount(1);
  await expect(visibleImages.first()).toContainText("+3");
  // Both rectangles are read in one synchronous call: the row slides in over
  // 260ms, so two separate reads on a busy machine would catch it mid-move and
  // report a skew that is not in the layout.
  const offset = await strip.evaluate((el) => {
    const image = el.querySelector(".session-image-thumb:not([hidden])")!.getBoundingClientRect();
    const file = el.querySelector(".session-file-card")!.getBoundingClientRect();
    return Math.abs(image.y - file.y);
  });
  expect(offset).toBeLessThanOrEqual(1);
  await visibleImages.first().click();
  await expect(page.locator("#sessionImageViewer .image-viewer-count")).toHaveText("1 / 4");
  await page.locator("#sessionImageViewer .image-viewer-next").click();
  await expect(page.locator("#sessionImageViewer .image-viewer-count")).toHaveText("2 / 4");
});

test("shows all three images without a counter", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eRunTurn = () => new Promise(() => {});
    const transfer = new DataTransfer();
    for (let i = 1; i <= 3; i++)
      transfer.items.add(
        new File([Uint8Array.from([137, 80, 78, 71, 13, 10, 26, 10])], `image-${i}.png`, { type: "image/png" }),
      );
    document
      .querySelector(".composer-float")!
      .dispatchEvent(new DragEvent("drop", { bubbles: true, dataTransfer: transfer }));
  });
  await page.fill("#promptInput", "Review these");
  await page.click("#sendBtn");
  await expect(page.locator("#messages .user-images .session-image-thumb:visible")).toHaveCount(3);
  await expect(page.locator("#messages .user-image-count")).toHaveCount(0);
});
