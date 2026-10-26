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

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/prompt"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/Ady-JK/paladin/core/internal/turn"
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
}

type FsDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Settings struct {
	SystemPrompt  string `json:"system_prompt"`
	ToolGuidance  string `json:"tool_guidance"`
	MaxToolRounds uint32 `json:"max_tool_rounds"`
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
	tools         *tools.Registry
	shell         *tools.ShellTool
	tracker       *tracker
	providersMu   sync.Mutex
	refreshing    atomic.Bool
	providerCache map[string]inference.Provider
	cacheMu       sync.Mutex
}

func New(dataDir string, cfg config.Config) *AgentRuntime {
	return WithKeyStore(dataDir, cfg, config.OsKeyStore{})
}

func WithKeyStore(dataDir string, cfg config.Config, keys config.KeyStore) *AgentRuntime {
	store := config.NewStore(dataDir, keys)
	shell := tools.NewShellTool()
	registry := tools.NewRegistry()
	registry.Register(tools.ReadTool{})
	engine := tools.NewEngine()
	registry.Register(tools.NewWriteTool(engine))
	registry.Register(tools.NewEditTool(engine))
	registry.Register(shell)
	return &AgentRuntime{
		dataDir:       dataDir,
		config:        cfg,
		store:         store,
		transcripts:   transcript.NewStore(filepath.Join(dataDir, "sessions")),
		tools:         registry,
		shell:         shell,
		tracker:       newTracker(),
		providerCache: map[string]inference.Provider{},
	}
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

func (r *AgentRuntime) systemPrompt(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if settings := r.loadSettings(); strings.TrimSpace(settings.SystemPrompt) != "" {
		return settings.SystemPrompt
	}
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	if strings.TrimSpace(r.config.SystemPrompt) != "" {
		return r.config.SystemPrompt
	}
	return prompt.SystemPrompt
}

func (r *AgentRuntime) toolGuidance() string {
	if settings := r.loadSettings(); strings.TrimSpace(settings.ToolGuidance) != "" {
		return settings.ToolGuidance
	}
	return prompt.ToolGuidance
}

func (r *AgentRuntime) maxRounds() uint32 {
	if settings := r.loadSettings(); settings.MaxToolRounds > 0 {
		return config.ClampMaxToolRounds(settings.MaxToolRounds)
	}
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return config.ClampMaxToolRounds(r.config.MaxToolRounds)
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Chat runs one turn, forwarding chunks on tx (closed with a done chunk
// by the caller protocol) until done is closed.
func (r *AgentRuntime) Chat(req turn.ChatRequest, tx chan<- turn.ChatChunk, done <-chan struct{}) (turn.ChatOutcome, error) {
	defer close(tx)
	if strings.TrimSpace(req.SessionID) == "" {
		return turn.ChatOutcome{}, fmt.Errorf("sessionId is required: create one with open_session first")
	}
	if strings.TrimSpace(req.Message) == "" && !req.Resume {
		return turn.ChatOutcome{}, fmt.Errorf("message is required")
	}
	sess, err := r.transcripts.Load(req.Cwd, req.SessionID)
	if err != nil {
		return turn.ChatOutcome{}, err
	}
	entry, _, provider, err := r.resolveProvider(strVal(req.Provider), strVal(req.Model))
	if err != nil {
		return turn.ChatOutcome{}, err
	}
	_ = entry
	history, err := sess.History()
	if err != nil {
		return turn.ChatOutcome{}, err
	}
	base := r.systemPrompt(strVal(req.SystemPrompt))
	system := prompt.ToolSystem(base, r.toolGuidance(), sess.Meta.Cwd)
	toolDefs := r.toolDefinitions()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !r.tracker.track(req.SessionID, req.Cwd, cancel) {
		return turn.ChatOutcome{}, fmt.Errorf("a turn is already running for session %q", req.SessionID)
	}
	defer r.tracker.untrack(req.SessionID)
	agent := turn.NewAgent(sess, provider, r.tools, r.shell, ctx, cancel, system, r.maxRounds(), history, toolDefs, tx, done)
	outcome, err := agent.RunTurn(req.Message, req.Resume)
	if err != nil {
		r.emitDone(tx, req.SessionID, false, false, turn.NewApiError("internal", err.Error()))
		return turn.ChatOutcome{}, err
	}
	r.emitDone(tx, outcome.SessionID, outcome.Stopped, outcome.NeedsContinue, nil)
	return outcome, nil
}

func (r *AgentRuntime) emitDone(tx chan<- turn.ChatChunk, sessionID string, stopped, needsContinue bool, apiErr *turn.ApiError) {
	select {
	case tx <- turn.ChatChunk{SessionID: sessionID, Done: true, Stopped: stopped, NeedsContinue: needsContinue, Error: apiErr}:
	default:
	}
}

func (r *AgentRuntime) toolDefinitions() []inference.ToolDefinition {
	var out []inference.ToolDefinition
	for _, d := range r.tools.Definitions() {
		out = append(out, inference.ToolDefinition{
			Name: d.Name, Description: d.Description, Parameters: d.Parameters,
		})
	}
	return out
}

func (r *AgentRuntime) Stop(sessionID string) bool {
	return r.tracker.stop(sessionID, "")
}

func (r *AgentRuntime) StopIn(sessionID, cwd string) bool {
	return r.tracker.stop(sessionID, cwd)
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
	if err := r.store.SaveProviders(file); err != nil {
		return config.ProviderInfo{}, err
	}
	if err := r.store.Keys.Set(name, key); err != nil {
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
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		switch strings.ToLower(name) {
		case "system volume information", "$recycle.bin", "recovery":
			continue
		}
		directories = append(directories, FsDirectory{Name: name, Path: filepath.Join(abs, name)})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].Name < directories[j].Name })
	parent := filepath.Dir(abs)
	if parent == abs {
		// Rust parent() of "/" is None -> "". filepath.Dir("/") = "/".
		parent = ""
	}
	return FsList{CurrentPath: abs, ParentPath: parent, Directories: directories}, nil
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
