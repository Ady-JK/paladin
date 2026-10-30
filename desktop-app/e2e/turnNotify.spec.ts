import { test, expect } from "@playwright/test";
import { noteFor } from "../electron/turnNotify";

// Pure decision logic for native notifications (no browser needed).
const turn = (over: Record<string, unknown> = {}) => ({
  event: "turn/done",
  sessionId: "s1",
  turn: { message: "fix the flaky test please", state: "done", ...over },
});

test("finished turn notifies with the message", () => {
  const n = noteFor(turn());
  expect(n).toEqual({ sessionId: "s1", title: "Paladin · turn finished", body: "fix the flaky test please" });
});

test("display text wins and long bodies are clipped", () => {
  const n = noteFor(turn({ display: "x".repeat(300) }))!;
  expect(n.body.length).toBeLessThanOrEqual(120);
  expect(n.body.endsWith("…")).toBe(true);
});

test("failed and stopped turns say so; dropped ones are silent", () => {
  expect(noteFor(turn({ error: "boom" }))!.title).toBe("Paladin · turn failed");
  expect(noteFor(turn({ error: "boom" }))!.body).toContain("boom");
  expect(noteFor(turn({ stopped: true }))).toBeNull();
  expect(noteFor(turn({ cancelled: true }))).toBeNull();
});

test("a reached budget says so; an unknown-cost warning stays quiet", () => {
  const budget = (state: string) => ({
    event: "chat/budget",
    sessionId: "s2",
    budget: { state, spent: 1.5, limit: 1 },
  });
  expect(noteFor(budget("reached"))).toEqual({
    sessionId: "s2",
    title: "Paladin · budget reached",
    body: "Spent $1.50 of $1.00 — the agent is wrapping up with a summary.",
  });
  expect(noteFor(budget("unknownCost"))).toBeNull();
  expect(noteFor(budget("spendIncomplete"))).toBeNull();
});

test("a running ask asks for input; other events are ignored", () => {
  const ask = { event: "chat/tool", sessionId: "s2", tool: { name: "ask", status: "running", arguments: "{}" } };
  expect(noteFor(ask)).toEqual({
    sessionId: "s2",
    title: "Paladin · needs your input",
    body: "The agent asked a question.",
  });
  expect(noteFor({ ...ask, tool: { name: "ask", status: "ok" } })).toBeNull();
  expect(noteFor({ event: "chat/delta", sessionId: "s1", delta: "x" })).toBeNull();
  expect(noteFor({ event: "turn/start", sessionId: "s1", turn: {} })).toBeNull();
  expect(noteFor(null)).toBeNull();
});

test("a pending approval notifies; resolved ones are silent", () => {
  const ap = {
    event: "chat/approval",
    sessionId: "s3",
    approval: { id: "c1", state: "pending", command: "npm install x" },
  };
  expect(noteFor(ap)).toEqual({ sessionId: "s3", title: "Paladin · needs approval", body: "npm install x" });
  expect(noteFor({ ...ap, approval: { id: "c2", state: "pending", path: "/etc/hosts" } })!.body).toBe(
    "Write /etc/hosts",
  );
  expect(noteFor({ ...ap, approval: { ...ap.approval, state: "approved" } })).toBeNull();
});
