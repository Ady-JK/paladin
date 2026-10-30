// ask.spec.ts covers the ask-question wizard: one question at a time,
// arrow navigation, Enter to advance, review screen, submit answers.
import { test, expect, type Page } from "@playwright/test";

const QUESTIONS = [
  { question: "Deploy where?", options: ["staging", "production"] },
  { question: "Region?", options: [] },
];

// Installs a stub turn that emits a running ask tool call, then resolves
// the chat promise once the wizard answers through answerAsk.
async function stubAskTurn(page: Page, questions: unknown[] = QUESTIONS) {
  await page.evaluate((questions) => {
    const w = window as any;
    w.__e2eAskAnswers = null;
    w.__e2eAskCancelled = null;
    const base = w.__paladinBaseInvoke.bind(window);
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "answerAsk") {
        w.__e2eAskAnswers = args.answers;
        const handlers = w.__e2eAskHandlers;
        handlers?.onTool({
          id: "c-ask-1",
          name: "ask",
          arguments: JSON.stringify({ questions }),
          result: JSON.stringify({ answers: args.answers }),
          status: "ok",
        });
        w.__e2eAskDone?.({
          sessionId: "sess-notify-1",
          reply: "Deploying to staging.",
          stopped: false,
        });
        return true;
      }
      if (cmd === "cancelAsk") {
        w.__e2eAskCancelled = args.callId;
        w.__e2eAskDone?.({
          sessionId: "sess-notify-1",
          reply: "",
          stopped: true,
        });
        return true;
      }
      return base(cmd, args);
    };
    (window as any).__e2eRunTurn = (params: any, handlers: any) => {
      const ww = window as any;
      ww.__e2eAskHandlers = handlers;
      ww.__e2eChats = ww.__e2eChats || [];
      ww.__e2eChats.push({ sessionId: params.sessionId, message: params.message });
      return new Promise((resolve) => {
        ww.__e2eAskDone = resolve;
        setTimeout(() => {
          handlers.onTool({
            id: "c-ask-1",
            name: "ask",
            arguments: JSON.stringify({ questions }),
            result: "",
            status: "running",
          });
        }, 50);
      });
    };
  }, questions);
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("the wizard walks one question at a time and submits every answer", async ({ page }) => {
  await stubAskTurn(page);
  await page.fill("#promptInput", "ship it");
  await page.click("#sendBtn");

  const modal = page.locator("#askModal");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("Question 1 of 2");
  await expect(modal).toContainText("Deploy where?");

  // ArrowRight moves forward, ArrowLeft moves back.
  await page.keyboard.press("ArrowRight");
  await expect(modal).toContainText("Question 2 of 2");
  await page.keyboard.press("ArrowLeft");
  await expect(modal).toContainText("Question 1 of 2");

  // Pick the second option, then Enter advances.
  await modal.getByRole("button", { name: /production/ }).click();
  await page.keyboard.press("Enter");
  await expect(modal).toContainText("Question 2 of 2");

  // No options: type a custom answer, Enter reaches review.
  await modal.locator('[data-ask="custom"]').fill("east");
  await page.keyboard.press("Enter");
  await expect(modal).toContainText("Review");
  await expect(modal).toContainText("production");
  await expect(modal).toContainText("east");

  // Enter on review submits; the agent turn continues on its own.
  await page.keyboard.press("Enter");
  await expect(modal).toBeHidden();
  const answers = await page.evaluate(() => (window as any).__e2eAskAnswers);
  expect(answers).toEqual([
    { question: "Deploy where?", answer: "production" },
    { question: "Region?", answer: "east" },
  ]);
  await expect(page.locator("#messages")).toContainText("Deploying to staging.");
});

test("Escape cancels the wizard and stops the turn", async ({ page }) => {
  await stubAskTurn(page);
  await page.fill("#promptInput", "ship it");
  await page.click("#sendBtn");

  const modal = page.locator("#askModal");
  await expect(modal).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(modal).toBeHidden();
  const cancelled = await page.evaluate(() => (window as any).__e2eAskCancelled);
  expect(cancelled).toBe("c-ask-1");
});

test("a multiple-choice question takes several options plus free text", async ({ page }) => {
  await stubAskTurn(page, [
    {
      question: "Which checks?",
      options: ["lint", "unit", "e2e", "types", "bench"],
      multiple: true,
    },
  ]);
  await page.fill("#promptInput", "ship it");
  await page.click("#sendBtn");
  const modal = page.locator("#askModal");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("Pick any");
  // All five options show (up to six are allowed), as checkboxes.
  await expect(modal.locator('[data-ask-opt][role="checkbox"]')).toHaveCount(5);
  // Nothing is preselected for multi-select.
  await expect(modal.locator('[data-ask-opt][aria-checked="true"]')).toHaveCount(0);
  await modal.getByRole("checkbox", { name: /lint/ }).click();
  await page.keyboard.press("3");
  await expect(modal.locator('[data-ask-opt][aria-checked="true"]')).toHaveCount(2);
  // Toggling again unselects.
  await page.keyboard.press("3");
  await page.keyboard.press("5");
  await modal.locator('[data-ask="custom"]').fill("fuzz");
  await page.keyboard.press("Enter");
  await expect(modal).toContainText("Review");
  await expect(modal).toContainText("lint, bench, fuzz");
  await page.keyboard.press("Enter");
  await expect(modal).toBeHidden();
  const answers = await page.evaluate(() => (window as any).__e2eAskAnswers);
  expect(answers).toEqual([{ question: "Which checks?", answer: "lint, bench, fuzz", selected: ["lint", "bench"] }]);
});
