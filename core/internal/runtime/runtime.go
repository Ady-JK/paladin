// Package runtime owns the AgentRuntime: sessions, providers, the
// agent turn entry point, and the model catalog refresh.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type Session struct {
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	SessionID string `json:"sessionId"`
}

type FsList struct {
	CurrentPath string        `json:"currentPath"`
	Directories []FsDirectory `json:"directories"`
	ParentPath  string        `json:"parentPath"`
	Files       []FsFile      `json:"files"`
}

type FsDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FsFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Settings struct {
	MaxToolRounds uint32 `json:"max_tool_rounds"`
}

// Prompts is the user-editable agent prompt: the system prompt file
// (config/SYSTEM_PROMPT.md). The file is authoritative — it wins over
// every default; only an explicit per-request override beats it, and
// the settings UI edits it. Tool guidance and examples are compiled
// per-OS in code and always appended with the machine + workspace
// environment block.
type Prompts struct {
	SystemPrompt string `json:"systemPrompt"`
}

type turnState struct {
	cancel context.CancelFunc
	cwd    string
}

type tracker struct {
	mu    sync.Mutex
	inner map[string]turnState
}

func newTracker() *tracker {
	return &tracker{inner: map[string]turnState{}}
}

func (t *tracker) track(sessionID, cwd string, cancel context.CancelFunc) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.inner[sessionID]; ok {
		return false
	}
	t.inner[sessionID] = turnState{cancel: cancel, cwd: cwd}
	return true
}

func (t *tracker) untrack(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.inner, sessionID)
}

// stop cancels one turn by session, or every turn of a workspace when
// the session id is empty. Reports whether anything was running.
func (t *tracker) stop(sessionID, cwd string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sessionID != "" {
		if state, ok := t.inner[sessionID]; ok {
			state.cancel()
			return true
		}
		return false
	}
	stopped := false
	for _, state := range t.inner {
		if cwd == "" || state.cwd == cwd {
			state.cancel()
			stopped = true
		}
	}
	return stopped
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
	agents        *agent.Registry
	tracker       *tracker
	providersMu   sync.Mutex
	refreshing    atomic.Bool
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
	return &AgentRuntime{
		dataDir:       dataDir,
		config:        cfg,
		store:         store,
		transcripts:   transcript.NewStore(filepath.Join(dataDir, "sessions")),
		catalog:       catalog,
		tools:         registry,
		shell:         shellTool,
		ask:           askTool,
		agents:        agents,
		tracker:       newTracker(),
		providerCache: map[string]inference.Provider{},
	}
}

// runtimeSessions adapts the transcript store to agent.SessionSource.
type runtimeSessions struct{ r *AgentRuntime }

func (s runtimeSessions) LoadSession(cwd, sessionID string) (transcript.Session, error) {
	return s.r.transcripts.Load(cwd, sessionID)
}

// runtimeProviders adapts provider resolution to agent.ProviderSource.
type runtimeProviders struct{ r *AgentRuntime }

func (p runtimeProviders) ResolveProvider(name, model string) (inference.Provider, error) {
	_, _, provider, err := p.r.resolveProvider(name, model)
	return provider, err
}

// runtimePrompts adapts the prompt file to agent.PromptSource.
type runtimePrompts struct{ r *AgentRuntime }

func (p runtimePrompts) SystemPrompt(override string) string { return p.r.systemPrompt(override) }

// runtimeSkills adapts skill loading to agent.SkillSource: the global
// config dir plus the session's workspace.
type runtimeSkills struct{ r *AgentRuntime }

