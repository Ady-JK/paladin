// Package runtime owns the AgentRuntime: sessions, providers, the
// agent turn entry point, and the model catalog refresh.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Ady-JK/paladin/core/internal/memory"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/Ady-JK/paladin/core/internal/worktree"
)

type Settings struct {
	MaxToolRounds uint32 `json:"maxToolRounds"`
	// MaxConcurrentTurns caps turns running at once across sessions;
	// extra turns wait in the hub queue. 0 means the default.
	MaxConcurrentTurns uint32 `json:"maxConcurrentTurns,omitempty"`
}

type AgentRuntime struct {
	dataDir       string
	config        config.Config
	configMu      sync.RWMutex
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
	providersMu   sync.Mutex
	refreshing    atomic.Bool
	refreshWG     sync.WaitGroup
	fetchMu       sync.RWMutex
	fetchModels   ModelFetcher
	providerCache map[string]inference.Provider
	cacheMu       sync.Mutex
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
		fetchModels:   networkModelFetcher,
	}
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

func (r *AgentRuntime) maxRounds() uint32 {
	if settings := r.loadSettings(); settings.MaxToolRounds > 0 {
		return config.ClampMaxToolRounds(settings.MaxToolRounds)
	}
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return config.ClampMaxToolRounds(r.config.MaxToolRounds)
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
	var steps agent.StepRecorder
	var extraWritable []string
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
	built, err := template.Build(agent.Deps{
		Sessions:  runtimeSessions{r},
		Providers: runtimeProviders{r},
		Prompts:   runtimePrompts{r},
		Skills:    runtimeSkills{r},
		Sections:  runtimeSections{r},
		Commands:  runtimeCommands{r},
		Tools:     r.tools,
		Shell:     r.shell,
		MaxRounds: r.maxRounds(),
		Context:   ctx,
		Cancel:    cancel,
		Tx:        tx,
		Done:      done,

		PersistGate: gate,
		Steps:       steps,
		Gate: &permission.Gate{
			Preset:      preset,
			Workspace:   workspace,
			SessionID:   req.SessionID,
			Store:       r.perms,
			Broker:      r.broker,
			SandboxKind: sandbox.Kind(),

			ExtraWritable: extraWritable,
		},
	}, req)
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	outcome, err := built.Run()
	if err != nil {
		r.emitDone(tx, req.SessionID, false, false, agent.NewApiError("internal", err.Error()))
		return agent.ChatOutcome{}, err
	}
	r.emitDone(tx, outcome.SessionID, outcome.Stopped, outcome.NeedsContinue, nil)
	return outcome, nil
}

func (r *AgentRuntime) emitDone(tx chan<- agent.ChatChunk, sessionID string, stopped, needsContinue bool, apiErr *agent.ApiError) {
	select {
	case tx <- agent.ChatChunk{SessionID: sessionID, Done: true, Stopped: stopped, NeedsContinue: needsContinue, Error: apiErr}:
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
// "session", "folder" or "deny". The gated turn continues on its own.
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
