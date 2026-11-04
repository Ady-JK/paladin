// types.ts holds the shared frontend types.
// It keeps every feature module speaking the same shapes.

export type Turn = {
  role: "user" | "agent" | "error";
  text: string;
  pending?: boolean;
};

export type CommandEntry = {
  name: string;
  description: string;
  argumentHint?: string;
  mode?: string;
  source: string;
  path?: string;
};

export type MemoryTarget = "user" | "global" | "project";

export type MemoryView = {
  target: MemoryTarget;
  cwd?: string;
  path: string;
  entries: string[];
  used: number;
  limit: number;
  /** False when memory.json switches this scope off: shown, not writable. */
  enabled: boolean;
};

/** Whether a session's frozen memory snapshot is behind the stores on disk. */
export type MemorySnapshotState = {
  frozen: boolean;
  stale: boolean;
  changed: string[];
};

/** Effective <data>/config/memory.json. */
export type MemoryConfigView = {
  limits: Record<MemoryTarget, number>;
  disabled: string[];
  provider: string;
  reviewTurns: number;
};

export type MemoryProject = {
  cwd: string;
  key: string;
  entries: number;
  used: number;
  limit: number;
};

export type SessionInfo = {
  sessionId: string;
  cwd: string;
  createdAt: number;
  provider: string;
  model: string;
  messageCount: number;
  title: string;
  worktree?: WorktreeInfo;
  // subagent is set on a subagent's child session.
  subagent?: SubagentLink;
  // children lists the subagent sessions this session spawned.
  children?: string[];
  // damaged says why the host could not read this session.
  damaged?: string;
};

export type AgentStatus = "queued" | "running" | "completed" | "failed" | "interrupted";

// SubagentLink ties a child session to the parent that spawned it.
export type SubagentLink = {
  parentId: string;
  spawnCallId?: string;
  type: string;
  name: string;
  task?: string;
  forkTurns?: string;
  status: AgentStatus;
  error?: string;
  // completionId names the latest result; see PROTOCOL.md Subagents.
  completionId?: string;
  updatedAt: number;
};

// AgentInfo is listAgents/interruptAgent and the agents/changed payload.
export type AgentInfo = {
  agentId: string;
  parentSessionId: string;
  name: string;
  type: string;
  task?: string;
  forkTurns?: string;
  status: AgentStatus;
  result?: string;
  error?: string;
  // completionId names the latest result; see PROTOCOL.md Subagents.
  completionId?: string;
  updatedAt: number;
};

// WorktreeInfo links a session to its managed git worktree. origin is
// the project folder it branched from.
export type WorktreeInfo = {
  repoRoot: string;
  path: string;
  branch: string;
  baseBranch?: string;
  origin: string;
};

export type RevertPreview = {
  index: number;
  turns: number;
  files: string[];
  deleted: string[];
  skipped: string[];
  snapshots: boolean;
};

export type ChangedFile = { path: string; status: "created" | "modified" | "deleted" };
export type SessionChanges = {
  available: boolean;
  turns: { turn: number; files: ChangedFile[]; callIds: string[] }[];
  files: ChangedFile[];
};
export type SessionDiff = { path: string; patch: string; truncated: boolean };

export type RevertResult = {
  index: number;
  message: string;
  restored: string[];
  deleted: string[];
  skipped: string[];
  failed?: string[];
  snapshots: boolean;
};

export type GitInfo = {
  repo: boolean;
  root?: string;
  branch?: string;
  hasCommits: boolean;
};

export type WorktreeStatus = {
  exists: boolean;
  changed: number;
  ahead: number;
  branch: string;
  baseBranch?: string;
  into: string;
  path: string;
  repoRoot: string;
};

export type MergeResult = {
  merged: boolean;
  nothingToMerge: boolean;
  commit?: string;
  conflicts: string[];
  into?: string;
};