func (s runtimeSkills) LoadSkill(cwd, name string) (agent.Skill, error) {
	loaded, err := skills.Load(s.r.dataDir, cwd, name)
	if err != nil {
		return agent.Skill{}, err
	}
	return agent.Skill{Name: loaded.Name, Content: loaded.Content}, nil
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

func (r *AgentRuntime) evictProviderCache() {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.providerCache = map[string]inference.Provider{}
}

func (r *AgentRuntime) resolveProvider(nameOverride, modelOverride string) (config.Provider, string, inference.Provider, error) {
	entry, key, err := r.store.Resolve(nameOverride)
	if err != nil {
		return config.Provider{}, "", nil, err
	}
	if strings.TrimSpace(modelOverride) != "" {
		entry.Model = strings.TrimSpace(modelOverride)
	}
	if strings.TrimSpace(entry.Model) == "" {
		entry.Model = r.defaultModel(entry)
	}
	cacheKey := entry.Name + "\x00" + entry.Model
	r.cacheMu.Lock()
	if cached, ok := r.providerCache[cacheKey]; ok {
		r.cacheMu.Unlock()
		return entry, key, cached, nil
	}
	r.cacheMu.Unlock()
	provider, err := inference.BuildProvider(entry.ApiType, entry.Name, entry.Model, entry.ApiURL, key)
	if err != nil {
		return config.Provider{}, "", nil, err
	}
	r.cacheMu.Lock()
	r.providerCache[cacheKey] = provider
	r.cacheMu.Unlock()
	return entry, key, provider, nil
}

// defaultModel picks the model a session should use when the caller did
// not choose one. Prefer the provider's fetched catalog so a new session
// never falls back to the generic gpt-4o-mini default on a provider that
// does not serve it. OpenCode additionally prefers a known chat model
// when the catalog is empty, because an invalid model name is rejected
// before the request reaches the subscription.
func (r *AgentRuntime) defaultModel(entry config.Provider) string {
	models := r.store.ModelsFor(entry.Name)
	if config.IsOpencodeEndpoint(entry.ApiURL) {
		for _, m := range models {
			if m == config.OpencodeDefaultModel {
				return m
			}
		}
	}
	for _, m := range models {
		if strings.TrimSpace(m) != "" {
			return m
		}
	}
	if config.IsOpencodeEndpoint(entry.ApiURL) {
		return config.OpencodeDefaultModel
	}
	return ""
}

func (r *AgentRuntime) systemPrompt(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return r.store.LoadSystemPrompt()
}

// GetPrompt returns the current prompt file content for the settings
// UI.
func (r *AgentRuntime) GetPrompt() Prompts {
	return Prompts{SystemPrompt: r.store.LoadSystemPrompt()}
}

// SetPrompt replaces the prompt file. Blank text is rejected.
func (r *AgentRuntime) SetPrompt(systemPrompt string) (Prompts, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return Prompts{}, fmt.Errorf("parse: system prompt must not be empty")
	}
	if err := r.store.SaveSystemPrompt(systemPrompt); err != nil {
		return Prompts{}, err
	}
	return r.GetPrompt(), nil
}

// ResetPrompt rewrites the prompt file from the built-in default.
func (r *AgentRuntime) ResetPrompt() (Prompts, error) {
	if err := r.store.ResetPrompt(); err != nil {
		return Prompts{}, err
	}
	return r.GetPrompt(), nil
}

func (r *AgentRuntime) maxRounds() uint32 {
	if settings := r.loadSettings(); settings.MaxToolRounds > 0 {
		return config.ClampMaxToolRounds(settings.MaxToolRounds)
	}
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return config.ClampMaxToolRounds(r.config.MaxToolRounds)
}

