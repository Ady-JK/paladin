// Package custom makes Paladin's built-in text editable. Every
// model-facing text the harness writes itself (prompt sections, tool
// descriptions, reminders, subagent roles) registers here with an id, a
// default and the {{placeholders}} it fills in. A file under
// <data>/config/custom/<id>.md replaces the default; deleting it (Reset)
// brings the default back. Overrides are global only: a cloned
// repository cannot rewrite the agent's instructions.
//
// Owners register their items in init() and read them with Text when
// they build the text, so an edit applies from the next turn on.
package custom

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// Kinds of item. Text items live in custom/<id>.md; the others point at
// files that already have a meaning (see File).
const (
	KindText    = "text"
	KindCommand = "command"
	KindSkill   = "skill"
	KindJSON    = "json"
)

// MaxBytes caps one override.
const MaxBytes = 256 << 10

// Item is one editable built-in.
type Item struct {
	ID          string   `json:"id"`
	Group       string   `json:"group"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Vars        []string `json:"vars,omitempty"`
	// File is the override's path relative to <data>/config; empty means
	// custom/<id>.md.
	File string `json:"-"`
	// Default returns the built-in text.
	Default func() string `json:"-"`
	// Validate, when set, checks an edit before it is saved.
	Validate func(string) error `json:"-"`
	// Order sorts items inside a group (then Title).
	Order int `json:"-"`
}

var (
	mu      sync.RWMutex
	items   = map[string]Item{}
	dataDir string
)

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,95}$`)

// Register adds items; a second registration of an id replaces the first
// (tool descriptions re-register when the tool set changes).
func Register(list ...Item) {
	mu.Lock()
	defer mu.Unlock()
	for _, it := range list {
		if !validID.MatchString(it.ID) || it.Default == nil {
			panic(fmt.Sprintf("custom: bad item %q", it.ID))
		}
		if it.Kind == "" {
			it.Kind = KindText
		}
		items[it.ID] = it
	}
}

// SetDataDir points overrides at a data folder ("" turns them off, as in
// tests that never set it).
func SetDataDir(dir string) {
	mu.Lock()
	dataDir = dir
	mu.Unlock()
}

// Items lists every registered item by group, order and title.
func Items() []Item {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Item, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Group != b.Group {
			return groupRank(a.Group) < groupRank(b.Group) || (groupRank(a.Group) == groupRank(b.Group) && a.Group < b.Group)
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Title < b.Title
	})
	return out
}

// Groups in the order the editor shows them.
var groupOrder = []string{"System prompt", "Tool guidance", "Tool descriptions", "Modes", "Reminders",
	"Subagents", "Compaction", "Commands", "Skills", "Settings files"}

func groupRank(g string) int {
	if i := slices.Index(groupOrder, g); i >= 0 {
		return i
	}
	return len(groupOrder)
}

// Lookup returns one item.
func Lookup(id string) (Item, bool) {
	mu.RLock()
	defer mu.RUnlock()
	it, ok := items[id]
	return it, ok
}

// Path is the override file for an item, or "" without a data folder.
func Path(it Item) string {
	mu.RLock()
	dir := dataDir
	mu.RUnlock()
	if dir == "" {
		return ""
	}
	if it.File != "" {
		return filepath.Join(dir, "config", filepath.FromSlash(it.File))
	}
	return filepath.Join(dir, "config", "custom", it.ID+".md")
}

// override reads an item's file; ok is false when there is none (or it
// is blank, which counts as none).
func override(it Item) (string, bool) {
	path := Path(it)
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > MaxBytes || strings.TrimSpace(string(data)) == "" {
		return "", false
	}
	return string(data), true
}

// Text is the item's current text: the override, else the default. An
// unknown id is a programming error and returns "".
func Text(id string) string {
	it, ok := Lookup(id)
	if !ok {
		return ""
	}
	if s, ok := override(it); ok {
		return s
	}
	return it.Default()
}

// Fill replaces {{name}} placeholders.
func Fill(text string, vars map[string]string) string {
	for k, v := range vars {
		text = strings.ReplaceAll(text, "{{"+k+"}}", v)
	}
	return text
}

// TextFill is Text with placeholders filled.
func TextFill(id string, vars map[string]string) string { return Fill(Text(id), vars) }

// State is one item as the editor shows it.
type State struct {
	Item
	Value      string `json:"value"`
	DefaultVal string `json:"default"`
	Customized bool   `json:"customized"`
	Path       string `json:"path,omitempty"`
}

// Get returns an item's current and default text.
func Get(id string) (State, error) {
	it, ok := Lookup(id)
	if !ok {
		return State{}, fmt.Errorf("not found: no customizable item %q", id)
	}
	def := it.Default()
	st := State{Item: it, Value: def, DefaultVal: def, Path: Path(it)}
	if s, ok := override(it); ok {
		st.Value, st.Customized = s, s != def
	}
	return st, nil
}

// Missing lists the placeholders an edit dropped.
func Missing(it Item, text string) []string {
	var out []string
	for _, v := range it.Vars {
		if !strings.Contains(text, "{{"+v+"}}") {
			out = append(out, v)
		}
	}
	return out
}

// Set saves an override. Text equal to the default removes it.
func Set(id, text string) (State, error) {
	it, ok := Lookup(id)
	if !ok {
		return State{}, fmt.Errorf("not found: no customizable item %q", id)
	}
	path := Path(it)
	if path == "" {
		return State{}, errors.New("customization is unavailable: no data folder")
	}
	if strings.TrimSpace(text) == "" {
		return State{}, errors.New("parse: the text is empty; use Reset to restore the default")
	}
	if len(text) > MaxBytes {
		return State{}, fmt.Errorf("parse: the text is over %d KB", MaxBytes>>10)
	}
	if it.Validate != nil {
		if err := it.Validate(text); err != nil {
			return State{}, fmt.Errorf("parse: %w", err)
		}
	}
	if text == it.Default() && it.Kind != KindJSON {
		return Reset(id)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return State{}, err
	}
	if err := atomicfile.Write(path, []byte(text)); err != nil {
		return State{}, err
	}
	return Get(id)
}

// Reset deletes an override so the default applies again.
func Reset(id string) (State, error) {
	it, ok := Lookup(id)
	if !ok {
		return State{}, fmt.Errorf("not found: no customizable item %q", id)
	}
	if path := Path(it); path != "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
	}
	return Get(id)
}
