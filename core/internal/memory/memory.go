// Package memory is Paladin's persistent, bounded agent memory,
// modelled on Hermes: small markdown stores of `§`-delimited entries
// that are injected into the system prompt so the agent remembers
// durable facts across sessions.
//
// Three scopes live under <data>/memory/:
//
//	USER.md                       who the user is (preferences, style)
//	MEMORY.md                     global agent notes (environment, lessons)
//	projects/<key>/MEMORY.md      one folder's notes, shared by every
//	                              session opened in that folder
//
// Every session of a folder reads and writes the same files, so writes
// are coordinated: each mutation takes an exclusive lock, re-reads the
// file from disk, applies the whole batch, checks the budget, and swaps
// the file in atomically. A stale in-memory view can therefore never
// clobber another session's additions. Sessions see a frozen snapshot
// (see SessionSnapshot) so the prompt stays cache-friendly; SnapshotStatus
// reports when the store on disk has moved past that snapshot.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// Delim separates entries on disk; entries may be multiline.
const Delim = "\n§\n"

// Target names one memory scope.
type Target string

const (
	TargetUser    Target = "user"
	TargetGlobal  Target = "global"
	TargetProject Target = "project"
)

// Character budgets per scope, counted over the serialized entries.
// memory.json may override them per target.
const (
	LimitUser    = 1500
	LimitGlobal  = 2500
	LimitProject = 4000
	// MaxEntry caps one entry so a single write cannot eat the budget.
	MaxEntry = 800
)

// ParseTarget validates a target name.
func ParseTarget(s string) (Target, error) {
	switch Target(strings.ToLower(strings.TrimSpace(s))) {
	case TargetUser:
		return TargetUser, nil
	case TargetGlobal, "memory":
		return TargetGlobal, nil
	case TargetProject, "folder":
		return TargetProject, nil
	}
	return "", fmt.Errorf("unknown memory target %q (use user, global or project)", s)
}

// Op is one mutation in a batch.
type Op struct {
	Action  string `json:"action"`
	Content string `json:"content,omitempty"`
	OldText string `json:"old_text,omitempty"`
}

// Result is a store's state after a read or write.
type Result struct {
	Entries []string `json:"entries"`
	Used    int      `json:"used"`
	Limit   int      `json:"limit"`
	// Masked lists the entry indices whose secret-shaped values were
	// replaced on the way in, so the tool can say so.
	Masked []int `json:"masked,omitempty"`
}

// Store is one bounded memory file.
type Store struct {
	path  string
	limit int
}

func NewStore(path string, limit int) *Store { return &Store{path: path, limit: limit} }

func (s *Store) Path() string { return s.path }
func (s *Store) Limit() int   { return s.limit }

// procLocks serializes writers inside this process; the file lock
// covers other processes (a second app instance, the CLI).
var procLocks sync.Map

func (s *Store) lockProc() func() {
	v, _ := procLocks.LoadOrStore(s.path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Entries reads the current entries from disk (deduplicated).
func (s *Store) Entries() ([]string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("io: read memory %s: %w", s.path, err)
	}
	return parse(string(data)), nil
}

// Read reports the entries with usage.
func (s *Store) Read() (Result, error) {
	entries, err := s.Entries()
	if err != nil {
		return Result{}, err
	}
	return s.result(entries), nil
}

func (s *Store) result(entries []string) Result {
	if entries == nil {
		entries = []string{}
	}
	return Result{Entries: entries, Used: used(entries), Limit: s.limit}
}

// Hash fingerprints the store's current on-disk content. A session keeps
// the fingerprint of the snapshot it rendered so a later read can tell
// that another writer moved the file underneath it.
func (s *Store) Hash() string { return hashOfFile(s.path) }

func hashOfFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return hashOf(nil)
	}
	return hashOf(data)
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// Apply runs a batch all-or-nothing against the latest on-disk state.
func (s *Store) Apply(ops []Op) (Result, error) {
	if len(ops) == 0 {
		return Result{}, fmt.Errorf("no operations")
	}
	masked := map[int]bool{}
	return s.mutate(func(entries []string) ([]string, error) {
		for i, op := range ops {
			next, hit, err := applyOp(entries, op)
			if err != nil {
				return nil, fmt.Errorf("operation %d (%s): %w", i+1, op.Action, err)
			}
			entries = next
			if hit >= 0 {
				masked[hit] = true
			}
		}
		return entries, nil
	}, masked)
}

