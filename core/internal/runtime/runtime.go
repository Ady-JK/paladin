// Package runtime owns the AgentRuntime: sessions, providers, the
// agent turn entry point, and the model catalog refresh.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/memory"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/Ady-JK/paladin/core/internal/worktree"
)

type Settings struct {
	// MaxConcurrentTurns caps turns running at once across sessions;
	// extra turns wait in the hub queue. 0 means the default.
	MaxConcurrentTurns uint32 `json:"maxConcurrentTurns,omitempty"`
	// DefaultBudgetUsd is the spend limit for sessions without their
	// own; 0 means none.
	DefaultBudgetUsd float64 `json:"defaultBudgetUsd,omitempty"`
	// ThinkingLevels is each model's reasoning effort, by provider then
	// model; absent means the provider default.
	ThinkingLevels map[string]map[string]string `json:"thinkingLevels,omitempty"`
}

type AgentRuntime struct {
	dataDir       string
	config        config.Config
	settingsMu    sync.Mutex
	store         *config.Store
	transcripts   *transcript.Store
	catalog       *tools.Catalog
	tools         *tools.Registry
	shell         *tools.ShellTool
	ask           *tools.AskTool
	memory        *memory.Bank
	perms         *permission.Store
	broker        *permission.Broker
	agents        *agent.Registry
	hub           *Hub
	snaps         *snapshot.Store
	worktrees     *worktree.Manager
	undoMu        sync.Mutex
	jobs          *jobBook
	providersMu   sync.Mutex
	refreshing    atomic.Bool
	refreshWG     sync.WaitGroup
	fetchMu       sync.RWMutex
	fetchModels   ModelFetcher
	providerCache map[string]inference.Provider
	// thinkingCache holds providers with a thinking level applied, so a
	// level the provider rejected stays dropped across turns.
	thinkingCache map[string]inference.Provider
	cacheMu       sync.Mutex
	mcp           *mcp.Manager
	mcpSink       atomic.Pointer[mcpSink]
	toolsMu       sync.Mutex
	toolsSink     atomic.Pointer[toolsSink]
	hooks         *hooks.Manager
	// started holds sessions whose SessionStart hooks ran in this process.
	started   map[string]bool
	startedMu sync.Mutex
	// live is each running turn's latest model input, for side questions.
	live   map[string][]inference.LlmMessage
	liveMu sync.Mutex
	// sideCtx ends in-flight side questions at shutdown.
	sideCtx    context.Context
	sideCancel context.CancelFunc
}

// New builds the production runtime, backed by the OS keychain. The
// PALADIN_KEYSTORE=memory override keeps sidecar tests hermetic on
// machines that have real provider keys in their desktop keyring.
func New(dataDir string, cfg config.Config) *AgentRuntime {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PALADIN_KEYSTORE")), "memory") {
		return WithKeyStore(dataDir, cfg, config.NewMemoryKeyStore())
	}
	return WithKeyStore(dataDir, cfg, config.OsKeyStore{})
}

func WithKeyStore(dataDir string, cfg config.Config, keys config.KeyStore) *AgentRuntime {
	store := config.NewStore(dataDir, keys)
	catalog := tools.BuiltinCatalog()
	registry := catalog.Instantiate()
	shell, _ := registry.Get("shell")
	shellTool, _ := shell.(*tools.ShellTool)
	ask, _ := registry.Get("ask")
	askTool, _ := ask.(*tools.AskTool)
	agents := agent.NewRegistry()
	_ = agents.Register(agent.DefaultTemplate())
	r := &AgentRuntime{
		dataDir:       dataDir,
		config:        cfg,
		store:         store,
		transcripts:   transcript.NewStore(filepath.Join(dataDir, "sessions")),
		catalog:       catalog,
		tools:         registry,
		shell:         shellTool,
		ask:           askTool,
		agents:        agents,
		perms:         permission.NewStore(dataDir),
		broker:        permission.NewBroker(),
		snaps:         snapshotStore(dataDir),
		worktrees:     worktree.New(filepath.Join(dataDir, "worktrees")),
		providerCache: map[string]inference.Provider{},
		thinkingCache: map[string]inference.Provider{},
		fetchModels:   networkModelFetcher,
		hooks:         hooks.NewManager(dataDir),
		started:       map[string]bool{},
		live:          map[string][]inference.LlmMessage{},
	}
	r.sideCtx, r.sideCancel = context.WithCancel(context.Background())
	r.jobs = newJobBook(r)
	if shellTool != nil {
		shellTool.Observe(r.jobs)
	}
	r.mcp = newMCPManager(dataDir, &r.mcpSink)
	r.registerKnowledgeTools()
	r.hub = NewHub(r.runTurn, r.maxConcurrentTurns)
	return r
}