// Chat runs one turn by resolving the default agent template. The
// template fixes the provider, session, history, tool subset and prompt
// before the loop starts; Chat only owns lifecycle and tracking.
func (r *AgentRuntime) Chat(req agent.Request, tx chan<- agent.ChatChunk, done <-chan struct{}) (agent.ChatOutcome, error) {
	defer close(tx)
	if strings.TrimSpace(req.SessionID) == "" {
		return agent.ChatOutcome{}, fmt.Errorf("sessionId is required: create one with open_session first")
	}
	if strings.TrimSpace(req.Message) == "" && !req.Resume {
		return agent.ChatOutcome{}, fmt.Errorf("message is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !r.tracker.track(req.SessionID, req.Cwd, cancel) {
		return agent.ChatOutcome{}, fmt.Errorf("a turn is already running for session %q", req.SessionID)
	}
	defer r.tracker.untrack(req.SessionID)

	template, ok := r.agents.Default()
	if !ok {
		return agent.ChatOutcome{}, fmt.Errorf("no agent template registered")
	}
	built, err := template.Build(agent.Deps{
		Sessions:  runtimeSessions{r},
		Providers: runtimeProviders{r},
		Prompts:   runtimePrompts{r},
		Skills:    runtimeSkills{r},
		Tools:     r.tools,
		Shell:     r.shell,
		MaxRounds: r.maxRounds(),
		Context:   ctx,
		Cancel:    cancel,
		Tx:        tx,
		Done:      done,
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

func (r *AgentRuntime) Stop(sessionID string) bool {
	return r.tracker.stop(sessionID, "")
}

func (r *AgentRuntime) StopIn(sessionID, cwd string) bool {
	return r.tracker.stop(sessionID, cwd)
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

// CancelAsk drops a blocked ask call and stops its turn.
func (r *AgentRuntime) CancelAsk(sessionID, callID string) bool {
	if r.ask == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" {
		return false
	}
	if !r.ask.Cancel(sessionID, callID) {
		return false
	}
	r.tracker.stop(sessionID, "")
	return true
}

func (r *AgentRuntime) OpenSession(sessionID, cwd string) (Session, error) {
	if strings.TrimSpace(sessionID) != "" {
		sess, err := r.transcripts.Load(cwd, sessionID)
		if err != nil {
			return Session{}, err
		}
		provider := ""
		if sess.Meta.Provider != nil {
			provider = *sess.Meta.Provider
		}
		model := ""
		if sess.Meta.Model != nil {
			model = *sess.Meta.Model
		}
		return Session{SessionID: sess.Meta.SessionID, Cwd: sess.Meta.Cwd, Provider: provider, Model: model}, nil
	}
	if strings.TrimSpace(cwd) == "" {
		return Session{}, fmt.Errorf("cwd is required to create a session")
	}
	entry, _, provider, err := r.resolveProvider("", "")
	if err != nil {
		return Session{}, err
	}
	sess, err := r.transcripts.Create(cwd, entry.Name, provider.Model())
	if err != nil {
		return Session{}, err
	}
	return Session{SessionID: sess.Meta.SessionID, Cwd: sess.Meta.Cwd, Provider: entry.Name, Model: provider.Model()}, nil
}

func (r *AgentRuntime) ListSessions(cwd string, limit int) ([]session.SessionInfo, error) {
	if limit < 0 {
		limit = 0
	}
	infos, err := r.transcripts.List(cwd, limit)
	if err != nil {
		return nil, err
	}
	out := make([]session.SessionInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, session.InfoFrom(info.Meta, info.MessageCount, info.Title))
	}
	return out, nil
}

func (r *AgentRuntime) SessionHistory(sessionID, cwd string) ([]session.HistoryMessage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	msgs, err := sess.History()
	if err != nil {
		return nil, err
	}
	return session.HistoryView(msgs), nil
}

// SessionTodos returns the session's activity list for the UI's todo
// hat above the composer: empty when the agent has not planned yet,
// never an error for a missing file.
func (r *AgentRuntime) SessionTodos(sessionID, cwd string) ([]session.TodoEntry, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(sess.Dir, session.TodoFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return []session.TodoEntry{}, nil
		}
		return nil, fmt.Errorf("io: session todos: %s", syserr.Str(err))
	}
	return session.TodosView(data), nil
}

// SessionUsage returns the session's billed footprint: accumulated
// tokens, gateway-reported spend, the model's context window and the
// fill percentage. Sessions that predate usage tracking (or whose
// provider never reported usage) come back with zero totals; the window
// is still resolved so the UI can show the budget.
func (r *AgentRuntime) SessionUsage(sessionID, cwd string) (session.SessionUsage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return session.SessionUsage{}, fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return session.SessionUsage{}, err
	}
	var totals session.UsageTotals
	if data, err := os.ReadFile(sess.UsagePath()); err == nil {
		_ = json.Unmarshal(data, &totals)
	}
	model := totals.Model
	if model == "" && sess.Meta.Model != nil {
		model = *sess.Meta.Model
	}
	window, known := inference.ContextWindow(model)
	return totals.View(sess.Meta.SessionID, window, known, inference.ContextWindow), nil
}

// OverallUsage rolls every session's usage.json into one fleet-wide
// view: totals plus per-model, per-session and per-day breakdowns.
// cwd scopes like ListSessions (empty means every workspace).
// limit caps the per-session table (0 means all); totals always cover
// every session found.
func (r *AgentRuntime) OverallUsage(cwd string, limit int) (session.OverallUsage, error) {
	infos, err := r.transcripts.List(cwd, 0)
	if err != nil {
		return session.OverallUsage{}, err
	}
	out := session.OverallUsage{SessionCount: int64(len(infos))}
	byModel := map[string]*session.ModelUsageView{}
	byDay := map[string]*session.DailyPoint{}
	for _, info := range infos {
		sess, err := r.transcripts.Load(info.Meta.Cwd, info.Meta.SessionID)
		if err != nil {
			continue
		}
		var totals session.UsageTotals
		if data, err := os.ReadFile(sess.UsagePath()); err == nil {
			_ = json.Unmarshal(data, &totals)
		}
		if totals.Calls == 0 && totals.TotalTokens == 0 {
			continue
		}
		out.SessionsWithUsage++
		out.Calls += totals.Calls
		out.PromptTokens += totals.PromptTokens
		out.CompletionTokens += totals.CompletionTokens
		out.ReasoningTokens += totals.ReasoningTokens
		out.CacheReadTokens += totals.CacheReadTokens
		out.CacheWriteTokens += totals.CacheWriteTokens
		out.TotalTokens += totals.TotalTokens
		if totals.HasCost {
			out.Cost += totals.Cost
			out.ListCost += totals.ListCost
			out.HasCost = true
		}
		if totals.UpdatedAt > out.UpdatedAt {
			out.UpdatedAt = totals.UpdatedAt
		}
		model := totals.Model
		if model == "" && sess.Meta.Model != nil {
			model = *sess.Meta.Model
		}
		entry := session.OverallSessionEntry{
			SessionID:        sess.Meta.SessionID,
			Title:            info.Title,
			Model:            model,
			CreatedAt:        sess.Meta.CreatedAt.Unix(),
			UpdatedAt:        totals.UpdatedAt,
			Calls:            totals.Calls,
			PromptTokens:     totals.PromptTokens,
			CompletionTokens: totals.CompletionTokens,
			ReasoningTokens:  totals.ReasoningTokens,
			CacheReadTokens:  totals.CacheReadTokens,
			CacheWriteTokens: totals.CacheWriteTokens,
			TotalTokens:      totals.TotalTokens,
			Cost:             totals.Cost,
			ListCost:         totals.ListCost,
			HasCost:          totals.HasCost,
		}
		out.Sessions = append(out.Sessions, entry)
		for name, m := range totals.Models {
			if m == nil {
				continue
			}
			agg := byModel[name]
			if agg == nil {
				agg = &session.ModelUsageView{Model: name}
				byModel[name] = agg
			}
			agg.Calls += m.Calls
			agg.PromptTokens += m.PromptTokens
			agg.CompletionTokens += m.CompletionTokens
			agg.ReasoningTokens += m.ReasoningTokens
			agg.CacheReadTokens += m.CacheReadTokens
			agg.CacheWriteTokens += m.CacheWriteTokens
			agg.TotalTokens += m.TotalTokens
			if m.HasCost {
				agg.Cost += m.Cost
				agg.ListCost += m.ListCost
				agg.HasCost = true
			}
		}
		// Sessions that predate per-model books still contribute to
		// the model share under their session model.
		if len(totals.Models) == 0 && model != "" {
			agg := byModel[model]
			if agg == nil {
				agg = &session.ModelUsageView{Model: model}
				byModel[model] = agg
			}
			agg.Calls += totals.Calls
			agg.PromptTokens += totals.PromptTokens
			agg.CompletionTokens += totals.CompletionTokens
			agg.ReasoningTokens += totals.ReasoningTokens
			agg.CacheReadTokens += totals.CacheReadTokens
			agg.CacheWriteTokens += totals.CacheWriteTokens
			agg.TotalTokens += totals.TotalTokens
			if totals.HasCost {
				agg.Cost += totals.Cost
				agg.ListCost += totals.ListCost
				agg.HasCost = true
			}
		}
		for date, d := range totals.Daily {
			if d == nil {
				continue
			}
			agg := byDay[date]
			if agg == nil {
				agg = &session.DailyPoint{Date: date}
				byDay[date] = agg
			}
			agg.Calls += d.Calls
			agg.PromptTokens += d.PromptTokens
			agg.CompletionTokens += d.CompletionTokens
			agg.ReasoningTokens += d.ReasoningTokens
			agg.CacheReadTokens += d.CacheReadTokens
			agg.CacheWriteTokens += d.CacheWriteTokens
			agg.TotalTokens += d.TotalTokens
			if d.HasCost {
				agg.Cost += d.Cost
				agg.ListCost += d.ListCost
				agg.HasCost = true
			}
		}
	}
	for _, m := range byModel {
		out.Models = append(out.Models, *m)
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].TotalTokens != out.Models[j].TotalTokens {
			return out.Models[i].TotalTokens > out.Models[j].TotalTokens
		}
		return out.Models[i].Model < out.Models[j].Model
	})
	sort.Slice(out.Sessions, func(i, j int) bool {
		if out.Sessions[i].TotalTokens != out.Sessions[j].TotalTokens {
			return out.Sessions[i].TotalTokens > out.Sessions[j].TotalTokens
		}
		return out.Sessions[i].SessionID < out.Sessions[j].SessionID
	})
	if limit > 0 && len(out.Sessions) > limit {
		out.Sessions = out.Sessions[:limit]
	}
	dates := make([]string, 0, len(byDay))
	for date := range byDay {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		out.Daily = append(out.Daily, *byDay[date])
	}
	return out, nil
}

