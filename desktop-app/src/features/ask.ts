// ask.ts renders the ask-question wizard: one modal that walks the
// user through a blocked ask tool call one question at a time.
// ArrowLeft/ArrowRight move between questions, Enter advances (Submit
// on the review screen), Esc cancels the call and stops the turn.
// Answers go back through answer_ask so the agent turn continues on
// its own with every question and answer as the tool result.
import { answerAsk, cancelAsk } from "../core/api";
import { escapeHtml } from "../core/format";
import type { AskQuestion } from "../core/types";
import { toast } from "../ui/toast";

type Wizard = {
  sessionId: string;
  callId: string;
  questions: AskQuestion[];
  // answers[i] is the current answer text; picked[i] is the selected
  // option index, or -1 when the answer is custom-typed (or empty).
  answers: string[];
  picked: number[];
  index: number; // questions.length means the review screen.
  overlay: HTMLElement;
  body: HTMLElement;
  submitted: boolean;
  onKey: (event: KeyboardEvent) => void;
};

let active: Wizard | null = null;
const queued: Wizard[] = [];

// parseAskArgs reads the questions out of an ask tool call's arguments.
// Null means the payload is not an ask call (or is malformed).
export function parseAskArgs(raw: string): AskQuestion[] | null {
  let args: unknown;
  try {
    args = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof args !== "object" || args === null) {
    return null;
  }
  const list = (args as Record<string, unknown>).questions;
  if (!Array.isArray(list) || list.length === 0 || list.length > 10) {
    return null;
  }
  const out: AskQuestion[] = [];
  for (const item of list) {
    if (typeof item !== "object" || item === null) {
      return null;
    }
    const rec = item as Record<string, unknown>;
    if (typeof rec.question !== "string" || !rec.question.trim()) {
      return null;
    }
    const options: string[] = [];
    if (Array.isArray(rec.options)) {
      for (const opt of rec.options.slice(0, 3)) {
        if (typeof opt === "string" && opt.trim()) {
          options.push(opt);
        }
      }
    }
    out.push({ question: rec.question.trim(), options });
  }
  return out;
}

// handleAskToolEvent opens the wizard for a running ask call. Settled
// calls close their wizard; a second ask while one is open queues
// behind it instead of stacking modals.
export function handleAskToolEvent(
  sessionId: string,
  tool: { id: string; arguments: string; status: string },
): void {
  if (tool.status !== "running") {
    settleAskToolEvent(tool.id);
    return;
  }
  if (active?.callId === tool.id || queued.some((w) => w.callId === tool.id)) {
    return;
  }
  const questions = parseAskArgs(tool.arguments);
  if (!questions) {
    return;
  }
  const wizard: Wizard = {
    sessionId,
    callId: tool.id,
    questions,
    answers: questions.map((q) => (q.options.length > 0 ? q.options[0] ?? "" : "")),
    picked: questions.map((q) => (q.options.length > 0 ? 0 : -1)),
    index: 0,
    overlay: document.createElement("div"),
    body: document.createElement("div"),
    submitted: false,
    onKey: () => {},
  };
  if (active) {
    queued.push(wizard);
    return;
  }
  openWizard(wizard);
}

// settleAskToolEvent closes the wizard for a call that finished without
// the wizard (the user stopped the turn, or the call errored).
export function settleAskToolEvent(callId: string): void {
  if (active?.callId === callId) {
    closeWizard(false);
    return;
  }
  const at = queued.findIndex((w) => w.callId === callId);
  if (at >= 0) {
    queued.splice(at, 1);
  }
}