func (r *AgentRuntime) settingsPath() string {
	return filepath.Join(r.dataDir, "config", "settings.json")
}

func (r *AgentRuntime) loadSettings() Settings {
	data, err := os.ReadFile(r.settingsPath())
	if err != nil {
		return Settings{}
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}
	}
	return s
}

// runTurn is the hub's RunFunc: it builds the default agent template
// (provider, session, history, tools, prompt) and runs one turn under
// parent. The hub has already validated req and owns tx.
func (r *AgentRuntime) runTurn(parent context.Context, req agent.Request, gate func(func()), tx chan<- agent.ChatChunk, done <-chan struct{}) (agent.ChatOutcome, error) {
	preset, err := permission.ParsePreset(req.Permissions)
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	template, ok := r.agents.Default()
	if !ok {
		return agent.ChatOutcome{}, fmt.Errorf("no agent template registered")
	}
	// A finished plan belongs to the finished work: the user's next
	// message drops it so the panel goes empty until the agent plans
	// the new work. Best-effort — a stale file never fails a turn.
	workspace := resolveCwd(r, req.Cwd)
	project := workspace
	var steps agent.StepRecorder
	var extraWritable []string
	var sessionStart string
	var hookEngine *hooks.Engine
	if sess, lerr := r.transcripts.Load(req.Cwd, req.SessionID); lerr == nil {
		// A pending undo becomes permanent once the user moves on.
		r.undoMu.Lock()
		cerr := r.commitRevert(&sess)
		r.undoMu.Unlock()
		if cerr != nil {
			return agent.ChatOutcome{}, fmt.Errorf("commit revert: %w", cerr)
		}
		r.dropFinishedTodos(sess, req.Resume || req.Compact)
		if sess.Meta.Cwd != "" {
			workspace = sess.Meta.Cwd
		}
		project = workspace
		if p := sess.Meta.ProjectCwd(); p != "" {
			project = p
		}
		hookEngine = r.hooks.Engine(project)
		if !req.Resume && !req.Compact {
			sessionStart = r.sessionStartSource(hookEngine, sess)
		}
		if rec := r.stepRecorderFor(sess, workspace); rec != nil {
			steps = rec
		}
		if sess.Meta.Worktree != nil {
			// Commits from a worktree write to the repository's shared git dir.
			if dir := worktree.CommonDir(ctx, workspace); dir != "" {
				extraWritable = append(extraWritable, dir)
			}
		}
	}
	registry := r.turnTools(req.Cwd, req.SessionID, r.tools.With(r.mcpTools(ctx, project)...))
	req.Budget = r.turnBudget(req.Cwd, req.SessionID)
	built, err := template.Build(agent.Deps{
		Sessions:  runtimeSessions{r},
		Providers: runtimeProviders{r},
		Prompts:   runtimePrompts{r},
		Skills:    runtimeSkills{r},
		Sections:  runtimeSections{r},
		Commands:  runtimeCommands{r},
		Tools:     registry,
		Shell:     r.shell,
		Context:   ctx,
		Cancel:    cancel,
		Tx:        tx,
		Done:      done,

		PersistGate:  gate,
		Steps:        steps,
		Hooks:        hookEngine,
		SessionStart: sessionStart,
		Live:         func(m []inference.LlmMessage) { r.setLiveConversation(req.SessionID, m) },
		Gate: &permission.Gate{
			Preset:      preset,
			Workspace:   workspace,
			SessionID:   req.SessionID,
			Store:       r.perms,
			Broker:      r.broker,
			SandboxKind: sandbox.Kind(),

			ExtraWritable: extraWritable,
			MCP:           registry.MCPInfo,
		},
	}, req)
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	defer r.setLiveConversation(req.SessionID, nil)
	outcome, err := built.Run()
	if err != nil {
		r.emitDone(tx, req.SessionID, false, agent.NewApiError("internal", err.Error()))
		return agent.ChatOutcome{}, err
	}
	r.emitDone(tx, outcome.SessionID, outcome.Stopped, nil)
	return outcome, nil
}