func (r *AgentRuntime) DeleteSession(sessionID, cwd string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return false, fmt.Errorf("session id is required")
	}
	if err := r.transcripts.Delete(cwd, sessionID); err != nil {
		return false, err
	}
	return true, nil
}

func (r *AgentRuntime) ListProviders() []config.ProviderInfo {
	return r.store.ProviderInfos()
}

func (r *AgentRuntime) RefreshProviders() bool {
	if !r.refreshing.CompareAndSwap(false, true) {
		return false
	}
	// Detach: the catalog fetch can take a while; the UI refetches.
	go func() {
		defer r.refreshing.Store(false)
		store := config.NewStore(r.dataDir, r.store.Keys)
		_ = refreshCatalog(store)
	}()
	return true
}

func (r *AgentRuntime) SetDefaultProvider(name string) ([]config.ProviderInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return nil, err
	}
	found := false
	for _, p := range file.Providers {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	file.DefaultProvider = name
	if err := r.store.SaveProviders(file); err != nil {
		return nil, err
	}
	return r.ListProviders(), nil
}

func (r *AgentRuntime) AddProvider(req config.ProviderInput) (config.ProviderInfo, error) {
	name, url, apiType, err := config.ValidateProviderInput(req, true)
	if err != nil {
		return config.ProviderInfo{}, err
	}
	key := strings.TrimSpace(req.ApiKey)
	return r.addProviderLocked(name, url, apiKeyString(apiType), key, req)
}

