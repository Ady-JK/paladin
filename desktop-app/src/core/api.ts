// api.ts wraps the Tauri backend commands.
// Single seam between the feature modules and the in-process agent-core.
// No daemon, no addr, no subscribe — everything runs in-process.

import { invoke } from "@tauri-apps/api/core";
import type {
  FsList,
  MessageInfo,
  ProviderInfo,
  SessionInfo,
} from "./types";

export function getWorkspace(): Promise<string> {
  return invoke<string>("get_workspace");
}

export function resolveWorkspace(cwd: string | null): Promise<string> {
  return invoke<string>("resolve_workspace", { cwd });
}

export function setWorkspace(path: string): Promise<string> {
  return invoke<string>("set_workspace", { path });
}

export function fsList(path: string | null): Promise<FsList> {
  return invoke<FsList>("fs_list", { path });
}

export function fsHome(): Promise<string> {
  return invoke<string>("fs_home");
}

export function listProviders(): Promise<ProviderInfo[]> {
  return invoke<ProviderInfo[]>("list_providers");
}

export function refreshProviders(): Promise<boolean> {
  return invoke<boolean>("refresh_providers");
}

export function setDefaultProvider(
  name: string,
): Promise<ProviderInfo[]> {
  return invoke<ProviderInfo[]>("set_default_provider", { name });
}

export function addProvider(
  entry: { name: string; apiUrl: string; apiKey: string; apiType: string },
): Promise<ProviderInfo> {
  return invoke<ProviderInfo>("add_provider", { ...entry });
}

export function updateProvider(
  entry: { name: string; apiUrl: string; apiKey: string; apiType: string },
): Promise<ProviderInfo> {
  return invoke<ProviderInfo>("update_provider", { ...entry });
}

export function openSession(
  sessionId: string | null,
  cwd: string | null,
): Promise<{ sessionId: string; cwd: string; provider: string; model: string }> {
  return invoke("open_session", { sessionId, cwd });
}

export function listSessions(
  cwd: string | null,
  limit = 100,
): Promise<SessionInfo[]> {
  return invoke<SessionInfo[]>("list_sessions", { cwd, limit });
}

export function sessionHistory(
  sessionId: string,
  cwd: string | null,
): Promise<MessageInfo[]> {
  return invoke<MessageInfo[]>("session_history", { sessionId, cwd });
}

export function deleteSession(
  sessionId: string,
  cwd: string | null,
): Promise<boolean> {
  return invoke<boolean>("delete_session", { sessionId, cwd });
}

export function chatStream(args: {
  sessionId: string | null;
  message: string;
  resume: boolean;
  cwd: string;
  provider: string | null;
  model: string | null;
  systemPrompt: string | null;
}): Promise<{ sessionId: string; reply: string; stopped: boolean; needsContinue: boolean }> {
  return invoke("chat_stream", { args });
}

export function stopChat(
  sessionId: string | null,
  cwd: string | null,
): Promise<boolean> {
  return invoke<boolean>("stop_chat", { sessionId, cwd });
}

export function showExternalPage(
  url: string,
  rect: { x: number; y: number; width: number; height: number },
): Promise<unknown> {
  return invoke("show_external_page", { url, ...rect });
}

export function navigateExternalPage(url: string): Promise<unknown> {
  return invoke("navigate_external_page", { url });
}

export function hideExternalPage(): Promise<unknown> {
  return invoke("hide_external_page", {});
}

export function moveExternalPage(rect: {
  x: number;
  y: number;
  width: number;
  height: number;
}): Promise<unknown> {
  return invoke("move_external_page", { ...rect });
}

export function reloadExternalPage(): Promise<unknown> {
  return invoke("reload_external_page", {});
}

export function dropExternalPage(): Promise<unknown> {
  return invoke("drop_external_page", {});
}
