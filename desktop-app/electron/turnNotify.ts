// turnNotify decides which sidecar events deserve a native notification
// (main shows them only while the window is unfocused or closed).
// Pure so it can be tested without Electron.

export interface TurnNote {
  sessionId: string;
  title: string;
  body: string;
}

const MAX_BODY = 120;

function clip(text: string): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > MAX_BODY ? `${flat.slice(0, MAX_BODY - 1)}…` : flat;
}

export function noteFor(msg: any): TurnNote | null {
  if (!msg || typeof msg.sessionId !== "string") return null;
  const sessionId: string = msg.sessionId;
  if (msg.event === "chat/tool") {
    const tool = msg.tool;
    if (tool?.name === "ask" && tool.status === "running") {
      return { sessionId, title: "Paladin · needs your input", body: "The agent asked a question." };
    }
    return null;
  }
  if (msg.event === "chat/approval") {
    const ap = msg.approval;
    if (!ap || ap.state !== "pending") return null;
    const what = ap.command ? String(ap.command) : ap.path ? `Write ${ap.path}` : "A tool call";
    return { sessionId, title: "Paladin · needs approval", body: clip(what) };
  }
  if (msg.event !== "turn/done" || !msg.turn) return null;
  const turn = msg.turn;
  // The user stopped or dropped it themselves: nothing to tell them.
  if (turn.cancelled || turn.stopped) return null;
  const what = clip(String(turn.display || turn.message || "")) || "(continue)";
  if (turn.error) {
    return { sessionId, title: "Paladin · turn failed", body: clip(`${what} — ${turn.error}`) };
  }
  if (turn.needsContinue) {
    return { sessionId, title: "Paladin · paused at the round cap", body: what };
  }
  return { sessionId, title: "Paladin · turn finished", body: what };
}