func apiKeyString(t config.ApiType) string { return string(t) }

func (r *AgentRuntime) addProviderLocked(name, url, apiType, key string, req config.ProviderInput) (config.ProviderInfo, error) {
	at, _ := config.ParseApiType(apiType)
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return config.ProviderInfo{}, err
	}
	for _, p := range file.Providers {
		if strings.EqualFold(p.Name, name) {
			return config.ProviderInfo{}, fmt.Errorf("provider %q already exists", name)
		}
	}
	file.Providers = append(file.Providers, config.Provider{
		Name: name, ApiURL: url, ApiType: at,
	})
	// Write the secret first: if the keychain is unavailable the catalog
	// is left untouched instead of gaining a provider without a key.
	if err := r.store.Keys.Set(name, key); err != nil {
		return config.ProviderInfo{}, err
	}
	if err := r.store.SaveProviders(file); err != nil {
		// The catalog write failed, so the provider does not exist: drop
		// the orphan secret rather than leaving it behind.
		_ = r.store.Keys.Delete(name)
		return config.ProviderInfo{}, err
	}
	r.evictProviderCache()
	r.RefreshProviders()
	return r.findProviderInfo(name)
}

func (r *AgentRuntime) findProviderInfo(name string) (config.ProviderInfo, error) {
	for _, p := range r.ListProviders() {
		if p.Name == name {
			return p, nil
		}
	}
	return config.ProviderInfo{}, fmt.Errorf("provider %q not found", name)
}