// ReplaceAll swaps the whole store for entries (the settings editor).
// Blank entries are dropped.
func (s *Store) ReplaceAll(entries []string) (Result, error) {
	masked := map[int]bool{}
	return s.mutate(func([]string) ([]string, error) {
		out := []string{}
		for _, e := range entries {
			// Blank rows are dropped before validation so the editor can
			// carry a trailing empty line without complaining.
			if normalize(e) == "" {
				continue
			}
			clean, wasMasked, err := cleanEntry(e)
			if err != nil {
				return nil, err
			}
			if wasMasked {
				masked[len(out)] = true
			}
			out = append(out, clean)
		}
		return out, nil
	}, masked)
}

func (s *Store) mutate(change func([]string) ([]string, error), masked map[int]bool) (Result, error) {
	unlock := s.lockProc()
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return Result{}, fmt.Errorf("io: memory dir: %w", err)
	}
	release, err := lockFile(s.path + ".lock")
	if err != nil {
		return Result{}, fmt.Errorf("io: lock memory: %w", err)
	}
	defer release()
	// Re-read under the lock: another session may have written since
	// this one last looked.
	current, err := s.Entries()
	if err != nil {
		return Result{}, err
	}
	next, err := change(append([]string(nil), current...))
	if err != nil {
		return Result{}, err
	}
	next = dedupe(next)
	if n := used(next); n > s.limit {
		return Result{}, fmt.Errorf("memory limit exceeded: %d/%d chars — consolidate or remove entries in the same batch", n, s.limit)
	}
	if err := atomicWrite(s.path, serialize(next)); err != nil {
		return Result{}, err
	}
	res := s.result(next)
	for i := range next {
		if masked[i] {
			res.Masked = append(res.Masked, i)
		}
	}
	return res, nil
}

// applyOp runs one operation and reports the entry whose content was
// masked on the way in (-1 when nothing was masked).
func applyOp(entries []string, op Op) ([]string, int, error) {
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case "add":
		content, masked, err := cleanEntry(op.Content)
		if err != nil {
			return nil, -1, err
		}
		for _, e := range entries {
			if e == content {
				return nil, -1, fmt.Errorf("that entry is already stored verbatim — use patch to change it, or add a genuinely new fact")
			}
		}
		entries = append(entries, content)
		if masked {
			return entries, len(entries) - 1, nil
		}
		return entries, -1, nil
	case "patch", "replace":
		content, masked, err := cleanEntry(op.Content)
		if err != nil {
			return nil, -1, err
		}
		if strings.TrimSpace(op.OldText) == "" {
			return nil, -1, fmt.Errorf("old_text is required for patch: copy the text of the entry to change")
		}
		i, err := findEntry(entries, op.OldText)
		if err != nil {
			return nil, -1, err
		}
		at, end, err := locateSpan(entries[i], op.OldText)
		if err != nil {
			return nil, -1, err
		}
		patched := normalize(entries[i][:at] + content + entries[i][end:])
		if patched == entries[i] {
			return nil, -1, fmt.Errorf("patch would leave the entry unchanged (%q) — content must differ from the text it replaces", clip(entries[i], 60))
		}
		if err := validEntry(patched); err != nil {
			return nil, -1, err
		}
		entries[i] = patched
		if masked {
			return entries, i, nil
		}
		return entries, -1, nil
	case "remove", "delete":
		if strings.TrimSpace(op.OldText) == "" {
			return nil, -1, fmt.Errorf("old_text is required for remove: copy the text of the entry to drop")
		}
		i, err := findEntry(entries, op.OldText)
		if err != nil {
			return nil, -1, err
		}
		return append(entries[:i], entries[i+1:]...), -1, nil
	}
	return nil, -1, fmt.Errorf("unknown action %q (use add, patch or remove)", op.Action)
}

// cleanEntry prepares one entry for storage: normalize, mask anything
// secret-shaped, then enforce the size, delimiter and threat rules on the
// result. Masking runs before the scan on purpose — a pasted credential
// is neutralized rather than rejected, and the strict scan still applies
// to everything else in the entry.
func cleanEntry(content string) (text string, masked bool, err error) {
	content = normalize(content)
	content, masked = maskSecrets(content)
	if err := validEntry(content); err != nil {
		return "", false, err
	}
	return content, masked, nil
}

