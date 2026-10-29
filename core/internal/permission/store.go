package permission

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/memory"
)

// Rule scopes.
const (
	ScopeGlobal  = "global"
	ScopeFolder  = "folder"
	ScopeSession = "session"
)

// FolderRules is one folder's saved rules.
type FolderRules struct {
	Cwd   string `json:"cwd"`
	Rules []Rule `json:"rules"`
}

type ruleFile struct {
	Cwd   string `json:"cwd,omitempty"`
	Rules []Rule `json:"rules"`
}

// Store keeps global and per-folder rules under <dataDir>/permissions
// and session rules in memory (they end with the app).
type Store struct {
	dir     string
	mu      sync.Mutex
	session map[string][]Rule
}

// NewStore roots the store at dataDir.
func NewStore(dataDir string) *Store {
	return &Store{dir: filepath.Join(dataDir, "permissions"), session: map[string][]Rule{}}
}

func (s *Store) globalPath() string { return filepath.Join(s.dir, "global.json") }

func (s *Store) folderPath(cwd string) string {
	return filepath.Join(s.dir, "folders", memory.ProjectKey(cwd)+".json")
}

func (s *Store) read(path string) ruleFile {
	var f ruleFile
	b, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	if json.Unmarshal(b, &f) != nil {
		return ruleFile{}
	}
	valid := f.Rules[:0]
	for _, r := range f.Rules {
		if r.validate() == nil {
			valid = append(valid, r.normalized())
		}
	}
	f.Rules = valid
	return f
}

func (s *Store) write(path string, f ruleFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if f.Rules == nil {
		f.Rules = []Rule{}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Rules returns one scope's rules.
func (s *Store) Rules(scope, cwd, sessionID string) ([]Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch scope {
	case ScopeGlobal:
		return s.read(s.globalPath()).Rules, nil
	case ScopeFolder:
		if strings.TrimSpace(cwd) == "" {
			return nil, errors.New("cwd is required for folder rules")
		}
		return s.read(s.folderPath(cwd)).Rules, nil
	case ScopeSession:
		return append([]Rule(nil), s.session[sessionID]...), nil
	}
	return nil, fmt.Errorf("scope must be %q, %q or %q, got %q", ScopeGlobal, ScopeFolder, ScopeSession, scope)
}

// Effective is every rule that applies to a call: session, then
// folder, then global (deny rules win regardless of order).
func (s *Store) Effective(cwd, sessionID string) []Rule {
	var out []Rule
	seen := map[Rule]bool{}
	add := func(rs []Rule) {
		for _, r := range rs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	if sessionID != "" {
		rs, _ := s.Rules(ScopeSession, cwd, sessionID)
		add(rs)
	}
	if strings.TrimSpace(cwd) != "" {
		rs, _ := s.Rules(ScopeFolder, cwd, sessionID)
		add(rs)
	}
	rs, _ := s.Rules(ScopeGlobal, cwd, sessionID)
	add(rs)
	return out
}

// Set replaces one scope's rules (the settings editor).
func (s *Store) Set(scope, cwd string, rules []Rule) error {
	clean := make([]Rule, 0, len(rules))
	seen := map[Rule]bool{}
	for _, r := range rules {
		if err := r.validate(); err != nil {
			return err
		}
		r = r.normalized()
		if !seen[r] {
			seen[r] = true
			clean = append(clean, r)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch scope {
	case ScopeGlobal:
		return s.write(s.globalPath(), ruleFile{Rules: clean})
	case ScopeFolder:
		if strings.TrimSpace(cwd) == "" {
			return errors.New("cwd is required for folder rules")
		}
		path := s.folderPath(cwd)
		if len(clean) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		return s.write(path, ruleFile{Cwd: filepath.Clean(cwd), Rules: clean})
	}
	return fmt.Errorf("scope must be %q or %q, got %q", ScopeGlobal, ScopeFolder, scope)
}

// Add appends rules to a scope, skipping duplicates.
func (s *Store) Add(scope, cwd, sessionID string, rules ...Rule) error {
	if scope == ScopeSession {
		for _, r := range rules {
			if err := r.validate(); err != nil {
				return err
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, r := range rules {
			r = r.normalized()
			dup := false
			for _, have := range s.session[sessionID] {
				dup = dup || have == r
			}
			if !dup {
				s.session[sessionID] = append(s.session[sessionID], r)
			}
		}
		return nil
	}
	current, err := s.Rules(scope, cwd, sessionID)
	if err != nil {
		return err
	}
	return s.Set(scope, cwd, append(current, rules...))
}

// Folders lists every folder with saved rules.
func (s *Store) Folders() []FolderRules {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := os.ReadDir(filepath.Join(s.dir, "folders"))
	var out []FolderRules
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f := s.read(filepath.Join(s.dir, "folders", e.Name()))
		if f.Cwd != "" && len(f.Rules) > 0 {
			out = append(out, FolderRules(f))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cwd < out[j].Cwd })
	return out
}