func (r *AgentRuntime) UpdateProvider(req config.ProviderInput) (config.ProviderInfo, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return config.ProviderInfo{}, fmt.Errorf("provider name is required")
	}
	validName, url, apiType, err := config.ValidateProviderInput(req, false)
	if err != nil {
		return config.ProviderInfo{}, err
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return config.ProviderInfo{}, err
	}
	found := false
	for i, p := range file.Providers {
		if p.Name == name {
			file.Providers[i].ApiURL = url
			file.Providers[i].ApiType = apiType
			found = true
		}
	}
	if !found {
		return config.ProviderInfo{}, fmt.Errorf("provider %q not found", name)
	}
	// An empty key keeps the stored one.
	if strings.TrimSpace(req.ApiKey) != "" {
		if err := r.store.Keys.Set(name, strings.TrimSpace(req.ApiKey)); err != nil {
			return config.ProviderInfo{}, err
		}
	}
	if err := r.store.SaveProviders(file); err != nil {
		return config.ProviderInfo{}, err
	}
	r.evictProviderCache()
	r.RefreshProviders()
	return r.findProviderInfo(validName)
}

func (r *AgentRuntime) GetWorkspace() string {
	return r.store.Workspace()
}

func (r *AgentRuntime) SetWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	cleaned := ""
	if path != "" {
		var abs string
		if filepath.IsAbs(path) {
			abs = path
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("workspace %q is not reachable: %s", path, syserr.Str(err))
			}
			abs = filepath.Join(cwd, path)
		}
		abs = cleanAbs(abs)
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("workspace %q is not reachable: %s", path, syserr.Str(err))
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workspace %q is not a directory", path)
		}
		cleaned = abs
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return "", err
	}
	if len(file.Providers) == 0 {
		file = config.EnsureSeeded(config.DefaultProvidersFile())
	}
	file.Workspace = cleaned
	if err := r.store.SaveProviders(file); err != nil {
		return "", err
	}
	return cleaned, nil
}

func cleanAbs(path string) string {
	// Mirror Rust clean_path: resolve . and .. lexically.
	vol := filepath.VolumeName(path)
	rest := path[len(vol):]
	isAbs := strings.HasPrefix(rest, "/")
	parts := strings.Split(rest, "/")
	var out []string
	for _, p := range parts {
		switch p {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, p)
		}
	}
	joined := strings.Join(out, "/")
	if isAbs {
		joined = "/" + joined
	}
	if result := vol + joined; result != "" {
		return result
	}
	return "/"
}

