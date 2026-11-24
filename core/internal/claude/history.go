package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// SavedSession contains only metadata; transcripts remain in Claude's own store.
type SavedSession struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Preview   string `json:"preview"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
	Path      string `json:"-"`
}

func configDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}
func validID(id string) bool { _, err := uuid.Parse(id); return err == nil }

// ReadEntries tolerates an incomplete final line of a transcript currently
// being written, but a malformed complete entry is an error, not silently lost.
func ReadEntries(path string) ([]Raw, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	entries := []Raw{}
	var line []byte
	for {
		piece, prefix, e := reader.ReadLine()
		line = append(line, piece...)
		if len(line) > maxFrame {
			return nil, fmt.Errorf("claude transcript entry exceeds %d bytes", maxFrame)
		}
		if prefix {
			continue
		}
		if len(line) > 0 {
			if !json.Valid(line) {
				if _, peekErr := reader.Peek(1); peekErr != nil {
					break
				}
				return nil, errors.New("invalid JSON in Claude transcript")
			}
			entries = append(entries, Decode(line))
			line = nil
		}
		if e != nil {
			break
		}
	}
	return entries, nil
}
func ListSessions(cwd, search string) ([]SavedSession, error) {
	files, err := filepath.Glob(filepath.Join(configDir(), "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	out := []SavedSession{}
	for _, p := range files {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if !validID(id) {
			continue
		}
		st, e := os.Stat(p)
		if e != nil {
			continue
		}
		entries, e := ReadEntries(p)
		if e != nil {
			continue
		}
		s := SavedSession{ID: id, Path: p, UpdatedAt: st.ModTime().Unix()}
		for _, r := range entries {
			if r.Str("cwd") != "" {
				s.Cwd = r.Str("cwd")
			}
			if r.Str("type") == "assistant" {
				s.Model = r.Obj("message").Str("model")
			}
			if r.Str("type") == "user" && s.Preview == "" && !r.Bool("isMeta") {
				s.Preview = UserText(r.Obj("message"))
			}
			if title := r.Str("customTitle"); title != "" {
				s.Title = title
			}
			if title := r.Str("summary"); s.Title == "" && title != "" {
				s.Title = title
			}
		}
		if s.Preview == "" {
			continue
		}
		if s.Title == "" {
			s.Title = s.Preview
		}
		if cwd != "" && filepath.Clean(cwd) != filepath.Clean(s.Cwd) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.Preview), strings.ToLower(search)) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}
func ReadSession(id string) (SavedSession, []Raw, error) {
	if !validID(id) {
		return SavedSession{}, nil, errors.New("claude session ID must be a UUID")
	}
	files, err := filepath.Glob(filepath.Join(configDir(), "projects", "*", id+".jsonl"))
	if err != nil {
		return SavedSession{}, nil, err
	}
	if len(files) == 0 {
		return SavedSession{}, nil, fmt.Errorf("claude session %s was not found", id)
	}
	if len(files) != 1 {
		return SavedSession{}, nil, errors.New("claude session ID exists in multiple project directories")
	}
	entries, err := ReadEntries(files[0])
	if err != nil {
		return SavedSession{}, nil, err
	}
	st, err := os.Stat(files[0])
	if err != nil {
		return SavedSession{}, nil, err
	}
	saved := SavedSession{ID: id, Path: files[0], UpdatedAt: st.ModTime().Unix()}
	for _, raw := range entries {
		if cwd := raw.Str("cwd"); cwd != "" {
			saved.Cwd = cwd
		}
		if raw.Str("type") == "assistant" {
			saved.Model = raw.Obj("message").Str("model")
		}
		if raw.Str("type") == "user" && saved.Preview == "" && !raw.Bool("isMeta") {
			saved.Preview = UserText(raw.Obj("message"))
		}
		if title := raw.Str("customTitle"); title != "" {
			saved.Title = title
		}
	}
	if saved.Title == "" {
		saved.Title = saved.Preview
	}
	return saved, Conversation(entries), nil
}
func UserText(m Raw) string {
	if s := m.Str("content"); s != "" {
		return s
	}
	var parts []string
	for _, b := range m.List("content") {
		if b.Str("type") == "text" {
			parts = append(parts, b.Str("text"))
		}
	}
	return strings.Join(parts, "\n")
}

// Conversation follows the latest parentUuid chain, avoiding abandoned branches
// after an external rewind and excluding sidechain/subagent conversations.
func Conversation(entries []Raw) []Raw {
	byID := map[string]Raw{}
	var leaf string
	for _, r := range entries {
		typ := r.Str("type")
		if r.Bool("isSidechain") || r.Str("parent_tool_use_id") != "" {
			continue
		}
		if id := r.Str("uuid"); id != "" {
			// Attachment/progress records are invisible but sit in the parent
			// chain between messages. Keep their links, not their rendered text.
			byID[id] = r
			if typ == "assistant" || typ == "user" || typ == "system" {
				leaf = id
			}
		}
	}
	chain := []Raw{}
	seen := map[string]bool{}
	for leaf != "" && !seen[leaf] {
		seen[leaf] = true
		r := byID[leaf]
		if r == nil {
			break
		}
		chain = append(chain, r)
		leaf = r.Str("parentUuid")
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}
