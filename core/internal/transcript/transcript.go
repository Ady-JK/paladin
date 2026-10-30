// Package transcript owns the on-disk session store:
// <root>/<encode_cwd>/<uuid>/ with meta.json + messages.jsonl.
package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/Ady-JK/paladin/core/internal/toolset"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Signature string `json:"signature,omitempty"`
}

type Message struct {
	Role       Role        `json:"role"`
	Content    string      `json:"content"`
	CreatedAt  time.Time   `json:"createdAt"`
	ToolCalls  *[]ToolCall `json:"toolCalls,omitempty"`
	ToolCallID *string     `json:"toolCallId,omitempty"`
	Name       *string     `json:"name,omitempty"`
	IsError    bool        `json:"isError,omitempty"`
	IsNotify   bool        `json:"isNotify,omitempty"`
	// Compaction marks a summary checkpoint: the model view restarts
	// here, with Content as the summary of everything before it.
	Compaction *Compaction `json:"compaction,omitempty"`
	// Replay marks a copy of a kept message re-recorded after a
	// compaction marker. It feeds the model view only; the original
	// already shows in history.
	Replay bool `json:"replay,omitempty"`
	// Display is the user's short form of an expanded user message
	// (`/review`, `/skill x …`); history and titles show it while the
	// model keeps seeing Content.
	Display string `json:"display,omitempty"`
}

// Compaction describes one context compaction checkpoint.
type Compaction struct {
	// TokensBefore is the estimated context size that triggered it.
	TokensBefore int64 `json:"tokensBefore"`
	// Reason is "auto" (threshold) or "overflow" (provider rejected).
	Reason string `json:"reason"`
}

// IsCompaction reports whether m is a compaction checkpoint marker.
func (m Message) IsCompaction() bool { return m.Role == RoleSystem && m.Compaction != nil }

type Meta struct {
	SchemaVersion uint32    `json:"schemaVersion"`
	SessionID     string    `json:"sessionId"`
	Cwd           string    `json:"cwd"`
	CreatedAt     time.Time `json:"createdAt"`
	Provider      *string   `json:"provider,omitempty"`
	Model         *string   `json:"model,omitempty"`
	// Title is a user-chosen name; empty means derive from messages.
	Title string `json:"title,omitempty"`
	// ForkedFrom is the source session id of a fork.
	ForkedFrom string `json:"forkedFrom,omitempty"`
	// Revert is a pending undo: messages from Index on are hidden and
	// dropped by the next turn, or brought back by a redo.
	Revert *Revert `json:"revert,omitempty"`
	// Worktree is set when the session works in a managed git worktree.
	Worktree *Worktree `json:"worktree,omitempty"`
	// Tools holds the session's tool switches and the tools it has seen.
	Tools *toolset.State `json:"tools,omitempty"`
	// BudgetUsd is the session's spend limit: nil inherits the app
	// default, 0 turns the limit off.
	BudgetUsd *float64 `json:"budgetUsd,omitempty"`
}

