// store.go creates, lists, loads, forks and deletes sessions on disk.

package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"uuid"
)

type Info struct {
	Meta         Meta
	MessageCount int
	Title        string
	// Damaged says why the session could not be read; such a session is
	// still listed (with its id and folder time) so it does not vanish.
	Damaged string
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

// sessionEntry is one session folder found on disk.
type sessionEntry struct {
	dir     string
	meta    Meta
	metaErr error
	at      time.Time // creation time, or the folder's time when meta is unreadable
}

// entries finds the session folders of a workspace (every workspace
// when cwd is empty) and reads their metadata only.
func (s *Store) entries(cwd string) ([]sessionEntry, error) {
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
	var out []sessionEntry
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("io: session: read %s: %w", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || seen[name] {
				continue
			}
			seen[name] = true
			sessionDir := filepath.Join(dir, name)
			meta, err := readMeta(sessionDir)
			if err != nil {
				if !ValidID(name) {
					continue // not a session folder
				}
				at := time.Time{}
				if fi, statErr := e.Info(); statErr == nil {
					at = fi.ModTime()
				}
				out = append(out, sessionEntry{dir: sessionDir, meta: Meta{SessionID: name}, metaErr: err, at: at})
				continue
			}
			out = append(out, sessionEntry{dir: sessionDir, meta: meta, at: meta.CreatedAt})
		}
	}
	return out, nil
}

// List returns sessions of a workspace, newest first, at most limit
// (0 means all). Empty cwd lists all namespaces. Only the returned
// sessions' transcripts are read, through a summary cache. A session
// that cannot be read is listed with Damaged set.
func (s *Store) List(cwd string, limit int) ([]Info, error) {
	entries, err := s.entries(cwd)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	infos := make([]Info, 0, len(entries))
	for _, e := range entries {
		infos = append(infos, info(e))
	}
	return infos, nil
}

// ListMeta returns the readable sessions' metadata without reading
// any transcript, in no particular order.
func (s *Store) ListMeta(cwd string) ([]Meta, error) {
	entries, err := s.entries(cwd)
	if err != nil {
		return nil, err
	}
	metas := make([]Meta, 0, len(entries))
	for _, e := range entries {
		if e.metaErr == nil {
			metas = append(metas, e.meta)
		}
	}
	return metas, nil
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

func info(e sessionEntry) Info {
	if e.metaErr != nil {
		meta := e.meta
		meta.CreatedAt = e.at
		return Info{Meta: meta, Title: "Unreadable session", Damaged: "metadata: " + e.metaErr.Error()}
	}
	out := Info{Meta: e.meta, Title: strings.TrimSpace(e.meta.Title)}
	sum, err := sessionSummary(e.dir)
	if err != nil {
		out.Damaged = "transcript: " + err.Error()
		return out
	}
	out.MessageCount = sum.Count
	if out.Title == "" {
		out.Title = sum.Title
	}
	return out
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
	if err := fork.appendMessages(msgs); err != nil {
		_ = os.RemoveAll(fork.Dir)
		return Session{}, err
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

// CreateChild makes a subagent session beside its parent: same folder
// and worktree, history seeded from snapshot. provider and model default
// to the parent's stored ones when empty. The parent's Children list
// gains the new id.
func (s *Store) CreateChild(parentID, cwd string, link Subagent, snapshot []Message, provider, model string) (Session, error) {
	parent, err := s.Load(cwd, parentID)
	if err != nil {
		return Session{}, err
	}
	if provider == "" && parent.Meta.Provider != nil {
		provider = *parent.Meta.Provider
	}
	if model == "" && parent.Meta.Model != nil {
		model = *parent.Meta.Model
	}
	child, err := s.Create(parent.Meta.Cwd, provider, model)
	if err != nil {
		return Session{}, err
	}
	if err := child.appendMessages(snapshot); err != nil {
		_ = os.RemoveAll(child.Dir)
		return Session{}, err
	}
	link.ParentID = parent.Meta.SessionID
	link.UpdatedAt = time.Now().UTC()
	child.Meta.Subagent = &link
	child.Meta.Title = shorten(strings.TrimSpace(link.Name), MaxTitleRunes)
	child.Meta.Worktree = parent.Meta.Worktree
	if err := writeMeta(child.Dir, child.Meta); err != nil {
		_ = os.RemoveAll(child.Dir)
		return Session{}, err
	}
	err = s.UpdateMeta(cwd, parentID, func(m *Meta) error {
		m.Children = append(m.Children, child.Meta.SessionID)
		return nil
	})
	if err != nil {
		_ = os.RemoveAll(child.Dir)
		return Session{}, err
	}
	return child, nil
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
