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
// (see SessionSnapshot) so the prompt stays cache-friendly.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	return Result{Entries: entries, Used: used(entries), Limit: s.limit}
}

// Apply runs a batch all-or-nothing against the latest on-disk state.
func (s *Store) Apply(ops []Op) (Result, error) {
	if len(ops) == 0 {
		return Result{}, fmt.Errorf("no operations")
	}
	return s.mutate(func(entries []string) ([]string, error) {
		for i, op := range ops {
			next, err := applyOp(entries, op)
			if err != nil {
				return nil, fmt.Errorf("operation %d (%s): %w", i+1, op.Action, err)
			}
			entries = next
		}
		return entries, nil
	})
}

// ReplaceAll swaps the whole store for entries (the settings editor).
// Blank entries are dropped.
func (s *Store) ReplaceAll(entries []string) (Result, error) {
	return s.mutate(func([]string) ([]string, error) {
		out := []string{}
		for _, e := range entries {
			e = normalize(e)
			if e == "" {
				continue
			}
			if reason := Scan(e); reason != "" {
				return nil, fmt.Errorf("blocked: %s", reason)
			}
			out = append(out, e)
		}
		return out, nil
	})
}

func (s *Store) mutate(change func([]string) ([]string, error)) (Result, error) {
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
	return s.result(next), nil
}

func applyOp(entries []string, op Op) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case "add":
		content := normalize(op.Content)
		if err := validEntry(content); err != nil {
			return nil, err
		}
		return append(entries, content), nil
	case "patch", "replace":
		content := normalize(op.Content)
		if err := validEntry(content); err != nil {
			return nil, err
		}
		i, err := findOne(entries, op.OldText)
		if err != nil {
			return nil, err
		}
		if strings.Count(entries[i], op.OldText) != 1 {
			return nil, fmt.Errorf("old_text occurs more than once in the entry; include more context")
		}
		patched := normalize(strings.Replace(entries[i], op.OldText, content, 1))
		if err := validEntry(patched); err != nil {
			return nil, err
		}
		entries[i] = patched
		return entries, nil
	case "remove", "delete":
		i, err := findOne(entries, op.OldText)
		if err != nil {
			return nil, err
		}
		return append(entries[:i], entries[i+1:]...), nil
	}
	return nil, fmt.Errorf("unknown action %q (use add, patch or remove)", op.Action)
}

func validEntry(content string) error {
	if content == "" {
		return fmt.Errorf("content is required")
	}
	if utf8.RuneCountInString(content) > MaxEntry {
		return fmt.Errorf("entry is %d chars, max %d — keep entries compact", utf8.RuneCountInString(content), MaxEntry)
	}
	if strings.Contains(content, "\n§\n") || strings.TrimSpace(content) == "§" {
		return fmt.Errorf("entries must not contain the § delimiter line")
	}
	if reason := Scan(content); reason != "" {
		return fmt.Errorf("blocked: %s", reason)
	}
	return nil
}

// findOne locates the single entry containing needle: an exact entry
// match wins, otherwise the substring must match exactly one entry.
func findOne(entries []string, needle string) (int, error) {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return -1, fmt.Errorf("old_text is required")
	}
	for i, e := range entries {
		if e == needle {
			return i, nil
		}
	}
	hit := -1
	for i, e := range entries {
		if strings.Contains(e, needle) {
			if hit >= 0 {
				return -1, fmt.Errorf("old_text matches more than one entry; include more context")
			}
			hit = i
		}
	}
	if hit < 0 {
		return -1, fmt.Errorf("no entry contains old_text %q", clip(needle, 60))
	}
	return hit, nil
}

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