function openWizard(wizard: Wizard): void {
  active = wizard;
  const { overlay } = wizard;
  overlay.id = "askModal";
  overlay.className = "modal-overlay fixed inset-0 z-50 flex items-center justify-center p-4";
  overlay.classList.add("is-open");
  overlay.innerHTML =
    `<div class="modal-card w-full max-w-md space-y-4 rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">` +
    `<div class="flex items-center justify-between border-b border-slate-200 pb-3 dark:border-slate-800">` +
    `<h3 class="text-sm font-semibold text-slate-900 dark:text-slate-100">Answer needed</h3>` +
    `<button type="button" data-ask="close" class="text-slate-400 hover:text-slate-200 dark:hover:text-slate-100" aria-label="Cancel">✕</button>` +
    `</div><div data-ask="body"></div></div>`;
  const body = overlay.querySelector<HTMLElement>('[data-ask="body"]');
  if (!body) {
    return;
  }
  wizard.body = body;
  overlay.querySelector('[data-ask="close"]')?.addEventListener("click", () => {
    void cancelWizard();
  });
  // The composer owns Enter/arrows/Esc globally (send + stop), and
  // focus usually stays in the composer while the turn runs, so the
  // wizard listens on document capture: it sees every key first and
  // stops the ones it handles before they send a chat message or stop
  // the turn the wizard is answering.
  wizard.onKey = (event: KeyboardEvent) => {
    if (active !== wizard) {
      return;
    }
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      event.stopPropagation();
      stepWizard(-1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      event.stopPropagation();
      stepWizard(1);
    } else if (event.key === "Enter") {
      // Let the custom input keep plain typing; Enter still advances.
      event.preventDefault();
      event.stopPropagation();
      advanceWizard();
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      void cancelWizard();
    } else if (/^[1-3]$/.test(event.key) && wizard.index < wizard.questions.length && !(event.target instanceof HTMLInputElement)) {
      const pick = Number(event.key) - 1;
      if (pick < (wizard.questions[wizard.index]?.options.length ?? 0)) {
        event.preventDefault();
        event.stopPropagation();
        pickOption(wizard.index, pick);
      }
    }
  };
  document.addEventListener("keydown", wizard.onKey, true);
  document.body.append(overlay);
  paintWizard();
}

function closeWizard(next: boolean): void {
  const finished = active;
  active = null;
  if (finished) {
    document.removeEventListener("keydown", finished.onKey, true);
    finished.overlay.remove();
  }
  if (!next) {
    return;
  }
  const following = queued.shift();
  if (following) {
    openWizard(following);
  }
}

async function cancelWizard(): Promise<void> {
  const wizard = active;
  if (!wizard || wizard.submitted) {
    return;
  }
  wizard.submitted = true;
  closeWizard(true);
  try {
    await cancelAsk(wizard.sessionId || null, wizard.callId);
  } catch (error) {
    toast(`Cancel failed: ${String(error)}`);
  }
}

function stepWizard(delta: -1 | 1): void {
  const wizard = active;
  if (!wizard) {
    return;
  }
  const last = wizard.questions.length; // review screen index.
  wizard.index = Math.min(last, Math.max(0, wizard.index + delta));
  paintWizard();
}

function pickOption(questionIdx: number, optionIdx: number): void {
  const wizard = active;
  if (!wizard) {
    return;
  }
  const options = wizard.questions[questionIdx]?.options ?? [];
  const value = options[optionIdx];
  if (value === undefined) {
    return;
  }
  wizard.picked[questionIdx] = optionIdx;
  wizard.answers[questionIdx] = value;
  paintWizard();
}

function advanceWizard(): void {
  const wizard = active;
  if (!wizard || wizard.submitted) {
    return;
  }
  if (wizard.index < wizard.questions.length) {
    const answer = (wizard.answers[wizard.index] ?? "").trim();
    if (!answer) {
      toast("Type an answer or pick an option first");
      paintWizard(true);
      return;
    }
    wizard.answers[wizard.index] = answer;
    wizard.index += 1;
    paintWizard();
    return;
  }
  void submitWizard();
}

async function submitWizard(): Promise<void> {
  const wizard = active;
  if (!wizard || wizard.submitted) {
    return;
  }
  for (let i = 0; i < wizard.questions.length; i++) {
    if (!(wizard.answers[i] ?? "").trim()) {
      wizard.index = i;
      toast("Answer every question before submitting");
      paintWizard(true);
      return;
    }
  }
  wizard.submitted = true;
  const answers = wizard.questions.map((q, i) => ({
    question: q.question,
    answer: (wizard.answers[i] ?? "").trim(),
  }));
  closeWizard(true);
  try {
    const ok = await answerAsk(wizard.sessionId, wizard.callId, answers);
    if (!ok) {
      toast("The turn already moved on — answers dropped");
    }
  } catch (error) {
    toast(`Answer failed: ${String(error)}`);
  }
}

function optionButton(questionIdx: number, text: string, optionIdx: number, selected: boolean): string {
  const ring = selected
    ? "border-accent-500 bg-accent-500/10 text-slate-900 dark:text-slate-100"
    : "border-slate-200 text-slate-600 hover:border-slate-300 dark:border-slate-700 dark:text-slate-300";
  return (
    `<button type="button" data-ask-opt="${questionIdx}:${optionIdx}" ` +
    `class="flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-xs ${ring}">` +
    `<span class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-current text-[9px] font-bold">${selected ? "●" : `${optionIdx + 1}`}</span>` +
    `<span class="min-w-0 flex-1 break-words">${escapeHtml(text)}</span></button>`
  );
}