func validEntry(content string) error {
	if content == "" {
		return fmt.Errorf("content is required")
	}
	if n := utf8.RuneCountInString(content); n > MaxEntry {
		return fmt.Errorf("entry is %d chars, max %d — keep entries compact and split long notes", n, MaxEntry)
	}
	if strings.Contains(content, "\n§\n") || strings.TrimSpace(content) == "§" {
		return fmt.Errorf("entries must not contain the § delimiter line")
	}
	if reason := Scan(content); reason != "" {
		return fmt.Errorf("blocked: %s", reason)
	}
	return nil
}

// findEntry, locateSpan and their helpers live in match.go.

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s)
}

func parse(data string) []string {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	var out []string
	for _, part := range strings.Split("\n"+data+"\n", Delim) {
		if e := normalize(part); e != "" {
			out = append(out, e)
		}
	}
	return dedupe(out)
}

func serialize(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return strings.Join(entries, Delim) + "\n"
}

func used(entries []string) int {
	if len(entries) == 0 {
		return 0
	}
	return utf8.RuneCountInString(strings.Join(entries, Delim))
}

func dedupe(entries []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

func atomicWrite(path, content string) error {
	if err := atomicfile.Write(path, []byte(content)); err != nil {
		return fmt.Errorf("io: write memory: %w", err)
	}
	return nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Bank resolves the three scopes under one data dir.
type Bank struct {
	dataDir string
	cfg     Config
}

func NewBank(dataDir string) *Bank {
	return &Bank{dataDir: dataDir, cfg: defaultConfig()}
}

// NewBankWithConfig builds a bank from a validated memory.json.
func NewBankWithConfig(dataDir string, cfg Config) *Bank {
	return &Bank{dataDir: dataDir, cfg: cfg.normalized()}
}

// Config returns the bank's live settings (see config.go).
func (b *Bank) Config() Config { return b.cfg }

// SetConfig swaps the settings in place, for a settings-file edit that
// must reach the running runtime without a restart.
func (b *Bank) SetConfig(cfg Config) { b.cfg = cfg.normalized() }

// Root is <data>/memory.
func Root(dataDir string) string { return filepath.Join(dataDir, "memory") }

// ProjectKey names a folder's memory dir: readable tail plus a hash.
func ProjectKey(cwd string) string {
	clean := filepath.ToSlash(filepath.Clean(cwd))
	sum := sha256.Sum256([]byte(clean))
	base := filepath.Base(clean)
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, base)
	if safe == "" || safe == "." || safe == ".." || safe == "_" {
		safe = "root"
	}
	if len(safe) > 40 {
		safe = safe[:40]
	}
	return safe + "-" + hex.EncodeToString(sum[:])[:12]
}

// ProjectDir is one folder's memory directory.
func ProjectDir(dataDir, cwd string) string {
	return filepath.Join(Root(dataDir), "projects", ProjectKey(cwd))
}

// Store returns the store for a scope; project needs a folder.
func (b *Bank) Store(target Target, cwd string) (*Store, error) {
	switch target {
	case TargetUser:
		return NewStore(filepath.Join(Root(b.dataDir), "USER.md"), b.cfg.LimitFor(TargetUser)), nil
	case TargetGlobal:
		return NewStore(filepath.Join(Root(b.dataDir), "MEMORY.md"), b.cfg.LimitFor(TargetGlobal)), nil
	case TargetProject:
		if strings.TrimSpace(cwd) == "" {
			return nil, fmt.Errorf("project memory needs a workspace folder")
		}
		return NewStore(filepath.Join(ProjectDir(b.dataDir, cwd), "MEMORY.md"), b.cfg.LimitFor(TargetProject)), nil
	}
	return nil, fmt.Errorf("unknown memory target %q", target)
}

// Enabled reports whether a scope is switched on in memory.json.
func (b *Bank) Enabled(target Target) bool { return b.cfg.TargetEnabled(target) }

// Apply runs a batch on one scope. Project writes record the folder
// path next to the store so the settings UI can label it.
func (b *Bank) Apply(target Target, cwd string, ops []Op) (Result, error) {
	if !b.Enabled(target) {
		return Result{}, fmt.Errorf("invalid: memory target %q is switched off in memory.json", target)
	}
	s, err := b.Store(target, cwd)
	if err != nil {
		return Result{}, err
	}
	res, err := s.Apply(ops)
	if err == nil && target == TargetProject {
		b.noteFolder(cwd)
	}
	return res, err
}

// ReplaceAll overwrites one scope (the settings editor). The editor may
// still clear a switched-off store, so only additions are refused.
func (b *Bank) ReplaceAll(target Target, cwd string, entries []string) (Result, error) {
	s, err := b.Store(target, cwd)
	if err != nil {
		return Result{}, err
	}
	if !b.Enabled(target) && hasContent(entries) {
		return Result{}, fmt.Errorf("invalid: memory target %q is switched off in memory.json", target)
	}
	res, err := s.ReplaceAll(entries)
	if err == nil && target == TargetProject {
		b.noteFolder(cwd)
	}
	return res, err
}

func hasContent(entries []string) bool {
	for _, e := range entries {
		if strings.TrimSpace(e) != "" {
			return true
		}
	}
	return false
}

// Read reports one scope.
func (b *Bank) Read(target Target, cwd string) (Result, error) {
	s, err := b.Store(target, cwd)
	if err != nil {
		return Result{}, err
	}
	return s.Read()
}

func (b *Bank) noteFolder(cwd string) {
	path := filepath.Join(ProjectDir(b.dataDir, cwd), "folder.txt")
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) == cwd {
		return
	}
	_ = atomicWrite(path, cwd+"\n")
}

