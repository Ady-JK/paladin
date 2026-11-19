// agentQuestion.ts draws a question an agent asked without stopping its work: an
// inline form in the conversation, answered whenever the user likes. It is the
// same card for every agent (Codex today); how the answer reaches the agent is the
// core's adapter's business. The turn keeps running around an open question, the
// composer stays usable, and an unsent draft survives the conversation being
// repainted (a session switch, a refresh).

import * as api from "../core/api";
import { state } from "../core/state";
import { escapeHtml } from "../core/format";
import type { ToolInfo } from "../core/types";
import { currentPreset } from "../features/permissions";
import { toast } from "../ui/toast";

type Question = { question: string; options: string[]; multiple?: boolean };
type Answer = { question: string; answer: string };
// Draft is what the user has chosen and typed so far for one question.
type Draft = { picked: number; text: string };

const drafts = new Map<string, Draft[]>();
// settled remembers the answers the user just sent, so a stale event that still
// says "pending" cannot reopen a question they answered.
const settled = new Map<string, Answer[]>();

const key = (sessionId: string, callId: string): string => `${sessionId}\n${callId}`;

function parseQuestions(raw: string): Question[] {
  try {
    const list = (JSON.parse(raw) as { questions?: unknown }).questions;
    if (!Array.isArray(list)) return [];
    const out: Question[] = [];
    for (const item of list) {
      if (typeof item !== "object" || item === null) continue;
      const rec = item as Record<string, unknown>;
      if (typeof rec.question !== "string" || !rec.question.trim()) continue;
      const options = Array.isArray(rec.options)
        ? rec.options.filter((o): o is string => typeof o === "string" && !!o)
        : [];
      out.push({ question: rec.question.trim(), options, multiple: rec.multiple === true });
    }
    return out;
  } catch {
    return [];
  }
}

// answersOf reads a settled card's answers out of its result.
function answersOf(result: string): Answer[] | null {
  try {
    const parsed = JSON.parse(result) as { status?: string; answers?: unknown };
    if (parsed.status !== "answered" || !Array.isArray(parsed.answers)) return null;
    return parsed.answers
      .filter((a): a is Record<string, unknown> => typeof a === "object" && a !== null)
      .map((a) => ({ question: String(a.question ?? ""), answer: String(a.answer ?? "") }));
  } catch {
    return null;
  }
}

function draftFor(id: string, questions: Question[]): Draft[] {
  let draft = drafts.get(id);
  if (!draft || draft.length !== questions.length) {
    // The first suggestion starts selected; it is only sent when the user sends.
    draft = questions.map((q) => ({ picked: q.options.length > 0 ? 0 : -1, text: "" }));
    drafts.set(id, draft);
  }
  return draft;
}

function answerText(q: Question, d: Draft): string {
  const typed = d.text.trim();
  if (typed) return typed;
  return d.picked >= 0 ? (q.options[d.picked] ?? "") : "";
}

function pendingHtml(questions: Question[], draft: Draft[], busy: boolean): string {
  const items = questions
    .map((q, i) => {
      const d = draft[i];
      const opts = q.options
        .map((o, j) => {
          const on = d.picked === j && !d.text.trim();
          return `<button type="button" class="agent-q-opt${on ? " is-picked" : ""}" data-opt="${j}" data-number="${j + 1}" aria-pressed="${on}"${busy ? " disabled" : ""}>${escapeHtml(o)}</button>`;
        })
        .join("");
      return (
        `<div class="agent-q-item" data-q="${i}"><div class="agent-q-text">${escapeHtml(q.question)}</div>` +
        (opts ? `<div class="agent-q-opts">${opts}</div>` : "") +
        `<input type="text" class="agent-q-input" spellcheck="false" placeholder="${q.options.length ? "Or type your own answer…" : "Type your answer…"}" value="${escapeHtml(d.text)}"${busy ? " disabled" : ""} />` +
        `</div>`
      );
    })
    .join("");
  return (
    `<div class="agent-q-head"><span class="agent-q-mark" aria-hidden="true">?</span><span class="agent-q-title">${questions.length > 1 ? "Questions" : "Question"}</span>` +
    `<span class="agent-q-state">${busy ? "sending…" : "waiting for you"}</span></div>` +
    `<div class="agent-q-items">${items}</div>` +
    `<div class="agent-q-actions"><button type="button" class="agent-q-send"${busy ? " disabled" : ""}>Send</button>` +
    `</div>`
  );
}

function answeredHtml(questions: Question[], answers: Answer[]): string {
  const items = questions
    .map((q, i) => {
      const a = answers.find((x) => x.question === q.question) ?? answers[i];
      return (
        `<div class="agent-q-item"><div class="agent-q-text">${escapeHtml(q.question)}</div>` +
        `<div class="agent-q-answer">${escapeHtml(a?.answer ?? "")}</div></div>`
      );
    })
    .join("");
  return (
    `<div class="agent-q-head"><span class="agent-q-mark is-done" aria-hidden="true">✓</span><span class="agent-q-title">${questions.length > 1 ? "Questions answered" : "Question answered"}</span>` +
    `<span class="agent-q-state">answered</span></div><div class="agent-q-items">${items}</div>`
  );
}