export type OpenedSession = {
  sessionId: string;
  cwd: string;
  provider: string;
  model: string;
  worktree?: WorktreeInfo;
  subagent?: SubagentLink;
};

// UsageModel is one model's slice of a session's usage. FillPercent is
// the model's last call input over its window; absent for unknown
// windows. LastInputTokens is absent on fixtures that predate it.
type UsageModel = {
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
  // costEstimated marks spend the gateway did not charge (a subscription
  // or free plan): cost is 0 and listCost is what the tokens would have
  // cost. Show `estimatedSpend` from features/usage.ts, not cost.
  costEstimated?: boolean;
  fillPercent?: number;
};

// FileEntry is one `@` completion row from `fileComplete`: Path is
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

// SkillEntry is one `listSkills` row: name, description, source
// (global/workspace) and defining path.
export type SkillEntry = {
  name: string;
  description: string;
  source: string;
  path: string;
};

// SessionUsage is the client-facing usage view from `sessionUsage`.
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
  // costEstimated marks spend the gateway did not charge (a subscription
  // or free plan): cost is 0 and listCost is what the tokens would have
  // cost. Show `estimatedSpend` from features/usage.ts, not cost.
  costEstimated?: boolean;
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
  // costEstimated marks spend the gateway did not charge (a subscription
  // or free plan): cost is 0 and listCost is what the tokens would have
  // cost. Show `estimatedSpend` from features/usage.ts, not cost.
  costEstimated?: boolean;
};

// OverallSessionEntry is one session's slice of the fleet-wide rollup.
type OverallSessionEntry = {
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
  // costEstimated marks spend the gateway did not charge (a subscription
  // or free plan): cost is 0 and listCost is what the tokens would have
  // cost. Show `estimatedSpend` from features/usage.ts, not cost.
  costEstimated?: boolean;
};

// OverallUsage is the fleet-wide rollup from `overallUsage`.
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
  // costEstimated marks spend the gateway did not charge (a subscription
  // or free plan): cost is 0 and listCost is what the tokens would have
  // cost. Show `estimatedSpend` from features/usage.ts, not cost.
  costEstimated?: boolean;
  updatedAt: number;
  models?: UsageModel[];
  sessions?: OverallSessionEntry[];
  daily?: DailyPoint[];
};

export type MessageInfo = {
  role: string;
  content: string;
  createdAt: number;
  name?: string;
  arguments?: string;
  status?: string;
  // kind tags special rows: "compaction" is a context-compaction
  // divider whose content is the summary; "thinking" is the model's
  // thinking before the assistant message that follows.
  kind?: string;
  // index is a user row's transcript position, the handle for undo.
  index?: number;
  image?: { path: string; mimeType: string; size: number; sha256: string };
  attachments?: {
    id: string;
    name: string;
    path: string;
    size: number;
    mimeType: string;
    image?: { path: string; mimeType: string; size: number; sha256: string };
  }[];
  // stats is a user row's turn cost, so a reopened session shows the
  // same summary the live turn did.
  stats?: TurnStats;
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
// summary is the model-written summary on a done phase, so the UI can
// show what was kept without re-reading the transcript.
export type CompactionInfo = {
  phase: "start" | "done" | "failed";
  reason: string;
  tokensBefore: number;
  tokensAfter?: number;
  error?: string;
  summary?: string;
};

export type ToolInfo = {
  id: string;
  name: string;
  arguments: string;
  result: string;
  status: "running" | "ok" | "failed";
};

// RefreshResult is one provider's catalog refresh outcome.
export type RefreshResult = {
  name: string;
  count: number;
  models: string[] | null;
  error?: string;
};

export type ProviderInfo = {
  name: string;
  apiType: string;
  apiUrl: string;
  model: string;
  models: string[];
  default: boolean;
  hasKey?: boolean;
  // meta is what the provider's own model listing published about each
  // model id in models; models it said nothing about are absent.
  meta?: Record<string, ModelMeta>;
};

// ModelMeta is a provider's published metadata for one model: the wire
// dialect serving it, its limits, the thinking levels it accepts and its
// prices in USD per million tokens.
export type ModelMeta = {
  contextWindow?: number;
  maxOutput?: number;
  /** "chat", "responses", "anthropic" or "google". */
  protocol?: string;
  /** Weakest first; empty with thinkingKnown means "none to pick". */
  thinkingLevels?: string[];
  thinkingKnown?: boolean;
  priceInput?: number;
  priceOutput?: number;
  priceCacheRead?: number;
  priceCacheWrite?: number;
};

type FsDirectory = {
  name: string;
  path: string;
};

type FsFile = {
  name: string;
  path: string;
};

export type FsList = {
  currentPath: string;
  parentPath: string;
  directories: FsDirectory[];
  files: FsFile[];
};

export type GroupedLab = {
  display: string;
  models: { provider: string; id: string }[];
};

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
  // multiple lets the user pick any number of the options.
  multiple?: boolean;
};