func (r *AgentRuntime) FsHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func (r *AgentRuntime) FsList(path string) (FsList, error) {
	path = strings.TrimSpace(path)
	target := path
	if target == "" {
		target = r.FsHome()
	}
	var abs string
	if filepath.IsAbs(target) {
		abs = target
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return FsList{}, fmt.Errorf("path %q is not valid: %v", target, err)
		}
		abs = filepath.Join(cwd, target)
	}
	abs = cleanAbs(abs)
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return FsList{}, fmt.Errorf("path %q does not exist", abs)
		}
		if os.IsPermission(err) {
			return FsList{}, fmt.Errorf("path %q cannot be read: permission denied", abs)
		}
		return FsList{}, fmt.Errorf("path %q cannot be listed: %s", abs, syserr.Str(err))
	}
	directories := []FsDirectory{}
	files := []FsFile{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			switch strings.ToLower(name) {
			case "system volume information", "$recycle.bin", "recovery":
				continue
			}
			directories = append(directories, FsDirectory{Name: name, Path: filepath.Join(abs, name)})
			continue
		}
		files = append(files, FsFile{Name: name, Path: filepath.Join(abs, name)})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].Name < directories[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	parent := filepath.Dir(abs)
	if parent == abs {
		// Rust parent() of "/" is None -> "". filepath.Dir("/") = "/".
		parent = ""
	}
	return FsList{CurrentPath: abs, ParentPath: parent, Directories: directories, Files: files}, nil
}

// FileEntry is one `@` completion row: Path is the Linux-style chat
// path (`src/foo.ts`, `../sib/file`, `src/sub/` for folders),
// ResolvedPath is the OS-specific absolute path the composer inserts
// into the message, Outside flags workspace escapes.
type FileEntry struct {
	Path         string `json:"path"`
	Display      string `json:"display"`
	ResolvedPath string `json:"resolvedPath"`
	Outside      bool   `json:"outside"`
	IsDir        bool   `json:"isDir"`
}

// FileComplete suggests workspace files and folders for a partial
// chat path. Gitignored paths are never suggested. Queries starting
// with `..` complete from the workspace parent so outside files and
// folders tag correctly.
func (r *AgentRuntime) FileComplete(query, cwd string, limit int) ([]FileEntry, error) {
	entries, err := tools.CompleteFiles(resolveCwd(r, cwd), query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, FileEntry{Path: e.Path, Display: e.Display, ResolvedPath: e.ResolvedPath, Outside: e.Outside, IsDir: e.IsDir})
	}
	return out, nil
}

// resolveCwd falls back to the runtime workspace when the caller
// passes no cwd.
func resolveCwd(r *AgentRuntime, cwd string) string {
	if strings.TrimSpace(cwd) != "" {
		return cwd
	}
	return r.GetWorkspace()
}

// SkillEntry is one listed skill: name, description, source
// (global/workspace) and defining path. Content is only returned by
// LoadSkill, never by listing, so the picker stays light.
type SkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
}

// ListSkills discovers skills from the global config dir and the
// workspace dir. A workspace skill shadows a global one of the same
// name.
func (r *AgentRuntime) ListSkills(cwd string) []SkillEntry {
	found := skills.List(r.dataDir, resolveCwd(r, cwd))
	out := make([]SkillEntry, 0, len(found))
	for _, s := range found {
		out = append(out, SkillEntry{Name: s.Name, Description: s.Description, Source: s.Source, Path: s.Path})
	}
	return out
}

func refreshCatalog(store *config.Store) error {
	file, err := store.LoadProviders()
	if err != nil {
		return err
	}
	catalog, err := store.LoadModels()
	if err != nil {
		return err
	}
	for _, p := range file.Providers {
		key, _, err := store.Keys.Get(p.Name)
		if err != nil {
			return err
		}
		listKey := key
		if config.IsPlaceholderKey(key) {
			listKey = "public-listing"
		}
		provider, err := inference.BuildProvider(p.ApiType, p.Name, p.Model, p.ApiURL, listKey)
		if err != nil {
			continue
		}
		models, err := provider.ListModels(context.Background())
		if err != nil {
			continue
		}
		sort.Strings(models)
		entry := catalog.Providers[p.Name]
		entry.AllModels = models
		if len(entry.EnabledModels) == 0 {
			entry.EnabledModels = append([]string(nil), models...)
		}
		if catalog.Providers == nil {
			catalog.Providers = map[string]config.ProviderModels{}
		}
		catalog.Providers[p.Name] = entry
	}
	return store.SaveModels(catalog)
}
