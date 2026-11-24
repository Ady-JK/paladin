package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type ClaudeCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}

func (r *AgentRuntime) ClaudeCommands(cwd string) ([]ClaudeCommand, error) {
	if cwd == "" {
		cwd = os.TempDir()
	}
	r.claudeCatalog.mu.Lock()
	if r.claudeCatalog.commands == nil {
		r.claudeCatalog.commands = map[string]*staleCache[[]ClaudeCommand]{}
	}
	cache := r.claudeCatalog.commands[cwd]
	if cache == nil {
		cache = &staleCache[[]ClaudeCommand]{}
		r.claudeCatalog.commands[cwd] = cache
	}
	r.claudeCatalog.mu.Unlock()
	return cache.get(claudeCommandsTTL, func() ([]ClaudeCommand, error) { return r.loadClaudeCommands(cwd) })
}

// loadClaudeCommands launches the CLI in cwd to read the commands it offers there.
func (r *AgentRuntime) loadClaudeCommands(cwd string) ([]ClaudeCommand, error) {
	ctx, cancel := context.WithTimeout(r.sideCtx, 45*time.Second)
	defer cancel()
	c, err := claude.Start(ctx, claude.Options{Cwd: cwd, PermissionMode: "default"}, nil)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	commands := []ClaudeCommand{}
	for _, cmd := range c.Info.List("commands") {
		commands = append(commands, ClaudeCommand{Name: cmd.Str("name"), Description: cmd.Str("description"), ArgumentHint: cmd.Str("argumentHint")})
	}
	return commands, nil
}
func (r *AgentRuntime) hideClaudeSession(id string) error {
	if id == "" {
		return nil
	}
	r.claudeDiscovery.hiddenMu.Lock()
	defer r.claudeDiscovery.hiddenMu.Unlock()
	path := filepath.Join(r.dataDir, "claude-hidden.json")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	hidden := map[string]bool{}
	if len(b) > 0 {
		if err = json.Unmarshal(b, &hidden); err != nil {
			return err
		}
	}
	hidden[id] = true
	b, err = json.Marshal(hidden)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, b)
}
func (r *AgentRuntime) ImportClaudeSession(id, cwd string) (Session, error) {
	return r.importClaudeSession(id, cwd, false)
}