// TodoEntry is one row of the session's activity list from
// `sessionTodos`: what to do plus where it stands (not_started,
// in_progress, finished, cancelled).
export type TodoEntry = {
  content: string;
  status: string;
};

export type SideView = "agent" | "chat";

export type ChatEntry = {
  id: string;
  title: string;
  url: string;
};

// TurnState is where a turn sits in the sidecar's turn hub.
type TurnState = "queued" | "running" | "done";

// TurnStats is what a turn cost: wall time and generated tokens.
export type TurnStats = { durationMs: number; tokens: number };

// TurnInfo describes one submitted message (see PROTOCOL.md).
export type TurnInfo = {
  turnId: string;
  sessionId: string;
  cwd: string;
  message: string;
  display?: string;
  skill?: string;
  command?: string;
  mode?: string;
  compact?: boolean;
  resume?: boolean;
  // parentSessionId is set on a subagent's turn.
  parentSessionId?: string;
  state: TurnState;
  persisted?: boolean;
  reply?: string;
  stopped?: boolean;
  cancelled?: boolean;
  permissions?: string;
  awaitingApproval?: boolean;
  error?: string;
  queuedAt?: string;
  startedAt?: string;
  endedAt?: string;
  // stats is the turn's cost, for the summary line under it.
  stats?: TurnStats;
};

// TurnEvent is any chat/* or turn/* line, for any session.
export type TurnEvent = {
  event: string;
  // mcp/status only (it has no sessionId).
  server?: MCPServerStatus;
  // tools/pending only: new tools the session holds back.
  pending?: string[];
  seq: number;
  sessionId: string;
  turnId: string;
  delta?: string;
  // chat/reasoning: a piece of the model's thinking.
  reasoning?: string;
  // chat/thinkingDropped: the thinking level the provider rejected.
  thinkingDropped?: string;
  tool?: ToolInfo;
  retry?: RetryInfo;
  compaction?: CompactionInfo;
  approval?: Approval;
  steer?: SteerInfo;
  budget?: BudgetNotice;
  hook?: HookNote;
  turn?: TurnInfo;
  // agents/changed only (it has no sessionId).
  parentSessionId?: string;
  agent?: AgentInfo;
};

// HookNote is chat/hook: a user hook ran and did something worth showing.
type HookOutcome = "blocked" | "context" | "error" | "stopped" | "message";
export type HookNote = { event: string; command: string; outcome: HookOutcome; message?: string };

// Hooks settings (hooksList and friends).
export type HookHandler = {
  key: string;
  event: string;
  matcher?: string;
  command: string;
  timeout?: number;
  statusMessage?: string;
  enabled: boolean;
};
export type HookScope = "global" | "folder";
export type HookSource = {
  path: string;
  scope: HookScope;
  format: "paladin" | "codex" | "claude";
  exists: boolean;
  editable: boolean;
  trusted: boolean;
  error?: string;
  warnings?: string[];
  text?: string;
  // fingerprint identifies the content listed; trusting needs it back.
  fingerprint?: string;
  handlers: HookHandler[] | null;
};
export type HooksOverview = { sources: HookSource[]; events: string[] };

