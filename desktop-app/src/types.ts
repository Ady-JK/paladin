export interface ProviderInfo {
  name: string;
  apiType: string;
  apiUrl: string;
  model: string;
  models: string[];
  default: boolean;
  hasKey: boolean;
}

export interface SessionInfo {
  sessionId: string;
  cwd: string;
  createdAt: number;
  provider: string;
  model: string;
  messageCount: number;
  title: string;
}

export interface HistoryMessage {
  role: string;
  content: string;
  createdAt: number;
  toolCallId?: string;
  name?: string;
  arguments?: string;
  status?: string;
}

export interface ToolActivity {
  id: string;
  name: string;
  arguments: string;
  result: string;
  status: string;
}

export interface ChatResult {
  sessionId: string;
  reply: string;
  stopped: boolean;
  needsContinue: boolean;
}

export interface ChatEntry {
  id: string;
  title: string;
  url: string;
  builtin: boolean;
}

export type SideView = "agent" | "chat";