// Project is one folder with memory on disk.
type Project struct {
	Cwd     string `json:"cwd"`
	Key     string `json:"key"`
	Entries int    `json:"entries"`
	Used    int    `json:"used"`
	Limit   int    `json:"limit"`
}

// Projects lists folders that have memory, by path.
func (b *Bank) Projects() []Project {
	dir := filepath.Join(Root(b.dataDir), "projects")
	items, err := os.ReadDir(dir)
	if err != nil {
		return []Project{}
	}
	out := []Project{}
	for _, it := range items {
		if !it.IsDir() {
			continue
		}
		folder, err := os.ReadFile(filepath.Join(dir, it.Name(), "folder.txt"))
		if err != nil {
			continue
		}
		cwd := strings.TrimSpace(string(folder))
		res, err := NewStore(filepath.Join(dir, it.Name(), "MEMORY.md"), b.cfg.LimitFor(TargetProject)).Read()
		if err != nil {
			continue
		}
		out = append(out, Project{Cwd: cwd, Key: it.Name(), Entries: len(res.Entries), Used: res.Used, Limit: res.Limit})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cwd < out[j].Cwd })
	return out
}

const rule = "══════════════════════════════════════════════"

// Section is one rendered scope, reported next to the block so the tool
// and the UI can show where the budget went.
type Section struct {
	Target  Target `json:"target"`
	Entries int    `json:"entries"`
	Used    int    `json:"used"`
	Limit   int    `json:"limit"`
	// Blocked counts entries withheld by the threat re-scan.
	Blocked int `json:"blocked"`
}

// Render builds the system-prompt memory block for a folder: user
// profile, global notes, then the folder's notes. Entries that fail the
// threat scan are shown as [BLOCKED] so a hand-edited poisoned file never
// reaches the model. Empty scopes are omitted; all empty renders "".
func (b *Bank) Render(cwd string) string {
	block, _ := b.RenderSections(cwd)
	return block
}

// RenderSections is Render plus a per-scope summary.
func (b *Bank) RenderSections(cwd string) (string, []Section) {
	sections := []struct {
		target Target
		title  string
	}{
		{TargetUser, "USER PROFILE (who the user is)"},
		{TargetGlobal, "MEMORY (your notes, shared by every folder)"},
		{TargetProject, "FOLDER MEMORY (notes for " + cwd + ")"},
	}
	var out strings.Builder
	summary := []Section{}
	for _, sec := range sections {
		if sec.target == TargetProject && strings.TrimSpace(cwd) == "" {
			continue
		}
		if !b.cfg.TargetEnabled(sec.target) {
			continue
		}
		res, err := b.Read(sec.target, cwd)
		if err != nil || len(res.Entries) == 0 {
			continue
		}
		pct := 0
		if res.Limit > 0 {
			pct = res.Used * 100 / res.Limit
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "%s\n%s [%d%% — %d/%d chars]\n%s\n", rule, sec.title, pct, res.Used, res.Limit, rule)
		shown := make([]string, len(res.Entries))
		blocked := 0
		for i, e := range res.Entries {
			if reason := Scan(e); reason != "" {
				blocked++
				shown[i] = "[BLOCKED: entry withheld (" + reason + "); ask the user to review memory]"
			} else {
				shown[i] = e
			}
		}
		out.WriteString(strings.Join(shown, Delim))
		summary = append(summary, Section{Target: sec.target, Entries: len(res.Entries), Used: res.Used, Limit: res.Limit, Blocked: blocked})
	}
	return out.String(), summary
}