// SideAnswer is a /btw answer (sideQuestion).
export type SideAnswer = { chatId: string; answer: string; model: string };

// SideChat is a saved /btw side chat; SideChatInfo is its list entry.
export type SideChatMessage = { role: "user" | "assistant"; content: string; model?: string; createdAt: number };
export type SideChat = { id: string; title: string; createdAt: number; updatedAt: number; messages: SideChatMessage[] };
export type SideChatInfo = { id: string; title: string; updatedAt: number; messageCount: number };

// SteerInfo is a message sent into a running turn (chat/steer).
export type SteerInfo = { message: string; display?: string };

// BudgetNotice is chat/budget: the tracked-cost limit was hit, it is
// not enforced because the provider reports no cost, or the usage
// record was damaged so tracked spend is only a lower bound.
export type BudgetNotice = {
  state: "reached" | "unknownCost" | "spendIncomplete";
  spent: number;
  limit: number;
  ended?: boolean;
};

// BudgetView is budgetGet/budgetSet: sessionUsd null inherits the
// default; effectiveUsd 0 means no limit.
export type BudgetView = {
  defaultUsd: number;
  sessionUsd: number | null;
  effectiveUsd: number;
  spentUsd: number;
  hasCost: boolean;
  calls: number;
};

// ThinkingView is thinkingGet/thinkingSet: the thinking levels the
// model accepts (weakest first, empty when it has none to adjust) and
// the stored one ("" is the provider default).
export type ThinkingView = {
  provider: string;
  model: string;
  levels: string[];
  level: string;
};

// Permission presets, rules and the approval card the gate sends.
export type PermissionPreset = "read-only" | "auto" | "full-access";
export type ApprovalDecision = "once" | "session" | "folder" | "global" | "deny";

export type PermissionRule = {
  tool: "shell" | "write" | "read" | "mcp" | "web" | "project" | "extension";
  pattern: string;
  action: "allow" | "deny";
};

export type Approval = {
  id: string;
  tool: string;
  kind: "shell" | "write" | "read" | "escalate" | "mcp" | "web" | "project" | "extension" | string;
  command?: string;
  path?: string;
  // MCP calls: the server, its own tool name and the JSON arguments.
  server?: string;
  mcpTool?: string;
  args?: string;
  // Web requests (web_fetch, browser open): the site and full URL.
  host?: string;
  url?: string;
  reasons?: string[];
  destructive?: boolean;
  choices?: ApprovalDecision[];
  remember?: PermissionRule[];
  state: "pending" | "approved" | "denied";
  decision?: ApprovalDecision | "interrupted";
};

export type PermissionsView = {
  global: PermissionRule[];
  folder: PermissionRule[];
  session: PermissionRule[];
  folders: { cwd: string; rules: PermissionRule[] }[];
  sandbox: { kind: string };
  presets: PermissionPreset[];
};

// HistoryPage is one window of a transcript. Start is the row ordinal
// of messages[0]; ordinals count from the start of the session, so an
// appended turn never shifts an older page.
export type HistoryPage = {
  messages: MessageInfo[];
  start: number;
  hasMore: boolean;
  total: number;
};

// SessionImages is the image-only view of a transcript: latestUser is
// the index of the last user row (-1 when none).
export type SessionImages = {
  latestUser: number;
  images: { index: number; role: string; path: string; name?: string }[];
};

