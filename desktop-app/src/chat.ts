// Chat: streaming with rAF coalescing + stick-to-bottom scroll.
import { chat as chatRpc } from "./api";
import { renderText } from "./format";
import { state } from "./state";
import { toolCard } from "./tools";
import type { ToolActivity } from "./types";
import { setStatus, toast } from "./ui";

const messagesEl = () => document.querySelector("#messages") as HTMLElement;
const chatViewEl = () => document.querySelector("#chatView") as HTMLElement;
const inputEl = () => document.querySelector("#promptInput") as HTMLTextAreaElement;

export function scrollToBottom(): void {
  const v = chatViewEl();
  v.scrollTop = v.scrollHeight;
}

export function addTurn(role: string, text: string): { body: HTMLElement; seg: HTMLElement } {
  const wrap = document.createElement("div");
  wrap.className = "turn-row";
  const avatar = role === "user" ? "U" : "P";
  wrap.innerHTML = `<div class="avatar ${role === "user" ? "" : "agent"}">${avatar}</div>
    <div class="bubble"><div class="who">${role}</div><div class="tbody"></div></div>`;
  const body = wrap.querySelector(".tbody") as HTMLElement;
  const seg = document.createElement("div");
  seg.innerHTML = renderText(text);
  body.append(seg);
  messagesEl().append(wrap);
  return { body, seg };
}

export async function send(resume = false): Promise<void> {
  const input = inputEl();
  const message = resume ? "" : input.value.trim();
  if (!message && !resume) return;
  if (state.busy) return;
  if (!resume) {
    input.value = "";
    addTurn("user", message);
    state.shownMessages++;
  }
  state.busy = true;
  document.querySelector("#sendBtn")!.toggleAttribute("disabled", true);
  (document.querySelector("#pauseBtn") as HTMLElement).style.display = "";
  setStatus("thinking…");

  const { body } = addTurn("agent", "");
  const seg = document.createElement("div");
  body.append(seg);
  let segment = "";
  let reply = "";
  const cards = new Map<string, { update: (t: ToolActivity) => void }>();

  let paintQueued = false;
  let stick = true;
  const view = chatViewEl();
  const onScroll = (): void => {
    stick = view.scrollHeight - view.scrollTop - view.clientHeight < 96;
  };
  view.addEventListener("scroll", onScroll);
  const gatedScroll = (): void => {
    if (stick) requestAnimationFrame(scrollToBottom);
  };

  try {
    const result = await chatRpc(
      {
        sessionId: state.sessionId,
        message,
        cwd: state.cwd,
        provider: state.selectedProvider || null,
        model: state.selectedModel || null,
        resume,
      },
      {
        onDelta: (delta) => {
          reply += delta;
          segment += delta;
          if (paintQueued) return;
          paintQueued = true;
          requestAnimationFrame(() => {
            paintQueued = false;
            seg.innerHTML = renderText(segment);
            gatedScroll();
          });
        },
        onTool: (tool) => {
          let c = cards.get(tool.id);
          if (!c) {
            const built = toolCard(tool);
            body.append(built.el);
            cards.set(tool.id, built);
          } else c.update(tool);
          if (tool.status !== "running") {
            segment = "";
            const next = document.createElement("div");
            body.append(next);
            seg.replaceWith(next);
            (seg as HTMLElement) = next as unknown as HTMLElement;
          }
          gatedScroll();
        },
      },
    );
    state.sessionId = result.sessionId;
    if (result.stopped) setStatus("stopped");
    else if (result.needsContinue) setStatus("needs continue — Resume");
    else setStatus("");
  } catch (e) {
    seg.innerHTML = renderText(`error: ${String(e)}`);
    toast(String(e), "error");
    setStatus("error");
  } finally {
    view.removeEventListener("scroll", onScroll);
    state.busy = false;
    document.querySelector("#sendBtn")!.toggleAttribute("disabled", false);
    (document.querySelector("#pauseBtn") as HTMLElement).style.display = "none";
    scrollToBottom();
  }
}