// SnapshotFile is the frozen per-session copy of the memory block.
const SnapshotFile = "memory_snapshot.md"

// StateFile records the store fingerprints the snapshot was rendered from.
const StateFile = "memory_snapshot.state"

// SnapshotState compares a session's frozen memory block with the stores
// on disk now, so the UI can say "memory changed since this session
// started" and offer the existing refresh RPC.
type SnapshotState struct {
	// Frozen is true when the session has a snapshot file.
	Frozen bool `json:"frozen"`
	// Stale is true when an enabled store differs from the content the
	// snapshot was rendered from.
	Stale bool `json:"stale"`
	// Changed lists the targets that moved, for an actionable message.
	Changed []string `json:"changed"`
}

// SessionSnapshot returns the session's frozen memory block, creating it
// from the current stores on first use. Freezing keeps the system prompt
// byte-stable across turns (prompt caching) — writes made during the
// session land on disk at once and show up in the next session or after
// ResetSessionSnapshot.
func (b *Bank) SessionSnapshot(sessionDir, cwd string) string {
	if strings.TrimSpace(sessionDir) == "" {
		return b.Render(cwd)
	}
	path := filepath.Join(sessionDir, SnapshotFile)
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	snap := b.Render(cwd)
	_ = atomicWrite(path, snap)
	_ = atomicWrite(filepath.Join(sessionDir, StateFile), b.fingerprint(cwd))
	return snap
}

// fingerprint lists target=hash pairs for the enabled scopes.
func (b *Bank) fingerprint(cwd string) string {
	parts := []string{}
	for _, t := range []Target{TargetUser, TargetGlobal, TargetProject} {
		if t == TargetProject && strings.TrimSpace(cwd) == "" {
			continue
		}
		if !b.cfg.TargetEnabled(t) {
			continue
		}
		s, err := b.Store(t, cwd)
		if err != nil {
			continue
		}
		parts = append(parts, string(t)+"="+s.Hash())
	}
	return strings.Join(parts, "\n")
}

// SnapshotStatus reports whether the stores moved past the session's
// frozen snapshot. A session with no snapshot yet is not stale: its first
// turn reads current memory.
func (b *Bank) SnapshotStatus(sessionDir, cwd string) SnapshotState {
	if strings.TrimSpace(sessionDir) == "" {
		return SnapshotState{}
	}
	if _, err := os.Stat(filepath.Join(sessionDir, SnapshotFile)); err != nil {
		return SnapshotState{}
	}
	st := SnapshotState{Frozen: true}
	data, err := os.ReadFile(filepath.Join(sessionDir, StateFile))
	if err != nil {
		// Snapshot from before state tracking: compare what the prompt
		// actually shows, which is still an honest staleness answer.
		if snap, rerr := os.ReadFile(filepath.Join(sessionDir, SnapshotFile)); rerr == nil && string(snap) != b.Render(cwd) {
			st.Stale = true
			st.Changed = []string{string(TargetGlobal)}
		}
		return st
	}
	before := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			before[k] = v
		}
	}
	for _, line := range strings.Split(b.fingerprint(cwd), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if old, seen := before[k]; !seen || old != v {
			st.Stale = true
			st.Changed = append(st.Changed, k)
		}
		delete(before, k)
	}
	for k := range before {
		st.Stale = true
		st.Changed = append(st.Changed, k)
	}
	sort.Strings(st.Changed)
	return st
}

// ResetSessionSnapshot drops the frozen copy so the next turn re-reads.
func (b *Bank) ResetSessionSnapshot(sessionDir string) error {
	for _, name := range []string{SnapshotFile, StateFile} {
		if err := os.Remove(filepath.Join(sessionDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
