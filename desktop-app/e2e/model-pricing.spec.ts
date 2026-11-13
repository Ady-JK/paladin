import { test, expect } from "@playwright/test";

test("groups providers and shows pricing for the selected model", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const bridge = (window as any).paladin;
    const original = bridge.invoke;
    bridge.invoke = (method: string, params: unknown) => {
      if (method === "listProviders")
        return Promise.resolve([
          { name: "alternate", default: false, apiType: "openai", apiUrl: "", model: "luna", models: ["luna"] },
          {
            name: "primary",
            default: true,
            apiType: "openai",
            apiUrl: "",
            model: "luna",
            models: ["luna"],
            meta: {
              luna: {
                pricing: {
                  source: "litellm",
                  fetchedAt: 1700000000,
                  tiers: [
                    { input: 0.2, output: 1.2, cacheRead: 0.02, cacheWrite: 0.25 },
                    { aboveInputTokens: 200000, input: 0.4, output: 1.8, cacheRead: 0.04, cacheWrite: 0.5 },
                  ],
                },
              },
            },
          },
        ]);
      if (method === "refreshProviders") {
        (window as any).__pricingRefresh = params;
        return Promise.resolve({ name: "primary", count: 1, models: ["luna"] });
      }
      return original(method, params);
    };
  });
  await page.goto("/");
  await expect(page.locator("#modelSelectProvider")).toHaveText("primary");
  await page.click("#modelSelectBtn");
  const rows = page.locator("#modelTree [data-mpick]");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("primary");
  await rows.first().hover();
  await expect(page.locator("#modelPriceTooltip")).toContainText("$0.2");
  await expect(page.locator("#modelPriceTooltip")).toContainText("Cache write");
  await expect(page.locator("#modelPriceTooltip")).toContainText("Up to 200,000 input tokens");
  await expect(page.locator("#modelPriceTooltip")).toContainText("Above 200,000 input tokens");
  await page.selectOption("#modelProviderFilter", "alternate");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("alternate");
  await page.click("#modelRefresh");
  await expect.poll(() => page.evaluate(() => (window as any).__pricingRefresh)).toEqual({ name: "alternate" });
  await rows.first().click();
  await expect(page.locator("#modelSelectProvider")).toHaveText("alternate");
  await expect(page.locator("#modelSelectLabel")).toHaveText("luna");
});