func (r *AgentRuntime) emitDone(tx chan<- agent.ChatChunk, sessionID string, stopped bool, apiErr *agent.ApiError) {
	select {
	case tx <- agent.ChatChunk{SessionID: sessionID, Done: true, Stopped: stopped, Error: apiErr}:
	default:
	}
}

// StopIn stops the session's running turn and drops its queued ones.
// An empty session id stops every session in cwd (all when cwd is empty).
func (r *AgentRuntime) StopIn(sessionID, cwd string) bool {
	return r.hub.StopSession(sessionID, cwd)
}

// AnswerAsk delivers the user's answers to a blocked ask call. Unknown
// calls are an error so the UI can report that the turn already moved on.
// The answered turn continues on its own: the answers come back as the
// ask tool result and the loop keeps going.
func (r *AgentRuntime) AnswerAsk(sessionID, callID string, answers []tools.AskAnswer) (bool, error) {
	if r.ask == nil {
		return false, fmt.Errorf("ask tool is not available")
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" {
		return false, fmt.Errorf("session id and call id are required")
	}
	if err := r.ask.Resolve(sessionID, callID, answers); err != nil {
		return false, err
	}
	return true, nil
}

// AnswerApproval resolves a pending permission prompt with "once",
// "session", "folder", "global" or "deny". The gated turn continues on its own.
func (r *AgentRuntime) AnswerApproval(sessionID, callID, decision string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" {
		return false, fmt.Errorf("session id and call id are required")
	}
	if err := r.broker.Resolve(sessionID, callID, decision); err != nil {
		return false, err
	}
	return true, nil
}

// CancelAsk drops a blocked ask call and stops its turn.
func (r *AgentRuntime) CancelAsk(sessionID, callID string) bool {
	if r.ask == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" {
		return false
	}
	if !r.ask.Cancel(sessionID, callID) {
		return false
	}
	r.hub.StopRunning(sessionID)
	return true
}

// updateSettings rewrites settings.json through edit, keeping keys this
// build does not know. Writes are atomic and serialized.
func (r *AgentRuntime) updateSettings(edit func(*Settings)) error {
	r.settingsMu.Lock()
	defer r.settingsMu.Unlock()
	raw := map[string]json.RawMessage{}
	var s Settings
	data, err := os.ReadFile(r.settingsPath())
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("settings.json is not valid JSON: %w", err)
		}
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("settings.json: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	edit(&s)
	known := map[string]json.RawMessage{}
	b, _ := json.Marshal(s)
	_ = json.Unmarshal(b, &known)
	st := reflect.TypeOf(s)
	for i := 0; i < st.NumField(); i++ {
		delete(raw, strings.Split(st.Field(i).Tag.Get("json"), ",")[0])
	}
	for k, v := range known {
		raw[k] = v
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.settingsPath()), 0o755); err != nil {
		return err
	}
	tmp := r.settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.settingsPath())
}