// AttachSnapshot is a session's persisted history plus whatever is
// live: the running turn (if not yet persisted) with its events so
// far, and the turns queued behind it. Events with seq > seq follow.
export type AttachSnapshot = {
  history: HistoryPage;
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

// RunningJob is one shell command the agent still has running.
export type RunningJob = {
  id: string;
  sessionId: string;
  workspace?: string;
  command: string;
  startedAt: string;
  elapsedMs: number;
  status: string;
  auto?: boolean;
};

// MCP servers, as mcpList and mcp/status report them.
export type MCPScope = "global" | "folder";
export type MCPState = "disabled" | "untrusted" | "stopped" | "starting" | "ready" | "failed" | "removed";

type MCPToolStatus = {
  name: string;
  exposed: string;
  description?: string;
  readOnly: boolean;
  destructive: boolean;
};

export type MCPServerStatus = {
  name: string;
  scope: MCPScope;
  folder?: string;
  transport: string;
  state: MCPState;
  error?: string;
  shadowed?: boolean;
  tools: MCPToolStatus[];
  server?: { name: string; version?: string } | null;
  stderr?: string;
  restarts: number;
};

export type MCPOverview = {
  servers: MCPServerStatus[];
  globalPath: string;
  folderPath?: string;
  hasFolderConfig: boolean;
  trusted: boolean;
  // folderFingerprint identifies the folder mcp.json listed; trusting
  // needs it back, so only what was shown gets trusted.
  folderFingerprint?: string;
  errors?: Partial<Record<MCPScope, string>>;
};

export type MCPConfigFile = { path: string; text: string; exists: boolean };

// MCPServerConfig is one mcp.json entry; MCPNamedServer pairs it with
// the name mcpParse derived (or the user typed).
type MCPServerConfig = {
  type?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  cwd?: string;
  url?: string;
  headers?: Record<string, string>;
  enabled?: boolean;
};

export type MCPNamedServer = { name: string; config: MCPServerConfig };

// ToolSwitch is one tool as toolsList reports it: the global and session
// switches (unset when absent) and the result, session beating global.
export type ToolSwitch = {
  name: string;
  description?: string;
  source: "builtin" | "mcp" | "extension";
  server?: string;
  tool?: string;
  readOnly?: boolean;
  destructive?: boolean;
  global?: boolean;
  session?: boolean;
  enabled: boolean;
  pending?: boolean;
};

// ToolBehavior is how the built-in tools act, app-wide (settings.json).
export type ToolBehavior = {
  formatOnWrite: boolean;
  verifyNudge: boolean;
  lspDiagnostics: boolean;
  webPrivateNetwork?: boolean;
};
// LanguageServer is one running (or recently failed) language server.
export type LanguageServer = {
  id: string;
  root: string;
  state: "starting" | "ready" | "failed";
  pid?: number;
  error?: string;
};
export type ToolsOverview = {
  tools: ToolSwitch[];
  pending: string[];
  extensionErrors?: string[];
  behavior?: ToolBehavior;
  languageServers?: LanguageServer[];
};

// Finding is one issue a /review recorded with report_findings.
export type Finding = {
  id: string;
  priority: "P0" | "P1" | "P2" | "P3";
  title: string;
  path: string;
  scope: "inside" | "outside";
  start_line: number;
  end_line: number;
  body: string;
  fix?: string;
  confidence: "high" | "medium" | "low";
};
export type FindingsReport = {
  verdict: "looks_good" | "merge_after_fixes" | "needs_rework";
  summary: string;
  findings: Finding[];
  note?: string;
};
// FileExcerpt is a window of a workspace file (fileExcerpt).
export type FileExcerpt = { path: string; rel: string; startLine: number; lines: string[]; truncated: boolean };

// CustomItem is one editable built-in (Settings → Customize).
export type CustomItem = {
  id: string;
  group: string;
  title: string;
  description: string;
  kind: "text" | "command" | "skill" | "json";
  vars?: string[];
  customized: boolean;
  enabled?: boolean;
};

// CustomState is one item with its current and default text.
export type CustomState = CustomItem & {
  value: string;
  default: string;
  path?: string;
  missing?: string[];
};
