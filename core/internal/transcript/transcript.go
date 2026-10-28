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
	"time"

	"github.com/google/uuid"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
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
	CreatedAt  time.Time   `json:"created_at"`
	ToolCalls  *[]ToolCall `json:"tool_calls,omitempty"`
	ToolCallID *string     `json:"tool_call_id,omitempty"`
	Name       *string     `json:"name,omitempty"`
	IsError    bool        `json:"is_error,omitempty"`
	IsNotify   bool        `json:"is_notify,omitempty"`
}

type Meta struct {
	SchemaVersion uint32    `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	Cwd           string    `json:"cwd"`
	CreatedAt     time.Time `json:"created_at"`
	Provider      *string   `json:"provider,omitempty"`
	Model         *string   `json:"model,omitempty"`
}

const SchemaVersion = 1

func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content, CreatedAt: time.Now().UTC()}
}

func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content, CreatedAt: time.Now().UTC()}
}

// Visible in history: users always, assistants only when non-blank.
func (m Message) Visible() bool {
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
			title := strings.TrimSpace(msg.Content)
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
	return Info{Meta: meta, MessageCount: count, Title: TitleFor(msgs)}
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
	// Atomic write so a crash mid-write never leaves a half-written
	// meta.json behind; session files are owner-only (may hold code).
	tmp, err := os.CreateTemp(dir, "meta-*.tmp")
	if err != nil {
		return fmt.Errorf("io: write meta: %s", syserr.Str(err))
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("io: write meta: %s", syserr.Str(err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("io: write meta: %s", syserr.Str(err))
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("io: write meta: %s", syserr.Str(err))
	}
	if err := os.Rename(tmpName, filepath.Join(dir, "meta.json")); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("io: write meta: %s", syserr.Str(err))
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