// createQuestionCard builds the card for a question call.
export function createQuestionCard(sessionId: string, tool: ToolInfo): HTMLElement {
  const card = document.createElement("div");
  card.className = "agent-q";
  card.dataset.call = tool.id;
  card.dataset.session = sessionId;
  paintQuestionCard(card, tool);
  card.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const opt = target.closest<HTMLElement>(".agent-q-opt");
    if (opt && !opt.hasAttribute("disabled")) {
      pick(card, sessionId, tool.id, opt);
    } else if (target.closest(".agent-q-send")) {
      void send(card, sessionId, tool.id);
    }
  });
  card.addEventListener("input", (event) => {
    const input = (event.target as HTMLElement).closest<HTMLInputElement>(".agent-q-input");
    if (input) typed(card, sessionId, tool.id, input);
  });
  card.addEventListener("keydown", (event) => {
    // Enter in a field sends once every question has an answer; the composer stays out of it.
    if (event.key === "Enter" && (event.target as HTMLElement).closest(".agent-q-input")) {
      event.preventDefault();
      event.stopPropagation();
      void send(card, sessionId, tool.id);
    }
  });
  return card;
}

// paintQuestionCard redraws a card for the call's current state: open, sending or answered.
export function paintQuestionCard(card: HTMLElement, tool: ToolInfo): void {
  const sessionId = card.dataset.session ?? "";
  const id = key(sessionId, tool.id);
  card.dataset.call = tool.id;
  card.dataset.args = tool.arguments;
  const questions = parseQuestions(tool.arguments);
  const answers = answersOf(tool.result) ?? settled.get(id) ?? null;
  if (answers) {
    card.dataset.state = "answered";
    card.innerHTML = answeredHtml(questions, answers);
    return;
  }
  const busy = card.dataset.state === "sending";
  card.dataset.state = busy ? "sending" : "pending";
  card.innerHTML = pendingHtml(questions, draftFor(id, questions), busy);
  refreshSend(card, questions, draftFor(id, questions));
}

function questionsOf(card: HTMLElement, sessionId: string, callId: string): { questions: Question[]; draft: Draft[] } {
  const questions = parseQuestions(card.dataset.args ?? "");
  return { questions, draft: draftFor(key(sessionId, callId), questions) };
}

function refreshSend(card: HTMLElement, questions: Question[], draft: Draft[]): void {
  const send = card.querySelector<HTMLButtonElement>(".agent-q-send");
  if (!send) return;
  const ready = questions.every((q, i) => answerText(q, draft[i]) !== "");
  send.disabled = !ready || card.dataset.state === "sending";
}

function pick(card: HTMLElement, sessionId: string, callId: string, opt: HTMLElement): void {
  const item = opt.closest<HTMLElement>(".agent-q-item");
  const { questions, draft } = questionsOf(card, sessionId, callId);
  const i = Number(item?.dataset.q);
  if (!Number.isInteger(i) || !draft[i]) return;
  draft[i].picked = Number(opt.dataset.opt);
  draft[i].text = ""; // picking an option replaces what was typed
  const input = item?.querySelector<HTMLInputElement>(".agent-q-input");
  if (input) input.value = "";
  item?.querySelectorAll<HTMLElement>(".agent-q-opt").forEach((el) => {
    const on = el === opt;
    el.classList.toggle("is-picked", on);
    el.setAttribute("aria-pressed", String(on));
  });
  refreshSend(card, questions, draft);
}

function typed(card: HTMLElement, sessionId: string, callId: string, input: HTMLInputElement): void {
  const item = input.closest<HTMLElement>(".agent-q-item");
  const { questions, draft } = questionsOf(card, sessionId, callId);
  const i = Number(item?.dataset.q);
  if (!Number.isInteger(i) || !draft[i]) return;
  draft[i].text = input.value;
  // Typing an answer takes over from the picked option.
  item?.querySelectorAll<HTMLElement>(".agent-q-opt").forEach((el) => {
    const on = !input.value.trim() && Number(el.dataset.opt) === draft[i].picked;
    el.classList.toggle("is-picked", on);
    el.setAttribute("aria-pressed", String(on));
  });
  refreshSend(card, questions, draft);
}

async function send(card: HTMLElement, sessionId: string, callId: string): Promise<void> {
  if (card.dataset.state !== "pending") return;
  const { questions, draft } = questionsOf(card, sessionId, callId);
  const answers = questions.map((q, i) => ({ question: q.question, answer: answerText(q, draft[i]) }));
  if (answers.length === 0 || answers.some((a) => !a.answer)) {
    toast("Answer every question first");
    return;
  }
  card.dataset.state = "sending";
  card.querySelectorAll<HTMLElement>("button, input").forEach((el) => el.setAttribute("disabled", ""));
  const status = card.querySelector<HTMLElement>(".agent-q-state");
  if (status) status.textContent = "sending…";
  try {
    const info = await api.answerQuestion({
      sessionId,
      callId,
      answers,
      cwd: state.cwd,
      provider: state.selectedProvider || null,
      model: state.selectedModel || null,
      mode: state.mode,
      permissions: currentPreset(),
    });
    settled.set(key(sessionId, callId), answers);
    drafts.delete(key(sessionId, callId));
    card.dataset.state = "answered";
    card.innerHTML = answeredHtml(questions, answers);
    // The live view learns about the turn the answer started (or joined).
    window.dispatchEvent(new CustomEvent("paladin:turn-submitted", { detail: info }));
  } catch (error) {
    card.dataset.state = "pending";
    toast(`Answer failed: ${String(error)}`, "error");
    paintQuestionCard(card, {
      id: callId,
      name: "agent_question",
      arguments: card.dataset.args ?? "{}",
      result: '{"status":"pending"}',
      status: "ok",
    });
  }
}
