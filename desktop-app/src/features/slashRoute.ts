// slashRoute.ts decides what a submitted `/` line does: run a local
// action, or become a turn for the agent with extra submit fields.

import { inputEl, messagesEl, modelSelectBtn } from "../core/dom";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { addTurn, renderGreeting } from "./chatView";
import { clearLanded } from "./live";
import { setMode } from "./mode";
import { newSession } from "./sessions";
import { undoLast } from "./undo";
import { forkSessionById, renameSessionById } from "./sessionActions";
import { openSettings } from "./settings";
import { loadSkills, parseSkillLine } from "./skills";
import { openToolsMenu } from "./toolSwitches";
import { openUsageDashboard } from "./usage";
import { askSideQuestion } from "./btw";
import { openBudgetMenu, parseBudget, setSessionBudget } from "./budget";
import { loadPrompts, promptArgs, setComposer, slashHelp } from "./slashCommands";

// AgentRoute is a slash line that still goes to the agent, with the
// extra submit fields it needs.
export type AgentRoute = {
  message: string;
  display: string;
  command?: string;
  compact?: boolean;
  mode?: "build" | "plan";
  skill?: string;
};

function needSession(what: string): boolean {
  if (!state.sessionId) {
    toast(`Open a session to ${what}`, "error");
    return false;
  }
  return true;
}

async function forkCurrent(): Promise<void> {
  if (!needSession("fork it")) return;
  await forkSessionById(state.sessionId);
}

async function renameCurrent(title: string): Promise<void> {
  if (!needSession("rename it")) return;
  await renameSessionById(state.sessionId, title);
}

// routeSlash handles a `/` line. "local" means it ran here and must
// not reach the agent; "none" means it is not a slash line; an object
// is a turn for the agent with extra submit fields.
export async function routeSlash(message: string): Promise<"local" | "none" | AgentRoute> {
  const parsed = parseSlashLine(message);
  if (!parsed) {
    return "none";
  }
  const { trimmed, name, rest } = parsed;
  const clear = () => {
    inputEl.value = "";
  };
  switch (name) {
    case "skill":
      // chat.ts splits `/skill <name> [message]` and sends the name
      // separately. A bare `/skill` names nothing: hint instead.
      if (!parseSkillLine(message)) {
        setComposer("/skill ");
        toast("Usage: /skill <name> [message] — pick one from the list", "error");
        return "local";
      }
      return "none";
    case "new":
      newSession();
      inputEl.value = rest;
      toast(rest ? "New session started" : "New session");
      return "local";
    case "compact":
      if (!needSession("compact it")) return "local";
      clear();
      return { message: "", display: "/compact", compact: true };
    case "plan":
      setMode("plan");
      clear();
      if (!rest) {
        toast("Plan mode — the agent stays read-only");
        return "local";
      }
      return { message: rest, display: rest, mode: "plan" };
    case "build":
      setMode("build");
      clear();
      toast("Build mode");
      return "local";
    case "fork":
      clear();
      void forkCurrent();
      return "local";
    case "undo":
      clear();
      void undoLast();
      return "local";
    case "btw":
      clear();
      if (!rest) {
        setComposer("/btw ");
        toast("Usage: /btw <question>", "error");
        return "local";
      }
      if (!needSession("ask a side question")) return "local";
      void askSideQuestion(rest);
      return "local";
    case "rename":
      clear();
      if (!rest) {
        setComposer("/rename ");
        toast("Usage: /rename <title>", "error");
        return "local";
      }
      void renameCurrent(rest);
      return "local";
    case "memories":
    case "memory":
      clear();
      openSettings("memory");
      return "local";
    case "mcp":
      clear();
      openSettings("mcp");
      return "local";
    case "tools":
      clear();
      openToolsMenu();
      return "local";
    case "models":
      clear();
      modelSelectBtn.click();
      return "local";
    case "settings":
      clear();
      openSettings("workspace");
      return "local";
    case "config":
      clear();
      openSettings("providers");
      return "local";
    case "budget": {
      clear();
      if (!rest) {
        openBudgetMenu();
        return "local";
      }
      const v = parseBudget(rest);
      if (v === undefined) {
        setComposer("/budget ");
        toast("Usage: /budget <usd> | off | default", "error");
        return "local";
      }
      void setSessionBudget(v);
      return "local";
    }
    case "usage":
      clear();
      void openUsageDashboard();
      return "local";
    case "clear":
      clear();
      messagesEl.innerHTML = "";
      clearLanded(state.sessionId);
      renderGreeting();
      return "local";
    case "help":
      clear();
      await loadPrompts();
      addTurn("user", message.trim(), { revertable: false });
      addTurn("agent", slashHelp());
      return "local";
  }
  return routeSidecarCommand(name, rest, trimmed);
}

// parseSlashLine splits a `/name args` line. Arguments keep later
// lines: `/review\nfocus on auth` passes both.
function parseSlashLine(message: string): { trimmed: string; name: string; rest: string } | null {
  const trimmed = message.trimStart();
  if (!trimmed.startsWith("/")) {
    return null;
  }
  const firstLine = trimmed.split("\n", 1)[0];
  const space = firstLine.search(/\s/);
  const name = (space < 0 ? firstLine : firstLine.slice(0, space)).slice(1).toLowerCase();
  const rest = (space < 0 ? trimmed.slice(firstLine.length) : trimmed.slice(space)).trim();
  return { trimmed, name, rest };
}

// routeSidecarCommand resolves a name that is not built in: a prompt
// command or a skill from the sidecar. The prompt list refreshes once
// before the name is called unknown.
async function routeSidecarCommand(name: string, rest: string, trimmed: string): Promise<"local" | AgentRoute> {
  let prompt = (await loadPrompts()).find((c) => c.name.toLowerCase() === name);
  if (!prompt) {
    prompt = (await loadPrompts(true)).find((c) => c.name.toLowerCase() === name);
  }
  if (prompt) {
    if (promptArgs(prompt) === "required" && !rest) {
      setComposer(`/${prompt.name} `);
      toast(`Usage: /${prompt.name} ${prompt.argumentHint}`, "error");
      return "local";
    }
    inputEl.value = "";
    const route: AgentRoute = { message: rest, display: trimmed, command: prompt.name };
    if (state.mode === "plan") route.mode = "plan";
    return route;
  }
  const skill = (await loadSkills()).find((s) => s.name.toLowerCase() === name);
  if (skill) {
    inputEl.value = "";
    return { message: rest, display: rest || `/skill ${skill.name}`, skill: skill.name };
  }
  toast(`Unknown command /${name} — try /help`, "error");
  return "local";
}
