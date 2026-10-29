// types.ts holds the shared frontend types.
// It keeps every feature module speaking the same shapes.

// Turn is a type in this module.
// It supports the desktop app frontend.
export type Turn = {
  role: "user" | "agent" | "error";
  text: string;
  pending?: boolean;
};

// SessionInfo is a type in this module.
// It supports the desktop app frontend.
export type SessionInfo = {
  sessionId: string;
  cwd: string;
  createdAt: number;
  provider: string;
  model: string;
  messageCount: number;
  title: string;
};

// UsageModel is one model's slice of a session's usage. FillPercent is
// the model's last call input over its window; absent for unknown
// windows. LastInputTokens is absent on fixtures that predate it.
export type UsageModel = {
  model: string;
  contextWindow: number;
  contextKnown: boolean;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  lastInputTokens?: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
  fillPercent?: number;
};

// FileEntry is one `@` completion row from `file_complete`: Path is
// the Linux-style chat path (`src/foo.ts`, `../sib/file`, `src/sub/`
// for folders), ResolvedPath the OS-specific absolute path the
// composer inserts into the message.
export type FileEntry = {
  path: string;
  display: string;
  resolvedPath: string;
  outside: boolean;
  isDir: boolean;
};

// SkillEntry is one `list_skills` row: name, description, source
// (global/workspace) and defining path.
export type SkillEntry = {
  name: string;
  description: string;
  source: string;
  path: string;
};

// SessionUsage is the client-facing usage view from `session_usage`.
// FillPercent is the last call's input tokens over the context window;
// absent when the model's window is unknown. LastInputTokens is absent
// on fixtures that predate it.
export type SessionUsage = {
  sessionId: string;
  model: string;
  contextWindow: number;
  contextKnown: boolean;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  lastInputTokens?: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
  fillPercent?: number;
  models?: UsageModel[];
  daily?: DailyPoint[];
};

// DailyPoint is one UTC calendar day's billed activity.
export type DailyPoint = {
  date: string;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
};

// OverallSessionEntry is one session's slice of the fleet-wide rollup.
export type OverallSessionEntry = {
  sessionId: string;
  title?: string;
  model: string;
  createdAt: number;
  updatedAt: number;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
};

// OverallUsage is the fleet-wide rollup from `overall_usage`.
export type OverallUsage = {
  sessionCount: number;
  sessionsWithUsage: number;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
  updatedAt: number;
  models?: UsageModel[];
  sessions?: OverallSessionEntry[];
  daily?: DailyPoint[];
};

// MessageInfo is a type in this module.
// It supports the desktop app frontend.
export type MessageInfo = {
  role: string;
  content: string;
  createdAt: number;
  name?: string;
  arguments?: string;
  status?: string;
  // kind tags special rows: "compaction" is a context-compaction
  // divider whose content is the summary.
  kind?: string;
};

// RetryInfo is a chat/retry event: a failed round runs again after
// delayMs (attempt of max).
export type RetryInfo = {
  attempt: number;
  max: number;
  delayMs: number;
  reason: string;
};

// CompactionInfo is a chat/compact event. reason is "auto" (near the
// context window) or "overflow" (provider rejected the prompt).
export type CompactionInfo = {
  phase: "start" | "done" | "failed";
  reason: string;
  tokensBefore: number;
  tokensAfter?: number;
  error?: string;
};

// ToolInfo is a type in this module.
// It supports the desktop app frontend.
export type ToolInfo = {
  id: string;
  name: string;
  arguments: string;
  result: string;
  status: "running" | "ok" | "failed";
};

// SessionEvent is a type in this module.
// It supports the desktop app frontend.
export type SessionEvent = {
  kind: string;
  sessionId: string;
  cwd: string;
  messageCount: number;
  // Events dropped for a slow client before this one; nonzero means the
  // stream gapped and the view should refetch.
  missed?: number;
};

// RefreshResult is one provider's catalog refresh outcome.
export type RefreshResult = {
  name: string;
  count: number;
  models: string[] | null;
  error?: string;
};

