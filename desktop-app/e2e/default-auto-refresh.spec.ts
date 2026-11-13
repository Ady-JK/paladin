import { expect, test, type Page } from "@playwright/test";

async function openWithProviders(page: Page, failing = false): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((failing) => {
    const w = window as any;
    const original = w.paladin.invoke;
    const realNow = Date.now.bind(Date);
    const realSetInterval = window.setInterval.bind(window);
    w.__timeOffset = 0;
    Date.now = () => realNow() + w.__timeOffset;
    window.setInterval = ((handler: TimerHandler, delay?: number, ...args: any[]) => {
      if (delay === 5 * 60_000 && typeof handler === "function") {
        w.__runDefaultRefreshTimer = () => handler(...args);
      }
      return realSetInterval(handler, delay, ...args);
    }) as typeof window.setInterval;
    w.__refreshCalls = [];
    w.__refreshFailure = failing;
    w.__e2eProviders = [
      {
        name: "fixture-default",
        apiType: "openai",
        apiUrl: "http://localhost:1/v1",
        model: "",
        models: ["old-model"],
        default: true,
        hasKey: true,
      },
      {
        name: "fixture-other",
        apiType: "openai",
        apiUrl: "http://localhost:2/v1",
        model: "",
        models: ["other-model"],
        default: false,
        hasKey: true,
      },
      {
        name: "github-copilot",
        apiType: "github-copilot",
        apiUrl: "https://api.githubcopilot.com",
        model: "",
        models: ["copilot-model"],
        default: false,
        hasKey: true,
      },
    ];
    w.paladin.invoke = async (method: string, args: any) => {
      if (method === "listProviders") return w.__e2eProviders;
      if (method === "setDefaultProvider") {
        for (const provider of w.__e2eProviders) provider.default = provider.name === args.name;
        return w.__e2eProviders;
      }
      if (method === "copilotSignInStart") return { userCode: "TEST-CODE", interval: 1, expiresIn: 60 };
      if (method === "copilotSignInPoll") {
        w.__e2eProviders.find((provider: any) => provider.name === "github-copilot").hasKey = true;
        return "connected";
      }
      if (method === "refreshProviders") {
        w.__refreshCalls.push(args);
        if (w.__refreshFailure) return { name: args.name, count: 0, models: [], error: "offline" };
        const provider = w.__e2eProviders.find((item: any) => item.name === args.name);
        provider.models = [args.name === "fixture-default" ? "new-model" : "refreshed-other"];
        return { name: args.name, count: 1, models: provider.models };
      }
      return original(method, args);
    };
  }, failing);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
}

test("refreshes only the default on startup and after the catalog expires", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toEqual([{ name: "fixture-default" }]);
  await page.click("#modelSelectBtn");
  await expect(page.locator('#modelTree [data-mpick="fixture-default\tnew-model"]')).toBeVisible();
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelMenu")).toBeHidden();
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelMenu")).toBeVisible();
  expect(await page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelMenu")).toBeHidden();
  await page.evaluate(() => {
    (window as any).__timeOffset = 31 * 60 * 1000;
    (window as any).__runDefaultRefreshTimer();
  });
  await expect
    .poll(() => page.evaluate(() => (window as any).__refreshCalls))
    .toEqual([{ name: "fixture-default" }, { name: "fixture-default" }]);
});

test("offline refresh keeps saved models and retries the default later", async ({ page }) => {
  await openWithProviders(page, true);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#modelSelectBtn");
  await expect(page.locator('#modelTree [data-mpick="fixture-default\told-model"]')).toBeVisible();
  expect(await page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelMenu")).toBeHidden();
  await page.evaluate(() => {
    (window as any).__refreshFailure = false;
    (window as any).__timeOffset = 61_000;
  });
  await page.click("#modelSelectBtn");
  await expect(page.locator('#modelTree [data-mpick="fixture-default\tnew-model"]')).toBeVisible();
  expect(await page.evaluate(() => (window as any).__refreshCalls)).toEqual([
    { name: "fixture-default" },
    { name: "fixture-default" },
  ]);
});

test("changing the default refreshes the new default only", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-setdefault="fixture-other"]').click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__refreshCalls))
    .toEqual([{ name: "fixture-default" }, { name: "fixture-other" }]);
});

test("updating the default provider refreshes its catalog", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-connect="fixture-default"]').click();
  await page.fill("#providerCardUrl", "http://localhost:3/v1");
  await page.click("#providerCardSave");
  await expect(page.locator("#providerCard")).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => (window as any).__refreshCalls))
    .toEqual([{ name: "fixture-default" }, { name: "fixture-default" }]);
});

test("updating a non-default provider does not refresh it", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-connect="fixture-other"]').click();
  await page.fill("#providerCardUrl", "http://localhost:4/v1");
  await page.click("#providerCardSave");
  await expect(page.locator("#providerCard")).toBeHidden();
  await expect(page.locator("#settingsProviderList")).toContainText("http://localhost:4/v1");
  expect(await page.evaluate(() => (window as any).__refreshCalls)).toEqual([{ name: "fixture-default" }]);
});

test("selecting a non-default Copilot model does not auto-refresh Copilot", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#modelSelectBtn");
  // The picker lists the default provider only until "All providers" is chosen.
  await page.selectOption("#modelProviderFilter", "*");
  await page.locator('#modelTree [data-mpick="github-copilot\tcopilot-model"]').click();
  await page.evaluate(() => {
    (window as any).__timeOffset = 31 * 60_000;
    (window as any).__runDefaultRefreshTimer();
  });
  await expect
    .poll(() => page.evaluate(() => (window as any).__refreshCalls))
    .toEqual([{ name: "fixture-default" }, { name: "fixture-default" }]);
  await expect(page.locator("#modelSelectLabel")).toHaveText("copilot-model");
});

test("signing in to non-default Copilot does not refresh it", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.evaluate(() => {
    (window as any).__e2eProviders.find((provider: any) => provider.name === "github-copilot").hasKey = false;
  });
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-connect="github-copilot"]').click();
  await expect(page.locator("#copilotSignInCode")).toHaveText("TEST-CODE");
  await expect(page.locator("#copilotSignIn")).toBeHidden({ timeout: 5000 });
  await expect(page.locator("#settingsProviderList")).toContainText("key stored");
  expect(await page.evaluate(() => (window as any).__refreshCalls)).toEqual([{ name: "fixture-default" }]);
});

test("signing in to default Copilot refreshes its catalog", async ({ page }) => {
  await openWithProviders(page);
  await expect.poll(() => page.evaluate(() => (window as any).__refreshCalls)).toHaveLength(1);
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-setdefault="github-copilot"]').click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__refreshCalls))
    .toEqual([{ name: "fixture-default" }, { name: "github-copilot" }]);
  await page.evaluate(() => {
    (window as any).__e2eProviders.find((provider: any) => provider.name === "github-copilot").hasKey = false;
  });
  await page.locator('[data-connect="github-copilot"]').click();
  await expect(page.locator("#copilotSignInCode")).toHaveText("TEST-CODE");
  await expect(page.locator("#copilotSignIn")).toBeHidden({ timeout: 5000 });
  await expect
    .poll(() => page.evaluate(() => (window as any).__refreshCalls))
    .toEqual([{ name: "fixture-default" }, { name: "github-copilot" }, { name: "github-copilot" }]);
});
