import { test, expect } from "@playwright/test";

// The WASM worker is replaced by the stub's voice object. The test pins
// the UI contract: click to start, live lines, click again to insert, and
// the model menu. The real microphone path is exercised by the app's manual
// smoke test because headless Chromium has no capture device.

const models = [
  {
    id: "tiny-streaming-en",
    label: "Moonshine Tiny Streaming (English)",
    arch: 2,
    language: "en",
    description: "Fastest. Ideal for short dictation and commands.",
    source: "bundled" as const,
  },
  {
    id: "small-streaming-en",
    label: "Moonshine Small Streaming (English)",
    arch: 4,
    language: "en",
    description: "Best balance of accuracy and speed.",
    source: "bundled" as const,
  },
];

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    (overview) => {
      (window as any).__e2eVoiceOverview = overview;
      // A silent media stream stands in for the microphone.
      navigator.mediaDevices.getUserMedia = async () => {
        const context = new AudioContext();
        const destination = context.createMediaStreamDestination();
        const oscillator = context.createOscillator();
        const gain = context.createGain();
        gain.gain.value = 0.00001;
        oscillator.connect(gain);
        gain.connect(destination);
        oscillator.start();
        (window as any).__e2eVoiceContext = context;
        return destination.stream;
      };
    },
    { available: true, models },
  );
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect.poll(() => page.evaluate(() => typeof (window as any).__e2eVoiceEmit)).toBe("function");
});

test("clicking the microphone shows live text and inserts the finished line", async ({ page }) => {
  await expect(page.locator("#voiceBtn")).toBeEnabled();
  await page.locator("#voiceBtn").click();
  await expect(page.locator("#voiceStatus")).toBeVisible();
  await expect(page.locator("#voiceStatusLabel")).toHaveText("Listening…");

  await page.evaluate(() =>
    (window as any).__e2eVoiceEmit({ event: "line", index: 0, text: "hello world", complete: false }),
  );
  await expect(page.locator("#voiceStatusText")).toHaveText("hello world");

  await page.locator("#voiceBtn").click();
  await expect(page.locator("#voiceStatusLabel")).toHaveText("Transcribing…");

  await page.evaluate(() => {
    (window as any).__e2eVoiceEmit({ event: "line", index: 0, text: "hello world", complete: true });
    (window as any).__e2eVoiceEmit({ event: "stopped" });
  });
  await expect(page.locator("#promptInput")).toHaveValue(/hello world/);
  await expect(page.locator("#voiceStatus")).toBeHidden();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eVoiceStops)).toBe(1);
});

test("the model menu lists installed models and remembers the pick", async ({ page }) => {
  await page.click("#voiceMenuBtn");
  await expect(page.locator("#voiceMenu")).toBeVisible();
  await expect(page.locator(".voice-menu-item")).toHaveCount(2);
  await page.locator(".voice-menu-item", { hasText: "Small Streaming" }).click();
  await expect(page.locator("#voiceMenu")).toBeHidden();
  await expect(page.locator("#voiceBtn")).toHaveAttribute("title", /Small Streaming/);
  expect(await page.evaluate(() => localStorage.getItem("paladin.voice.model"))).toBe("small-streaming-en");

  await page.reload();
  await expect(page.locator("#voiceBtn")).toHaveAttribute("title", /Small Streaming/);
});

test("Escape cancels without inserting text", async ({ page }) => {
  await page.locator("#voiceBtn").click();
  await expect(page.locator("#voiceStatusLabel")).toHaveText("Listening…");
  await page.evaluate(() =>
    (window as any).__e2eVoiceEmit({ event: "line", index: 0, text: "discard me", complete: false }),
  );
  await page.keyboard.press("Escape");
  await expect(page.locator("#voiceStatus")).toBeHidden();
  await expect(page.locator("#promptInput")).toHaveValue("");
});