// ProviderInfo is a type in this module.
// It supports the desktop app frontend.
export type ProviderInfo = {
  name: string;
  apiType: string;
  apiUrl: string;
  model: string;
  models: string[];
  default: boolean;
  hasKey?: boolean;
};

// FsDirectory is a type in this module.
// It supports the desktop app frontend.
export type FsDirectory = {
  name: string;
  path: string;
};

// FsFile is a type in this module.
// It supports the desktop app frontend.
export type FsFile = {
  name: string;
  path: string;
};

// FsList is a type in this module.
// It supports the desktop app frontend.
export type FsList = {
  currentPath: string;
  parentPath: string;
  directories: FsDirectory[];
  files: FsFile[];
};

// GroupedLab is a type in this module.
// It supports the desktop app frontend.
export type GroupedLab = {
  display: string;
  models: { provider: string; id: string }[];
};

// GroupedProvider is a type in this module.
// It supports the desktop app frontend.
export type GroupedProvider = {
  display: string;
  infos: ProviderInfo[];
  isDefault: boolean;
  labs: GroupedLab[];
};

// DiffRow is one rendered line of a two-sided text diff.
// OldNo/newNo are hunk-relative line numbers; one side is null on add/del rows.
export type DiffRow = {
  kind: "context" | "del" | "add";
  oldNo: number | null;
  newNo: number | null;
  text: string;
};

// MatchSpan is one applied replacement span from an edit result.
// Start and end are 1-based line numbers in the pre-edit file.
export type MatchSpan = {
  start: number;
  end: number;
};

// ToolView is a type in this module.
// It supports the desktop app frontend.
export type ToolView = {
  el: HTMLElement;
  update: (tool: ToolInfo) => void;
};

// AskQuestion is one model-posed question in an ask tool call: the
// question plus up to three suggested options. The wizard always offers
// a custom typed answer besides the options.
export type AskQuestion = {
  question: string;
  options: string[];
};

// AskAnswer pairs one asked question with the user's answer, in the
// order the questions were asked.
export type AskAnswer = {
  question: string;
  answer: string;
};

// TodoEntry is one row of the session's activity list from
// `session_todos`: what to do plus where it stands (not_started,
// in_progress, finished, cancelled).
export type TodoEntry = {
  content: string;
  status: string;
};

// SideView is a type in this module.
// It supports the desktop app frontend.
export type SideView = "agent" | "chat";

// ChatEntry is a type in this module.
// It supports the desktop app frontend.
export type ChatEntry = {
  id: string;
  title: string;
  url: string;
};

// TurnState is where a turn sits in the sidecar's turn hub.
export type TurnState = "queued" | "running" | "done";

// TurnInfo describes one submitted message (see PROTOCOL.md).
export type TurnInfo = {
  turnId: string;
  sessionId: string;
  cwd: string;
  message: string;
  display?: string;
  skill?: string;
  resume?: boolean;
  state: TurnState;
  persisted?: boolean;
  reply?: string;
  stopped?: boolean;
  needsContinue?: boolean;
  cancelled?: boolean;
  error?: string;
  queuedAt?: string;
  startedAt?: string;
  endedAt?: string;
};

// TurnEvent is any chat/* or turn/* line, for any session.
export type TurnEvent = {
  event: string;
  seq: number;
  sessionId: string;
  turnId: string;
  streamId?: string;
  delta?: string;
  tool?: ToolInfo;
  retry?: RetryInfo;
  compaction?: CompactionInfo;
  turn?: TurnInfo;
};

// AttachSnapshot is a session's persisted history plus whatever is
// live: the running turn (if not yet persisted) with its events so
// far, and the turns queued behind it. Events with seq > seq follow.
export type AttachSnapshot = {
  history: MessageInfo[];
  turn?: TurnInfo | null;
  events?: TurnEvent[] | null;
  queued?: TurnInfo[] | null;
  seq: number;
};

// ResourceUsage is CPU (% of one core), resident memory in bytes and
// the number of processes summed.
export type ResourceUsage = { cpu: number; rss: number; procs: number };

// ResourceSnapshot is the core's `resources` reply.
export type ResourceSnapshot = {
  at: number;
  supported: boolean;
  self: ResourceUsage;
  sessions: Record<string, ResourceUsage>;
};