func (r *AgentRuntime) importClaudeSession(id, cwd string, automatic bool) (Session, error) {
	r.claudeDiscovery.importMu.Lock()
	defer r.claudeDiscovery.importMu.Unlock()
	if automatic {
		r.claudeDiscovery.hiddenMu.Lock()
		data, err := os.ReadFile(filepath.Join(r.dataDir, "claude-hidden.json"))
		r.claudeDiscovery.hiddenMu.Unlock()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Session{}, err
		}
		hidden := map[string]bool{}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &hidden); err != nil {
				return Session{}, err
			}
		}
		if hidden[id] {
			return Session{}, errors.New("claude session link was deleted")
		}
	}
	saved, entries, err := claude.ReadSession(id)
	if err != nil {
		return Session{}, err
	}
	metas, err := r.transcripts.ListMeta("")
	if err != nil {
		return Session{}, err
	}
	for _, m := range metas {
		if m.Harness != nil && m.Harness.Name == HarnessClaude && m.Harness.ThreadID == id {
			return r.openSession(m.SessionID, m.Cwd, true)
		}
	}
	dir := saved.Cwd
	if st, e := os.Stat(dir); e != nil || !st.IsDir() {
		dir = cwd
	}
	if dir == "" {
		dir = saved.Cwd
	}
	sess, err := r.transcripts.Create(dir, HarnessClaude, saved.Model)
	if err != nil {
		return Session{}, err
	}
	sess.Meta.Harness = &transcript.Harness{Name: HarnessClaude, ThreadID: id}
	sess.Meta.Title = shortenTitle(saved.Title)
	for _, entry := range entries {
		if at, err := time.Parse(time.RFC3339Nano, entry.Str("timestamp")); err == nil {
			sess.Meta.CreatedAt = at
			break
		}
	}
	// A half-imported session would look already linked and never be
	// retried, so any failure from here removes it.
	if err = sess.SaveMeta(); err == nil {
		err = sess.ReplaceTurns(claudeTurns(entries))
	}
	if err != nil {
		_ = r.transcripts.Delete(dir, sess.Meta.SessionID)
		return Session{}, err
	}
	return harnessSessionView(sess), nil
}
func claudeTurns(entries []claude.Raw) []transcript.Turn {
	out := []transcript.Turn{}
	p := claudeadapter.NewProjection()
	var user *transcript.Message
	flush := func() {
		p.Flush(nil)
		rows := append([]transcript.Message{}, p.Records...)
		if user != nil {
			rows = append([]transcript.Message{*user}, rows...)
		}
		if len(rows) > 0 {
			out = append(out, transcript.Turn{Messages: rows})
		}
		p = claudeadapter.NewProjection()
		user = nil
	}
	for _, raw := range entries {
		if raw.Str("type") == "user" && !raw.Bool("isMeta") && claude.UserText(raw.Obj("message")) != "" {
			flush()
			rec := transcript.UserMessage(claude.UserText(raw.Obj("message")))
			rec.HarnessTurn = raw.Str("uuid")
			user = &rec
		} else {
			p.Apply(raw)
		}
	}
	flush()
	return out
}
func (r *AgentRuntime) syncClaudeSession(id, cwd string) (bool, error) {
	sess, err := r.transcripts.Load(cwd, id)
	if err != nil {
		return false, err
	}
	h := sess.Meta.Harness
	if h == nil || h.Name != HarnessClaude || h.ThreadID == "" || sess.Meta.Revert != nil {
		return false, nil
	}
	// A running CLI still owns its transcript, even between root turns.
	r.claudeMu.Lock()
	th := r.claudeThreads[id]
	r.claudeMu.Unlock()
	if th != nil {
		select {
		case <-th.client.Done():
		default:
			return false, nil
		}
	}
	before, err := sess.HistoryTurns()
	if err != nil {
		return false, err
	}
	beforeBytes, err := json.Marshal(before)
	if err != nil {
		return false, err
	}
	beforeHash := sha256.Sum256(beforeBytes)
	_, entries, err := claude.ReadSession(h.ThreadID)
	if err != nil {
		return false, err
	}
	source := claudeTurns(entries)
	if len(source) == 0 {
		return false, nil
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(data)
	checkpoint := filepath.Join(sess.Dir, "claude-sync.json")
	changed := false
	err = r.hub.Gated(id, func() error {
		if r.hub.Running(id) {
			return nil
		}
		current, err := r.transcripts.Load(cwd, id)
		if err != nil {
			return err
		}
		if current.Meta.Harness == nil || current.Meta.Harness.ThreadID != h.ThreadID || current.Meta.Revert != nil {
			return nil
		}
		previous, err := os.ReadFile(checkpoint)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if string(previous) == string(hash[:]) {
			return nil
		}
		local, err := current.HistoryTurns()
		if err != nil {
			return err
		}
		localBytes, err := json.Marshal(local)
		if err != nil {
			return err
		}
		if sha256.Sum256(localBytes) != beforeHash {
			return nil
		}
		byID := map[string]transcript.Turn{}
		for _, t := range local {
			if key := harnessTurnID(t); key != "" {
				byID[key] = t
			}
		}
		for i := range source {
			if old, ok := byID[harnessTurnID(source[i])]; ok {
				source[i].Stats = old.Stats
				for j := range source[i].Messages {
					if source[i].Messages[j].Role == transcript.RoleUser && len(old.Messages) > 0 {
						source[i].Messages[j].Attachments = old.Messages[0].Attachments
						source[i].Messages[j].Display = old.Messages[0].Display
					}
				}
			}
		}
		if err := current.ReplaceTurns(source); err != nil {
			return err
		}
		changed = true
		return atomicfile.Write(checkpoint, hash[:])
	})
	return changed, err
}

// Discovery never starts inference. It follows only Claude's project transcript
// directories and creates normal sidebar links, preserving deletion tombstones.
func (r *AgentRuntime) StartClaudeDiscovery() {
	r.claudeDiscovery.lifecycleMu.Lock()
	defer r.claudeDiscovery.lifecycleMu.Unlock()
	if r.sideCtx.Err() != nil {
		return
	}
	r.claudeDiscovery.once.Do(func() {
		r.claudeDiscovery.wg.Add(1)
		go func() {
			defer r.claudeDiscovery.wg.Done()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				r.discoverClaude()
				select {
				case <-r.sideCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

type claudeDiscovery struct {
	lifecycleMu sync.Mutex
	importMu    sync.Mutex
	hiddenMu    sync.Mutex
	once        sync.Once
	wg          sync.WaitGroup
}

func (r *AgentRuntime) discoverClaude() {
	if _, err := claude.FindBinary(); err != nil {
		return
	}
	saved, err := claude.ListSessions("", "")
	if err != nil {
		return
	}
	b, _ := os.ReadFile(filepath.Join(r.dataDir, "claude-hidden.json"))
	hidden := map[string]bool{}
	_ = json.Unmarshal(b, &hidden)
	metas, err := r.transcripts.ListMeta("")
	if err != nil {
		return
	}
	held := map[string]bool{}
	for _, m := range metas {
		if m.Harness != nil && m.Harness.Name == HarnessClaude {
			held[m.Harness.ThreadID] = true
		}
	}
	changed := false
	for _, s := range saved {
		if r.sideCtx.Err() != nil {
			return
		}
		if hidden[s.ID] || held[s.ID] {
			continue
		}
		if _, err := r.importClaudeSession(s.ID, s.Cwd, true); err == nil {
			changed = true
		}
	}
	if changed {
		r.hub.PublishSession("", "sessions/changed")
	}
}
