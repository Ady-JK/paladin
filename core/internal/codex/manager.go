package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const modelCacheTTL = 10 * time.Minute

// Model is one model Codex offers, as `model/list` reports it.
type Model struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName"`
	Description   string   `json:"description,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	Efforts       []string `json:"efforts"`
	IsDefault     bool     `json:"isDefault"`
	// FastTier is the service tier that makes the model faster at the cost of
	// more of the plan (Codex's fast mode), empty when it has none.
	FastTier string `json:"fastTier,omitempty"`
	// FastNote is how Codex describes that tier.
	FastNote string `json:"fastNote,omitempty"`
}

// Status is what the UI needs to offer Codex: whether the CLI works, who is
// signed in and which models it can run.
type Status struct {
	Installed bool    `json:"installed"`
	Version   string  `json:"version,omitempty"`
	LoggedIn  bool    `json:"loggedIn"`
	Account   string  `json:"account,omitempty"`
	Models    []Model `json:"models"`
	// Permissions are the permission presets Codex accepts on this machine.
	Permissions []string `json:"permissions"`
	Error       string   `json:"error,omitempty"`
}

// Manager owns the shared app-server process, starting it on first use and
// again after it dies.
type Manager struct {
	mu     sync.Mutex
	client *Client
	// startMu lets one caller at a time start the app-server without holding
	// mu, so Live, Close and Models never wait on a slow start.
	startMu sync.Mutex
	// startedAt, coolUntil and lastErr back off after a start that failed or
	// a process that died right away.
	startedAt time.Time
	coolUntil time.Time
	lastErr   error

	models   []Model
	modelsAt time.Time
}

// NewManager builds a manager; nothing starts until a Client is asked for.
func NewManager() *Manager { return &Manager{} }

// startCooldown is how long a failed or short-lived app-server is left alone
// before another start is tried.
const (
	startCooldown = 5 * time.Second
	youngProcess  = 10 * time.Second
)

// live returns the running client, noting a process that died young.
func (m *Manager) live() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return nil
	}
	select {
	case <-m.client.Done():
		if time.Since(m.startedAt) < youngProcess {
			m.coolUntil, m.lastErr = time.Now().Add(startCooldown), m.client.Err()
		}
		m.client = nil
		return nil
	default:
		return m.client
	}
}

// Client returns the live app-server, starting it when needed.
func (m *Manager) Client(ctx context.Context) (*Client, error) {
	if c := m.live(); c != nil {
		return c, nil
	}
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if c := m.live(); c != nil {
		return c, nil // another caller started it while this one waited
	}
	m.mu.Lock()
	if time.Now().Before(m.coolUntil) && m.lastErr != nil {
		err := m.lastErr
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	bin, err := FindBinary()
	if err != nil {
		return nil, err
	}
	c, err := Start(ctx, bin)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.coolUntil, m.lastErr = time.Now().Add(startCooldown), err
		return nil, err
	}
	m.client, m.startedAt = c, time.Now()
	return c, nil
}

// Live returns the app-server if one is running, and never starts one.
func (m *Manager) Live() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return nil
	}
	select {
	case <-m.client.Done():
		return nil
	default:
		return m.client
	}
}

// Close stops the app-server.
func (m *Manager) Close() {
	m.mu.Lock()
	c := m.client
	m.client = nil
	m.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// Models lists the models Codex offers (cached for a few minutes).
func (m *Manager) Models(ctx context.Context) ([]Model, error) {
	m.mu.Lock()
	if len(m.models) > 0 && time.Since(m.modelsAt) < modelCacheTTL {
		out := append([]Model(nil), m.models...)
		m.mu.Unlock()
		return out, nil
	}
	m.mu.Unlock()
	c, err := m.Client(ctx)
	if err != nil {
		return nil, err
	}
	models, err := c.Models(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.models, m.modelsAt = models, time.Now()
	m.mu.Unlock()
	return models, nil
}

// Status reports whether Codex is usable. It never fails: problems are
// carried in Error so the UI can explain them.
func (m *Manager) Status(ctx context.Context) Status {
	if _, err := FindBinary(); err != nil {
		return Status{Models: []Model{}, Permissions: []string{}, Error: err.Error()}
	}
	st := Status{Installed: true, Models: []Model{}, Permissions: []string{}}
	c, err := m.Client(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if fields := strings.Fields(c.Info.UserAgent); len(fields) > 0 {
		st.Version = strings.TrimPrefix(fields[0], "paladin/")
	}
	var acct struct {
		Account *struct {
			Type  string `json:"type"`
			Email string `json:"email"`
		} `json:"account"`
		RequiresOpenaiAuth bool `json:"requiresOpenaiAuth"`
	}
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &acct); err != nil {
		st.Error = err.Error()
		return st
	}
	if acct.Account != nil {
		st.LoggedIn = true
		st.Account = acct.Account.Email
		if st.Account == "" {
			st.Account = acct.Account.Type
		}
	} else if !acct.RequiresOpenaiAuth {
		st.LoggedIn = true
	}
	if !st.LoggedIn {
		st.Error = "Codex is not signed in: run `codex login` in a terminal"
		return st
	}
	models, err := m.Models(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Models = models
	st.Permissions = c.Requirements().Presets()
	return st
}

// Models asks the app-server for its visible models.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	var res struct {
		Data []struct {
			ID               string `json:"id"`
			Model            string `json:"model"`
			DisplayName      string `json:"displayName"`
			Description      string `json:"description"`
			Hidden           bool   `json:"hidden"`
			IsDefault        bool   `json:"isDefault"`
			DefaultEffort    string `json:"defaultReasoningEffort"`
			SupportedEfforts []struct {
				Effort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
			ServiceTiers []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"serviceTiers"`
			SpeedTiers []string `json:"additionalSpeedTiers"`
		} `json:"data"`
	}
	if err := c.Call(ctx, "model/list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(res.Data))
	for _, d := range res.Data {
		if d.Hidden {
			continue
		}
		id := d.Model
		if id == "" {
			id = d.ID
		}
		efforts := make([]string, 0, len(d.SupportedEfforts))
		for _, e := range d.SupportedEfforts {
			efforts = append(efforts, e.Effort)
		}
		name := d.DisplayName
		if name == "" {
			name = id
		}
		m := Model{ID: id, DisplayName: name, Description: d.Description,
			DefaultEffort: d.DefaultEffort, Efforts: efforts, IsDefault: d.IsDefault}
		for _, t := range d.ServiceTiers {
			if strings.EqualFold(t.Name, "fast") || t.ID == "fast" || t.ID == "priority" {
				m.FastTier, m.FastNote = t.ID, t.Description
				break
			}
		}
		if m.FastTier == "" {
			// Older Codex lists only the names of the extra speeds.
			for _, t := range d.SpeedTiers {
				if t == "fast" {
					m.FastTier = t
				}
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// ThreadParams are the settings a thread starts or resumes with.
type ThreadParams struct {
	Cwd            string
	Model          string
	ApprovalPolicy string // untrusted, on-request, never
	Sandbox        string // read-only, workspace-write, danger-full-access
}

func (p ThreadParams) wire() map[string]any {
	out := map[string]any{"cwd": p.Cwd, "approvalPolicy": p.ApprovalPolicy, "sandbox": p.Sandbox}
	if p.Model != "" {
		out["model"] = p.Model
	}
	return out
}

// StartThread creates a Codex thread and returns its id.
func (c *Client) StartThread(ctx context.Context, p ThreadParams) (string, error) {
	params := p.wire()
	params["serviceName"] = "paladin"
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if err := c.Call(ctx, "thread/start", params, &res); err != nil {
		return "", err
	}
	if res.Thread.ID == "" {
		return "", fmt.Errorf("codex thread/start returned no thread id")
	}
	c.noteModel(res.Thread.ID, res.Model)
	c.MarkLoaded(res.Thread.ID)
	return res.Thread.ID, nil
}

// ResumeThread loads a thread started by an earlier process.
func (c *Client) ResumeThread(ctx context.Context, threadID string, p ThreadParams) error {
	if c.Loaded(threadID) {
		return nil
	}
	params := p.wire()
	params["threadId"] = threadID
	params["excludeTurns"] = true
	var res struct {
		Model string `json:"model"`
	}
	if err := c.Call(ctx, "thread/resume", params, &res); err != nil {
		if strings.Contains(err.Error(), "already has an active writer") {
			return fmt.Errorf("this Codex thread is open in another Codex window or terminal: close it there, then send your message again")
		}
		return fmt.Errorf("resume codex thread %s: %w", threadID, err)
	}
	c.noteModel(threadID, res.Model)
	c.MarkLoaded(threadID)
	return nil
}

// TurnParams describe one turn.
type TurnParams struct {
	ThreadID       string
	Input          []map[string]any
	Cwd            string
	Model          string
	Effort         string
	ApprovalPolicy string
	SandboxPolicy  map[string]any
	// Collaboration is Codex's mode for the turn ("default" or "plan") and
	// the settings it runs with; every turn sends it so leaving plan mode sticks.
	Collaboration map[string]any
	// Summary is how Codex summarizes its reasoning ("auto", "concise",
	// "detailed" or "none"); without one it streams no reasoning at all.
	Summary string
	// ServiceTier is the speed tier for this and later turns ("priority" is
	// Codex's fast mode); empty keeps the thread's.
	ServiceTier string
}

// ReasoningSummary is the summary style to ask for: the one the user set in
// Codex's config, else "auto". A turn's own setting overrides the config, and
// some providers refuse "detailed", so the configured value is passed on.
func (c *Client) ReasoningSummary(ctx context.Context, cwd string) string {
	var res struct {
		Config Raw `json:"config"`
	}
	if err := c.Call(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &res); err == nil {
		if style := res.Config.Str("model_reasoning_summary"); style != "" {
			return style
		}
	}
	return "auto"
}

// StartTurn begins a turn and returns its id.
func (c *Client) StartTurn(ctx context.Context, p TurnParams) (string, error) {
	params := map[string]any{"threadId": p.ThreadID, "input": p.Input}
	if p.Cwd != "" {
		params["cwd"] = p.Cwd
	}
	if p.Model != "" {
		params["model"] = p.Model
	}
	if p.Effort != "" {
		params["effort"] = p.Effort
	}
	if p.ApprovalPolicy != "" {
		params["approvalPolicy"] = p.ApprovalPolicy
	}
	if p.SandboxPolicy != nil {
		params["sandboxPolicy"] = p.SandboxPolicy
	}
	if p.Collaboration != nil {
		params["collaborationMode"] = p.Collaboration
	}
	if p.Summary != "" {
		params["summary"] = p.Summary
	}
	if p.ServiceTier != "" {
		params["serviceTier"] = p.ServiceTier
	}
	var res struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := c.Call(ctx, "turn/start", params, &res); err != nil {
		return "", err
	}
	return res.Turn.ID, nil
}

// SteerTurn adds a user message to the running turn.
func (c *Client) SteerTurn(ctx context.Context, threadID, turnID string, input []map[string]any) error {
	return c.Call(ctx, "turn/steer", map[string]any{"threadId": threadID, "expectedTurnId": turnID, "input": input}, nil)
}

// TerminateProcess stops a command Codex started in a thread, by the process id
// its command item carries. A thread that is already gone has nothing left to stop.
func (c *Client) TerminateProcess(ctx context.Context, threadID, processID string) error {
	err := c.Call(ctx, "thread/backgroundTerminals/terminate", map[string]any{"threadId": threadID, "processId": processID}, nil)
	if err != nil && strings.Contains(err.Error(), "thread not found") {
		return nil
	}
	return err
}

// InterruptTurn asks Codex to stop the running turn.
func (c *Client) InterruptTurn(ctx context.Context, threadID, turnID string) error {
	return c.Call(ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}, nil)
}

// RollbackThread drops the last n turns from the thread's history.
func (c *Client) RollbackThread(ctx context.Context, threadID string, n int) error {
	return c.Call(ctx, "thread/rollback", map[string]any{"threadId": threadID, "numTurns": n}, nil)
}

// Raw is a decoded JSON object with helpers for the loosely typed item
// payloads Codex sends.
type Raw map[string]json.RawMessage

// Decode parses a JSON object; invalid input yields an empty Raw.
func Decode(data []byte) Raw {
	var r Raw
	if json.Unmarshal(data, &r) != nil {
		return Raw{}
	}
	return r
}

// Str returns a string field ("" when absent or not a string).
func (r Raw) Str(key string) string {
	var s string
	_ = json.Unmarshal(r[key], &s)
	return s
}

// Obj returns an object field.
func (r Raw) Obj(key string) Raw {
	if v, ok := r[key]; ok {
		return Decode(v)
	}
	return Raw{}
}

// Int returns a numeric field (0 when absent).
func (r Raw) Int(key string) int64 {
	var n float64
	_ = json.Unmarshal(r[key], &n)
	return int64(n)
}

// List returns an array of objects.
func (r Raw) List(key string) []Raw {
	var items []json.RawMessage
	_ = json.Unmarshal(r[key], &items)
	out := make([]Raw, 0, len(items))
	for _, it := range items {
		out = append(out, Decode(it))
	}
	return out
}

func boolOf(raw json.RawMessage) bool {
	var b bool
	return json.Unmarshal(raw, &b) == nil && b
}

// Strings reads a field that is a list of strings.
func (r Raw) Strings(key string) []string {
	var out []string
	_ = json.Unmarshal(r[key], &out)
	return out
}

// JSON re-encodes one field ("null" when absent).
func (r Raw) JSON(key string) string {
	if v, ok := r[key]; ok {
		return string(v)
	}
	return "null"
}

// Has reports whether the field is present and not null.
func (r Raw) Has(key string) bool {
	v, ok := r[key]
	return ok && string(v) != "null"
}

// CompactThread asks Codex to compact the thread's context. The compaction
// runs as a turn whose notifications arrive on the thread's subscription.
func (c *Client) CompactThread(ctx context.Context, threadID string) error {
	return c.Call(ctx, "thread/compact/start", map[string]any{"threadId": threadID}, nil)
}

// ForkThread makes a new thread holding sourceID's history through the turn
// lastTurnID (inclusive) and returns its id. The source must not be running.
// A source that was archived is unarchived and the fork retried once.
func (c *Client) ForkThread(ctx context.Context, sourceID, lastTurnID string, p ThreadParams) (string, error) {
	params := p.wire()
	params["threadId"] = sourceID
	params["excludeTurns"] = true
	if lastTurnID != "" {
		params["lastTurnId"] = lastTurnID
	}
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	err := c.Call(ctx, "thread/fork", params, &res)
	if err != nil && archivedPattern.MatchString(err.Error()) {
		if uerr := c.Call(ctx, "thread/unarchive", map[string]any{"threadId": sourceID}, nil); uerr == nil {
			res.Thread.ID = ""
			err = c.Call(ctx, "thread/fork", params, &res)
		}
	}
	if err != nil {
		return "", fmt.Errorf("fork codex thread %s: %w", sourceID, err)
	}
	if res.Thread.ID == "" {
		return "", fmt.Errorf("codex thread/fork returned no thread id")
	}
	c.noteModel(res.Thread.ID, res.Model)
	c.MarkLoaded(res.Thread.ID)
	return res.Thread.ID, nil
}

// archivedPattern matches Codex's complaint about an archived thread.
var archivedPattern = regexp.MustCompile(`(?i)\bsession \S+ is archived\b|\bcodex unarchive\b`)

// RevertThread drops the thread's history from the turn beforeTurnID on (that
// turn and every later one). It changes the conversation only; files stay as
// they are. The thread must be loaded, and have paginated history.
func (c *Client) RevertThread(ctx context.Context, threadID, beforeTurnID string) error {
	return c.Call(ctx, "thread/revert", map[string]any{"threadId": threadID, "beforeTurnId": beforeTurnID}, nil)
}

// StartReview starts a code review turn on the thread. target is one of the
// protocol's review targets ({"type":"uncommittedChanges"}, …).
func (c *Client) StartReview(ctx context.Context, threadID string, target map[string]any) error {
	return c.Call(ctx, "review/start", map[string]any{"threadId": threadID, "target": target, "delivery": "inline"}, nil)
}

// Goal is a thread's long-running objective.
type Goal struct {
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	TokenBudget *int64 `json:"tokenBudget"`
	TokensUsed  int64  `json:"tokensUsed"`
	TimeUsedSec int64  `json:"timeUsedSeconds"`
}

// GetGoal reads the thread's goal (nil when it has none).
func (c *Client) GetGoal(ctx context.Context, threadID string) (*Goal, error) {
	var res struct {
		Goal *Goal `json:"goal"`
	}
	if err := c.Call(ctx, "thread/goal/get", map[string]any{"threadId": threadID}, &res); err != nil {
		return nil, err
	}
	return res.Goal, nil
}

// SetGoal sets the objective, or only the status when objective is empty.
func (c *Client) SetGoal(ctx context.Context, threadID, objective, status string) (*Goal, error) {
	params := map[string]any{"threadId": threadID}
	if objective != "" {
		params["objective"] = objective
	}
	if status != "" {
		params["status"] = status
	}
	var res struct {
		Goal *Goal `json:"goal"`
	}
	if err := c.Call(ctx, "thread/goal/set", params, &res); err != nil {
		return nil, err
	}
	return res.Goal, nil
}

// ClearGoal removes the thread's goal.
func (c *Client) ClearGoal(ctx context.Context, threadID string) error {
	return c.Call(ctx, "thread/goal/clear", map[string]any{"threadId": threadID}, nil)
}

// UploadFeedback sends the thread and Codex's logs to OpenAI and returns the
// id support can look up.
func (c *Client) UploadFeedback(ctx context.Context, threadID, reason string) (string, error) {
	params := map[string]any{"classification": "bug", "includeLogs": true, "threadId": threadID}
	if reason != "" {
		params["reason"] = reason
	}
	var res struct {
		ThreadID string `json:"threadId"`
	}
	if err := c.Call(ctx, "feedback/upload", params, &res); err != nil {
		return "", err
	}
	return res.ThreadID, nil
}
