// Measure synchronous model-picker input handling against a synthetic catalog.
// Start the renderer first; PALADIN_BENCH_URL and PALADIN_E2E_BROWSER are optional.
/* global window, document, performance, Event */
import process from "node:process";
import { chromium } from "@playwright/test";

const executablePath = process.env.PALADIN_E2E_BROWSER;
const browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) });
try {
  const page = await browser.newPage();
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const base = window.__paladinBaseInvoke;
    const models = Array.from({ length: 1500 }, (_, i) => `vendor/claude-sonnet-${String(i).padStart(4, "0")}`);
    window.__paladinBaseInvoke = (method, args) =>
      method === "listProviders"
        ? Promise.resolve(
            Array.from({ length: 2 }, (_, i) => ({
              name: `proxy-${i}`,
              apiType: "openai",
              apiUrl: "http://localhost:4000/v1",
              model: models[0],
              models,
              default: i === 0,
            })),
          )
        : base(method, args);
  });
  await page.goto(process.env.PALADIN_BENCH_URL || "http://localhost:5173");
  await page.locator("#modelSelectBtn").click();
  await page.locator("#modelTree [data-mpick]").first().waitFor();
  const result = await page.evaluate(() => {
    const input = document.querySelector("#modelSearch");
    const queries = ["claude-sont-13", "zzzzzzzzzzzzzzzzzzzz", "cld-snt-13", "proxy-1", "sonnet-0999"];
    const samples = [];
    for (let i = 0; i < 30; i++) {
      const start = performance.now();
      input.value = queries[i % queries.length];
      input.dispatchEvent(new Event("input", { bubbles: true }));
      samples.push(performance.now() - start);
    }
    samples.sort((a, b) => a - b);
    return { catalogEntries: 3000, samples: samples.length, medianMs: samples[15], p90Ms: samples[27] };
  });
  process.stdout.write(`${JSON.stringify(result)}\n`);
} finally {
  await browser.close();
}