// paintWizard renders the current question or the review screen.
// focusCustom puts the cursor in the custom input (missing answer).
function paintWizard(focusCustom = false): void {
  const wizard = active;
  if (!wizard) {
    return;
  }
  const n = wizard.questions.length;
  if (wizard.index >= n) {
    paintReview(wizard);
    wizard.overlay.querySelector<HTMLElement>('[data-ask="submit"]')?.focus();
    return;
  }
  const q = wizard.questions[wizard.index];
  if (!q) {
    return;
  }
  const picked = wizard.picked[wizard.index] ?? -1;
  const custom = picked === -1 ? (wizard.answers[wizard.index] ?? "") : "";
  wizard.body.innerHTML =
    `<div class="text-[11px] font-medium uppercase tracking-wide text-slate-400">Question ${wizard.index + 1} of ${n}</div>` +
    `<div class="mt-1 text-sm font-medium text-slate-900 dark:text-slate-100">${escapeHtml(q.question)}</div>` +
    `<div class="mt-3 space-y-2">` +
    q.options.map((opt, i) => optionButton(wizard.index, opt, i, picked === i)).join("") +
    `</div>` +
    `<input data-ask="custom" type="text" placeholder="Type your own answer…" value="${escapeHtml(custom)}" spellcheck="false" ` +
    `class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-100 px-3 py-2 text-xs text-slate-800 focus:border-accent-500 focus:outline-none dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200" />` +
    `<div class="mt-4 flex items-center justify-between">` +
    `<button type="button" data-ask="back" class="rounded-lg border border-slate-200 px-3 py-1.5 text-xs text-slate-500 dark:border-slate-700 dark:text-slate-400" ${wizard.index === 0 ? "disabled" : ""}>← Back</button>` +
    `<span class="text-[10px] text-slate-400">← → move · Enter next · 1–3 pick</span>` +
    `<button type="button" data-ask="next" class="rounded-lg bg-accent-500 px-3 py-1.5 text-xs font-semibold text-white">${wizard.index === n - 1 ? "Review →" : "Next →"}</button>` +
    `</div>`;
  wizard.body.querySelectorAll("[data-ask-opt]").forEach((el) => {
    el.addEventListener("click", () => {
      const [qi, oi] = String(el.getAttribute("data-ask-opt")).split(":").map(Number);
      if (qi !== undefined && oi !== undefined) {
        pickOption(qi, oi);
      }
    });
  });
  const customInput = wizard.body.querySelector<HTMLInputElement>('[data-ask="custom"]');
  customInput?.addEventListener("input", () => {
    if (!wizard) {
      return;
    }
    wizard.picked[wizard.index] = -1;
    wizard.answers[wizard.index] = customInput.value;
  });
  wizard.body.querySelector('[data-ask="back"]')?.addEventListener("click", () => stepWizard(-1));
  wizard.body.querySelector('[data-ask="next"]')?.addEventListener("click", () => advanceWizard());
  if (focusCustom || q.options.length === 0) {
    customInput?.focus();
  }
}

function paintReview(wizard: Wizard): void {
  const rows = wizard.questions
    .map((q, i) => {
      const answer = (wizard.answers[i] ?? "").trim() || "—";
      return (
        `<button type="button" data-ask-jump="${i}" class="block w-full rounded-lg border border-slate-200 px-3 py-2 text-left hover:border-slate-300 dark:border-slate-700">` +
        `<div class="text-[11px] font-medium text-slate-500 dark:text-slate-400">${escapeHtml(q.question)}</div>` +
        `<div class="mt-0.5 text-xs text-slate-900 dark:text-slate-100">${escapeHtml(answer)}</div></button>`
      );
    })
    .join("");
  wizard.body.innerHTML =
    `<div class="text-[11px] font-medium uppercase tracking-wide text-slate-400">Review — click a question to edit</div>` +
    `<div class="mt-2 max-h-72 space-y-2 overflow-auto">${rows}</div>` +
    `<div class="mt-4 flex items-center justify-between">` +
    `<button type="button" data-ask="back" class="rounded-lg border border-slate-200 px-3 py-1.5 text-xs text-slate-500 dark:border-slate-700 dark:text-slate-400">← Back</button>` +
    `<button type="button" data-ask="submit" class="rounded-lg bg-accent-500 px-4 py-1.5 text-xs font-semibold text-white">Submit answers (Enter)</button>` +
    `</div>`;
  wizard.body.querySelectorAll("[data-ask-jump]").forEach((el) => {
    el.addEventListener("click", () => {
      wizard.index = Number(el.getAttribute("data-ask-jump"));
      paintWizard();
    });
  });
  wizard.body.querySelector('[data-ask="back"]')?.addEventListener("click", () => stepWizard(-1));
  wizard.body.querySelector('[data-ask="submit"]')?.addEventListener("click", () => submitWizard());
}
