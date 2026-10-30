// slashCommands.ts is the `/` catalog shared by the palette (slash.ts)
// and the router (slashRoute.ts): built-in commands, the folder's
// prompt commands (cached per working folder) and the /help text.

import * as api from "../core/api";
import { inputEl } from "../core/dom";
import { state } from "../core/state";
import { type CommandEntry, type SkillEntry } from "../core/types";
import { type IconName } from "../icons";
import { skillsSnapshot } from "./skills";

export type Args = "none" | "optional" | "required";

type SlashCommand = {
  name: string;
  label: string;
  desc: string;
  hint: string;
  icon: IconName;
  args: Args;
  // chevron marks rows that open something (a picker, a pane).
  chevron?: boolean;
};

export const SLASH_COMMANDS: SlashCommand[] = [
  {
    name: "new",
    label: "New chat",
    desc: "Start a fresh session",
    hint: "/new [first message]",
    icon: "sl-new",
    args: "optional",
  },
  {
    name: "compact",
    label: "Compact",
    desc: "Summarize the conversation to free up context",
    hint: "/compact",
    icon: "sl-compact",
    args: "none",
  },
  {
    name: "plan",
    label: "Plan mode",
    desc: "Read-only: explore, then propose a plan",
    hint: "/plan [task]",
    icon: "sl-plan",
    args: "optional",
  },
  {
    name: "build",
    label: "Build mode",
    desc: "Let the agent edit files and run commands",
    hint: "/build",
    icon: "sl-build",
    args: "none",
  },
  {
    name: "undo",
    label: "Undo",
    desc: "Revert the last message: its file changes and the reply",
    hint: "/undo",
    icon: "tl-undo",
    args: "none",
  },
  {
    name: "fork",
    label: "Fork",
    desc: "Branch this conversation into a new session",
    hint: "/fork",
    icon: "sl-fork",
    args: "none",
  },
  {
    name: "btw",
    label: "By the way",
    desc: "Ask a side question; the answer is not added to the chat",
    hint: "/btw [question]",
    icon: "comments",
    args: "optional",
  },
  {
    name: "rename",
    label: "Rename",
    desc: "Rename this session",
    hint: "/rename <title>",
    icon: "tl-pencil",
    args: "required",
  },
  {
    name: "memories",
    label: "Memories",
    desc: "View and edit what Paladin remembers",
    hint: "/memories",
    icon: "sl-memory",
    args: "none",
    chevron: true,
  },
  {
    name: "skill",
    label: "Skill",
    desc: "Attach a skill to this turn",
    hint: "/skill <name> [message]",
    icon: "sl-skill",
    args: "required",
    chevron: true,
  },
  {
    name: "mcp",
    label: "MCP servers",
    desc: "Server status, tools and mcp.json",
    hint: "/mcp",
    icon: "sl-plug",
    args: "none",
    chevron: true,
  },
  {
    name: "tools",
    label: "Tools",
    desc: "Switch tools on or off for this session",
    hint: "/tools",
    icon: "tl-tools",
    args: "none",
    chevron: true,
  },
  {
    name: "models",
    label: "Model",
    desc: "Switch the provider and model",
    hint: "/models",
    icon: "sl-model",
    args: "none",
    chevron: true,
  },
  {
    name: "budget",
    label: "Budget",
    desc: "Set this session's spend limit",
    hint: "/budget [usd|off|default]",
    icon: "sl-budget",
    args: "optional",
  },
  {
    name: "usage",
    label: "Usage",
    desc: "Tokens, cost and context for this session",
    hint: "/usage",
    icon: "sl-usage",
    args: "none",
    chevron: true,
  },
  {
    name: "settings",
    label: "Settings",
    desc: "Open settings",
    hint: "/settings",
    icon: "rl-gear",
    args: "none",
    chevron: true,
  },
  {
    name: "config",
    label: "Providers",
    desc: "Configure providers and API keys",
    hint: "/config",
    icon: "sl-sliders",
    args: "none",
    chevron: true,
  },
  {
    name: "clear",
    label: "Clear view",
    desc: "Clear the conversation view (history stays)",
    hint: "/clear",
    icon: "sl-clear",
    args: "none",
  },
  { name: "help", label: "Help", desc: "List every command", hint: "/help", icon: "sl-help", args: "none" },
];

const PROMPT_LABELS: Record<string, string> = { review: "Code review", init: "Init AGENTS.md" };
export const PROMPT_ICONS: Record<string, IconName> = { review: "sl-review", init: "sl-init" };

let prompts: CommandEntry[] | null = null;
let promptsCwd = "";

// cachedPrompts is the last prompt list loaded, without fetching.
export function cachedPrompts(): CommandEntry[] {
  return prompts ?? [];
}

// loadPrompts fetches the prompt commands for the working folder once.
export async function loadPrompts(force = false): Promise<CommandEntry[]> {
  if (!force && prompts && promptsCwd === state.cwd) {
    return prompts;
  }
  try {
    prompts = await api.listCommands(state.cwd || null);
    promptsCwd = state.cwd;
  } catch {
    prompts = prompts ?? [];
  }
  return prompts;
}

export function promptLabel(name: string): string {
  return PROMPT_LABELS[name] ?? name.replace(/[-_.]+/g, " ").replace(/^./, (c) => c.toUpperCase());
}

export function promptArgs(c: CommandEntry): Args {
  const hint = (c.argumentHint || "").trim();
  if (!hint) return "optional";
  return hint.startsWith("<") ? "required" : "optional";
}

// setComposer replaces the composer text and puts the caret at the end.
export function setComposer(text: string): void {
  inputEl.value = text;
  inputEl.selectionStart = inputEl.value.length;
  inputEl.selectionEnd = inputEl.value.length;
  inputEl.dispatchEvent(new Event("input"));
  inputEl.focus();
}

// slashHelp lists every command, prompt and skill.
export function slashHelp(): string {
  const lines = ["Commands:", ...SLASH_COMMANDS.map((c) => `  ${c.hint} — ${c.desc}`)];
  if (prompts?.length) {
    lines.push(
      "",
      "Prompts:",
      ...prompts.map((c) => `  /${c.name}${c.argumentHint ? ` ${c.argumentHint}` : ""} — ${c.description}`),
    );
  }
  const skills = skillsSnapshot();
  if (skills.length) {
    lines.push(
      "",
      "Skills (/skill <name> or /<name>):",
      ...skills.map((s: SkillEntry) => `  ${s.name} — ${s.description || s.source}`),
    );
  }
  return lines.join("\n");
}