// Worktree ties a session to a managed git worktree.
type Worktree struct {
	RepoRoot   string `json:"repoRoot"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Base       string `json:"base"`
	BaseBranch string `json:"baseBranch,omitempty"`
	// Origin is the folder the worktree was made from; project-scoped
	// state such as folder memory stays keyed on it.
	Origin string `json:"origin"`
}

// ProjectCwd is the folder project-scoped state belongs to: the
// worktree's origin folder, or the session's own folder.
func (m Meta) ProjectCwd() string {
	if m.Worktree != nil && m.Worktree.Origin != "" {
		return m.Worktree.Origin
	}
	return m.Cwd
}

// Revert records a pending undo so it can be redone or committed.
type Revert struct {
	// Index is the transcript position of the reverted user message.
	Index int `json:"index"`
	// Pre is the workspace snapshot taken just before the revert.
	Pre string `json:"pre,omitempty"`
	// Restored lists the files the revert rewrote, each with the
	// snapshot entry it wrote ("" when it deleted the file).
	Restored []RevertFile `json:"restored,omitempty"`
	// Skipped lists files left alone because they changed since.
	Skipped []string `json:"skipped,omitempty"`
	// Snapshots is false when only the conversation was reverted.
	Snapshots bool `json:"snapshots"`
}

// RevertFile is one file rewritten by a revert.
type RevertFile struct {
	Path  string `json:"path"`
	Wrote string `json:"wrote"`
}

// MaxTitleRunes caps a user-chosen session title.
const MaxTitleRunes = 120

const SchemaVersion = 1

func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content, CreatedAt: time.Now().UTC()}
}

func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content, CreatedAt: time.Now().UTC()}
}

// Visible in history: users always, assistants only when non-blank.
func (m Message) Visible() bool {
	if m.Replay {
		return false
	}
	switch m.Role {
	case RoleUser:
		return true
	case RoleAssistant:
		if strings.TrimSpace(m.Content) != "" {
			return true
		}
		return m.ToolCalls != nil && len(*m.ToolCalls) > 0
	default:
		return true
	}
}

// TitleFor returns the first user message truncated to 80 chars,
// used as session title.
func TitleFor(messages []Message) string {
	for _, msg := range messages {
		if msg.Role == RoleUser && msg.Visible() {
			title := strings.TrimSpace(msg.Display)
			if title == "" {
				title = strings.TrimSpace(msg.Content)
			}
			if title == "" {
				continue
			}
			return shorten(title, 80)
		}
	}
	return ""
}

func shorten(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	cut := string(runes[:n])
	if i := strings.LastIndexAny(cut, " \n\t"); i > n/2 {
		return strings.TrimRight(cut[:i], " \n\t\r") + "…"
	}
	return strings.TrimRight(cut, " \n\t\r") + "…"
}

// Session is a session: metadata plus its on-disk directory.
type Session struct {
	Meta Meta
	Dir  string
}

func (s *Session) Append(role Role, content string) error {
	return s.AppendMessage(Message{
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Session) AppendMessage(msg Message) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("io: open transcript: %s", syserr.Str(err))
	}
	defer f.Close()
	line, err := jsonx.Marshal(msg)
	if err != nil {
		return fmt.Errorf("io: encode message: %s", syserr.Str(err))
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("io: write message: %s", syserr.Str(err))
	}
	// Sync each append so a crash cannot silently drop the tail of the
	// transcript; message volume is low enough that the cost is noise.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("io: sync message: %s", syserr.Str(err))
	}
	return nil
}

// SaveMeta writes the session's metadata atomically.
func (s *Session) SaveMeta() error {
	return writeMeta(s.Dir, s.Meta)
}

// Truncate keeps the first n transcript messages, atomically.
func (s *Session) Truncate(n int) error {
	msgs, err := s.History()
	if err != nil {
		return err
	}
	if n < 0 || n >= len(msgs) {
		return nil
	}
	var buf []byte
	for _, m := range msgs[:n] {
		line, err := jsonx.Marshal(m)
		if err != nil {
			return fmt.Errorf("io: encode message: %s", syserr.Str(err))
		}
		buf = append(append(buf, line...), '\n')
	}
	return atomicWrite(s.Dir, "messages.jsonl", buf)
}

func (s *Session) History() ([]Message, error) {
	return LoadTranscript(s.Dir)
}

// UsagePath is the per-session token/spend ledger. It lives next to
// meta.json so per-session accounting survives restarts and needs no
// migration of the transcript itself.
func (s *Session) UsagePath() string {
	return filepath.Join(s.Dir, "usage.json")
}

type Info struct {
	Meta         Meta
	MessageCount int
	Title        string
}

// Store is the on-disk session store: <root>/<encode_cwd>/<uuid>/.
type Store struct {
	Root string
}

func NewStore(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) Create(cwd, provider, model string) (Session, error) {
	if strings.TrimSpace(cwd) == "" {
		return Session{}, fmt.Errorf("io: session: cwd is required")
	}
	id := uuid.New().String()
	dir := filepath.Join(s.Root, EncodeCwd(cwd), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Session{}, fmt.Errorf("io: session: create %s: %w", dir, err)
	}
	// Session files may hold workspace code, so they are owner-only.
	if err := os.Chmod(dir, 0o700); err != nil {
		return Session{}, fmt.Errorf("io: session: create %s: %w", dir, err)
	}
	meta := Meta{
		SchemaVersion: SchemaVersion,
		SessionID:     id,
		Cwd:           cwd,
		CreatedAt:     time.Now().UTC(),
	}
	if provider != "" {
		meta.Provider = &provider
	}
	if model != "" {
		meta.Model = &model
	}
	if err := writeMeta(dir, meta); err != nil {
		return Session{}, err
	}
	return Session{Meta: meta, Dir: dir}, nil
}

// List returns sessions of a workspace, newest first. Empty cwd lists
// all namespaces.
func (s *Store) List(cwd string, limit int) ([]Info, error) {
	var dirs []string
	if cwd != "" {
		dirs = append(dirs, sessionDirs(s.Root, cwd)...)
	} else {
		entries, err := os.ReadDir(s.Root)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("io: session: read %s: %w", s.Root, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(s.Root, e.Name()))
			}
		}
	}
	seen := map[string]bool{}
	var infos []Info
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("io: session: read %s: %w", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if seen[name] {
				continue
			}
			seen[name] = true
			sessionDir := filepath.Join(dir, name)
			meta, err := readMeta(sessionDir)
			if err != nil {
				continue
			}
			infos = append(infos, info(sessionDir, meta))
		}
	}
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].Meta.CreatedAt.After(infos[j].Meta.CreatedAt)
	})
	if limit > 0 && len(infos) > limit {
		infos = infos[:limit]
	}
	return infos, nil
}

// Load a session by id, scoping to cwd's namespace when given.
func (s *Store) Load(cwd, sessionID string) (Session, error) {
	if sessionID == "" {
		return Session{}, fmt.Errorf("not found: session: session id is required")
	}
	if !ValidID(sessionID) {
		return Session{}, fmt.Errorf("not found: session: invalid session id %q", sessionID)
	}
	if cwd != "" {
		for _, ns := range sessionDirs(s.Root, cwd) {
			if sess, err := s.loadDir(filepath.Join(ns, sessionID), sessionID); err == nil {
				return sess, nil
			}
		}
	} else {
		entries, err := os.ReadDir(s.Root)
		if err != nil {
			if os.IsNotExist(err) {
				return Session{}, fmt.Errorf("not found: session: %q not found", sessionID)
			}
			return Session{}, fmt.Errorf("io: session: read %s: %w", s.Root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if sess, err := s.loadDir(filepath.Join(s.Root, e.Name(), sessionID), sessionID); err == nil {
				return sess, nil
			}
		}
	}
	return Session{}, fmt.Errorf("not found: session: %q not found", sessionID)
}

func (s *Store) loadDir(dir, sessionID string) (Session, error) {
	meta, err := readMeta(dir)
	if err != nil {
		return Session{}, err
	}
	if meta.SessionID == "" {
		meta.SessionID = sessionID
	} else if meta.SessionID != sessionID {
		return Session{}, fmt.Errorf("not found: session: %q not found", sessionID)
	}
	return Session{Meta: meta, Dir: dir}, nil
}

// Delete a session and remove its namespace dir when empty.
func (s *Store) Delete(cwd, sessionID string) error {
	dir, err := s.sessionDir(cwd, sessionID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("io: session: delete %s: %w", dir, err)
	}
	if parent := filepath.Dir(dir); parent != "" {
		if entries, err := os.ReadDir(parent); err == nil && len(entries) == 0 {
			_ = os.Remove(parent)
		}
	}
	return nil
}

func (s *Store) sessionDir(cwd, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("not found: session: session id is required")
	}
	if !ValidID(sessionID) {
		return "", fmt.Errorf("not found: session: invalid session id %q", sessionID)
	}
	if cwd != "" {
		for _, ns := range sessionDirs(s.Root, cwd) {
			dir := filepath.Join(ns, sessionID)
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				return dir, nil
			}
		}
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("not found: session: %q not found", sessionID)
		}
		return "", fmt.Errorf("io: session: read %s: %w", s.Root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.Root, e.Name(), sessionID)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("not found: session: %q not found", sessionID)
}

func info(dir string, meta Meta) Info {
	msgs, err := LoadTranscript(dir)
	if err != nil {
		msgs = nil
	}
	count := 0
	for _, m := range msgs {
		if m.Visible() {
			count++
		}
	}
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = TitleFor(msgs)
	}
	return Info{Meta: meta, MessageCount: count, Title: title}
}

// Fork copies a session's transcript into a new session in the same
// workspace. The copy is independent: later turns on either side never
// touch the other.
func (s *Store) Fork(cwd, sessionID string) (Session, error) {
	return s.ForkTo(cwd, sessionID, "", nil)
}

// ForkTo forks into another folder (empty means the source's own),
// tied to wt; a same-folder fork keeps the source's worktree.
func (s *Store) ForkTo(cwd, sessionID, target string, wt *Worktree) (Session, error) {
	src, err := s.Load(cwd, sessionID)
	if err != nil {
		return Session{}, err
	}
	msgs, err := src.History()
	if err != nil {
		return Session{}, err
	}
	// A pending undo hides the tail; the fork starts from what is shown.
	if rv := src.Meta.Revert; rv != nil && rv.Index >= 0 && rv.Index < len(msgs) {
		msgs = msgs[:rv.Index]
	}
	provider, model := "", ""
	if src.Meta.Provider != nil {
		provider = *src.Meta.Provider
	}
	if src.Meta.Model != nil {
		model = *src.Meta.Model
	}
	if target == "" {
		target, wt = src.Meta.Cwd, src.Meta.Worktree
	}
	fork, err := s.Create(target, provider, model)
	if err != nil {
		return Session{}, err
	}
	for _, m := range msgs {
		if err := fork.AppendMessage(m); err != nil {
			_ = os.RemoveAll(fork.Dir)
			return Session{}, err
		}
	}
	title := strings.TrimSpace(src.Meta.Title)
	if title == "" {
		title = TitleFor(msgs)
	}
	if title == "" {
		title = "Session"
	}
	fork.Meta.Title = shorten(title, MaxTitleRunes-7) + " (fork)"
	fork.Meta.ForkedFrom = src.Meta.SessionID
	fork.Meta.Worktree = wt
	if err := writeMeta(fork.Dir, fork.Meta); err != nil {
		_ = os.RemoveAll(fork.Dir)
		return Session{}, err
	}
	return fork, nil
}

// Rename sets a session's title; an empty title restores the derived one.
func (s *Store) Rename(cwd, sessionID, title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > MaxTitleRunes {
		return fmt.Errorf("invalid: title is longer than %d characters", MaxTitleRunes)
	}
	return s.UpdateMeta(cwd, sessionID, func(m *Meta) error {
		m.Title = title
		return nil
	})
}

// metaMu serialises read-modify-write updates of meta.json.
var metaMu sync.Mutex

// UpdateMeta loads a session's meta, lets fn change it and writes it
// back; concurrent updates don't lose each other's changes.
func (s *Store) UpdateMeta(cwd, sessionID string, fn func(*Meta) error) error {
	metaMu.Lock()
	defer metaMu.Unlock()
	sess, err := s.Load(cwd, sessionID)
	if err != nil {
		return err
	}
	if err := fn(&sess.Meta); err != nil {
		return err
	}
	return writeMeta(sess.Dir, sess.Meta)
}

// EncodeCwd encodes a cwd to a namespace dir: escapes `-`, `:`, `/`,
// plus a hash suffix so similar inputs never collide. Reads also probe
// the legacy layout.
func EncodeCwd(cwd string) string {
	name := strings.ReplaceAll(cwd, "\\", "/")
	sum := sha256.Sum256([]byte(name))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	name = strings.ReplaceAll(name, "-", "-0")
	name = strings.ReplaceAll(name, ":", "-1")
	name = strings.ReplaceAll(name, "/", "--")
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	if isReservedWindowsName(name) {
		return "_" + name + suffix
	}
	return name + suffix
}

func legacyEncodeCwd(cwd string) string {
	name := strings.ReplaceAll(cwd, "\\", "/")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "/", "--")
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	if isReservedWindowsName(name) {
		return "_" + name
	}
	return name
}

func sessionDirs(root, cwd string) []string {
	fresh := filepath.Join(root, EncodeCwd(cwd))
	legacy := filepath.Join(root, legacyEncodeCwd(cwd))
	if fresh == legacy {
		return []string{fresh}
	}
	return []string{fresh, legacy}
}

func isReservedWindowsName(name string) bool {
	device := name
	if i := strings.IndexByte(device, '.'); i >= 0 {
		device = device[:i]
	}
	switch strings.ToUpper(device) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(device) == 4 {
		upper := strings.ToUpper(device)
		prefix := upper[:3]
		last := device[3]
		if (prefix == "COM" || prefix == "LPT") && last >= '1' && last <= '9' {
			return true
		}
	}
	return false
}

func ValidID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, "/\\") {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

func LoadTranscript(dir string) ([]Message, error) {
	data, err := os.ReadFile(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("io: open transcript: %s", syserr.Str(err))
	}
	var out []Message
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return nil, fmt.Errorf("io: decode message: %s", syserr.Str(err))
		}
		out = append(out, msg)
	}
	return out, nil
}

func writeMeta(dir string, meta Meta) error {
	data, err := jsonx.MarshalIndent(meta)
	if err != nil {
		return fmt.Errorf("io: encode meta: %s", syserr.Str(err))
	}
	return atomicWrite(dir, "meta.json", data)
}

// atomicWrite replaces dir/name so a crash never leaves a half-written
// file behind; session files are owner-only (they may hold code).
func atomicWrite(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, name+"-*.tmp")
	if err != nil {
		return fmt.Errorf("io: write %s: %s", name, syserr.Str(err))
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("io: write %s: %s", name, syserr.Str(err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("io: write %s: %s", name, syserr.Str(err))
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("io: write %s: %s", name, syserr.Str(err))
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("io: write %s: %s", name, syserr.Str(err))
	}
	return nil
}

func readMeta(dir string) (Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Meta{}, fmt.Errorf("io: read meta: %s", syserr.Str(err))
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, fmt.Errorf("io: decode meta: %s", syserr.Str(err))
	}
	if meta.SchemaVersion == 0 {
		meta.SchemaVersion = SchemaVersion
	}
	if meta.SchemaVersion > SchemaVersion {
		return Meta{}, fmt.Errorf("version too new: schema %d > supported %d", meta.SchemaVersion, SchemaVersion)
	}
	return meta, nil
}
