import { test, expect } from "@playwright/test";

// Startup refreshes only the default; add/edit does not fetch, and
// an explicit row refresh contacts only the named provider.
test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRefreshCalls = [];
    w.__e2eProviders = [
      {
        name: "fixture-default",
        apiType: "openai",
        apiUrl: "http://localhost:1/v1",
        model: "",
        models: ["first-model"],
        default: true,
        hasKey: true,
      },
      {
        name: "fixture-other",
        apiType: "openai",
        apiUrl: "http://localhost:2/v1",
        model: "",
        models: [],
        default: false,
        hasKey: true,
      },
      {
        name: "litellm",
        apiType: "openai",
        apiUrl: "http://localhost:4000/v1",
        model: "",
        models: [],
        default: false,
        hasKey: false,
      },
    ];
    const prev = w.paladin.invoke;
    w.paladin.invoke = async (cmd: string, args: any) => {
      if (cmd === "listProviders") return w.__e2eProviders;
      if (cmd === "refreshProviders") {
        w.__e2eRefreshCalls.push(args ?? {});
        if (args && args.name) {
          return { name: args.name, count: 3, models: ["a", "b", "c"] };
        }
        return true;
      }
      return prev(cmd, args);
    };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eRefreshCalls))
    .toEqual([{ name: "fixture-default" }]);
});

const refreshCalls = (page: any) => page.evaluate(() => (window as any).__e2eRefreshCalls);

test("startup refreshes only the default provider", async ({ page }) => {
  expect(await refreshCalls(page)).toEqual([{ name: "fixture-default" }]);
});

test("saving a provider does not fetch models", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.click("#settingsAddProviderBtn");
  await page.fill("#providerCardName", "together");
  await page.fill("#providerCardUrl", "https://api.together.xyz/v1");
  await page.fill("#providerCardKey", "sk-test");
  await page.click("#providerCardSave");
  await expect(page.locator("#providerCard")).toBeHidden();
  await page.waitForTimeout(300);
  expect(await refreshCalls(page)).toEqual([{ name: "fixture-default" }]);
});

test("row refresh fetches only that provider", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  const btn = page.locator('[data-refresh="fixture-other"]');
  await expect(btn).toBeVisible();
  await btn.click();
  await expect.poll(() => refreshCalls(page)).toEqual([{ name: "fixture-default" }, { name: "fixture-other" }]);
  await expect(page.locator("body")).toContainText("fixture-other: 3 models");
});

test("LiteLLM's refreshed models appear in the composer even with a large catalog", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const previous = w.paladin.invoke;
    w.paladin.invoke = async (method: string, args: any) => {
      if (method === "refreshProviders" && args?.name === "litellm") {
        w.__e2eProviders.find((provider: any) => provider.name === "litellm").models = [
          "New Proxy Model",
          "Other Proxy Model",
        ];
        return { name: "litellm", count: 2, models: ["New Proxy Model", "Other Proxy Model"] };
      }
      if (method === "listProviders") {
        const providers = await previous(method, args);
        providers.find((provider: any) => provider.name === "fixture-other").models = Array.from(
          { length: 400 },
          (_, index) => `a-model-${String(index).padStart(3, "0")}`,
        );
        return providers;
      }
      return previous(method, args);
    };
  });
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-refresh="litellm"]').click();
  await expect(page.locator("body")).toContainText("litellm: 2 models");
  await page.locator("#settingsCloseBtn").click();
  await page.locator("#modelSelectBtn").click();
  // The picker shows one provider at a time; "All providers" is capped at 300 rows,
  // which the 400-model fixture catalog would fill before litellm.
  await page.selectOption("#modelProviderFilter", "litellm");
  const row = page.locator('#modelTree [data-mpick="litellm\tNew Proxy Model"]');
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.locator("#modelSelectLabel")).toHaveText("New Proxy Model");
});