// threatPatterns catch prompt-injection and exfiltration attempts:
// memory is replayed into every future system prompt, so a poisoned
// entry would be a persistent jailbreak.
var threatPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b[^.\n]{0,40}\b(previous|prior|above|earlier|all|system)\b[^.\n]{0,20}\b(instructions?|prompts?|rules?|messages?)`), "instruction override"},
	{regexp.MustCompile(`(?i)\byou are now\b|\bnew (system )?instructions?:`), "role hijack"},
	{regexp.MustCompile(`(?i)\b(reveal|print|show|output|leak)\b[^.\n]{0,30}\bsystem prompt\b`), "system prompt extraction"},
	{regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(ba|z)?sh\b`), "remote code execution"},
	{regexp.MustCompile(`(?i)\b(send|post|upload|exfiltrate)\b[^.\n]{0,40}\b(secrets?|tokens?|api[ _-]?keys?|credentials?|passwords?)\b`), "exfiltration"},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "private key"},
	{regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{30,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,})\b`), "secret token"},
}

// Scan reports why content must not enter memory, or "" when clean.
func Scan(content string) string {
	for _, r := range content {
		switch {
		case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064, r == 0xFEFF:
			return "invisible or bidi control characters"
		}
	}
	for _, p := range threatPatterns {
		if p.re.MatchString(content) {
			return p.reason
		}
	}
	return ""
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
}

func NewBank(dataDir string) *Bank { return &Bank{dataDir: dataDir} }

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
		return NewStore(filepath.Join(Root(b.dataDir), "USER.md"), LimitUser), nil
	case TargetGlobal:
		return NewStore(filepath.Join(Root(b.dataDir), "MEMORY.md"), LimitGlobal), nil
	case TargetProject:
		if strings.TrimSpace(cwd) == "" {
			return nil, fmt.Errorf("project memory needs a workspace folder")
		}
		return NewStore(filepath.Join(ProjectDir(b.dataDir, cwd), "MEMORY.md"), LimitProject), nil
	}
	return nil, fmt.Errorf("unknown memory target %q", target)
}

// Apply runs a batch on one scope. Project writes record the folder
// path next to the store so the settings UI can label it.
func (b *Bank) Apply(target Target, cwd string, ops []Op) (Result, error) {
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

// ReplaceAll overwrites one scope (the settings editor).
func (b *Bank) ReplaceAll(target Target, cwd string, entries []string) (Result, error) {
	s, err := b.Store(target, cwd)
	if err != nil {
		return Result{}, err
	}
	res, err := s.ReplaceAll(entries)
	if err == nil && target == TargetProject {
		b.noteFolder(cwd)
	}
	return res, err
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
		res, err := NewStore(filepath.Join(dir, it.Name(), "MEMORY.md"), LimitProject).Read()
		if err != nil {
			continue
		}
		out = append(out, Project{Cwd: cwd, Key: it.Name(), Entries: len(res.Entries), Used: res.Used, Limit: res.Limit})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cwd < out[j].Cwd })
	return out
}

const rule = "══════════════════════════════════════════════"

// Render builds the system-prompt memory block for a folder: user
// profile, global notes, then the folder's notes. Entries that fail
// the threat scan are shown as [BLOCKED] so a hand-edited poisoned
// file never reaches the model. Empty scopes are omitted; all empty
// renders "".
func (b *Bank) Render(cwd string) string {
	sections := []struct {
		target Target
		title  string
	}{
		{TargetUser, "USER PROFILE (who the user is)"},
		{TargetGlobal, "MEMORY (your notes, shared by every folder)"},
		{TargetProject, "FOLDER MEMORY (notes for " + cwd + ")"},
	}
	var out strings.Builder
	for _, sec := range sections {
		if sec.target == TargetProject && strings.TrimSpace(cwd) == "" {
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
		for i, e := range res.Entries {
			if reason := Scan(e); reason != "" {
				shown[i] = "[BLOCKED: entry withheld (" + reason + "); ask the user to review memory]"
			} else {
				shown[i] = e
			}
		}
		out.WriteString(strings.Join(shown, Delim))
	}
	return out.String()
}

// SnapshotFile is the frozen per-session copy of the memory block.
const SnapshotFile = "memory_snapshot.md"

// SessionSnapshot returns the session's frozen memory block, creating
// it from the current stores on first use. Freezing keeps the system
// prompt byte-stable across turns (prompt caching) — writes made during
// the session land on disk at once and show up in the next session or
// after ResetSessionSnapshot.
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
	return snap
}

// ResetSessionSnapshot drops the frozen copy so the next turn re-reads.
func (b *Bank) ResetSessionSnapshot(sessionDir string) error {
	err := os.Remove(filepath.Join(sessionDir, SnapshotFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
