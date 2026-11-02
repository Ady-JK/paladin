// memory.json is the user's memory settings: per-target character
// budgets, which scopes are switched on, and the backend provider name.
// It mirrors lsp.json — a small JSON file under <data>/config/, read
// tolerantly (a missing or broken file falls back to the defaults) and
// validated before the settings editor saves it.
//
// Defaults reproduce the behaviour of the built-in budgets exactly, so an
// absent file changes nothing.

package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigPath is the user's memory overrides.
func ConfigPath(dataDir string) string { return filepath.Join(dataDir, "config", "memory.json") }

// ProviderFile is the only backend that exists: the §-delimited files
// under <data>/memory. The seam is here so a second backend can be added
// without touching the tool or the prompt path.
const ProviderFile = "file"

// maxLimit bounds a configured budget so a typo cannot unbound the
// prompt. Above this a single store would crowd out the rest.
const maxLimit = 200_000

// Config is the parsed memory.json.
type Config struct {
	// Limits overrides the per-target character budgets. Absent or zero
	// entries keep the built-in default for that target.
	Limits Limits `json:"limits,omitempty"`
	// Disabled lists scopes to switch off: "user", "global", "project".
	// A switched-off scope is neither rendered nor written by the tool.
	Disabled []string `json:"disabled,omitempty"`
	// Provider names the storage backend. Empty means ProviderFile.
	Provider string `json:"provider,omitempty"`
	// ReviewTurns is how many turns may pass without a memory write
	// before the runtime nudges for a background review. Zero means the
	// built-in default; see ReviewTurnsDefault.
	ReviewTurns int `json:"reviewTurns,omitempty"`
}

// Limits holds the per-scope budgets.
type Limits struct {
	User    int `json:"user,omitempty"`
	Global  int `json:"global,omitempty"`
	Project int `json:"project,omitempty"`
}

func defaultConfig() Config {
	return Config{Limits: Limits{User: LimitUser, Global: LimitGlobal, Project: LimitProject}, Provider: ProviderFile}
}

// normalized fills every gap with its default so callers never branch.
func (c Config) normalized() Config {
	out := c
	if out.Provider = strings.TrimSpace(out.Provider); out.Provider == "" {
		out.Provider = ProviderFile
	}
	if out.Limits.User <= 0 {
		out.Limits.User = LimitUser
	}
	if out.Limits.Global <= 0 {
		out.Limits.Global = LimitGlobal
	}
	if out.Limits.Project <= 0 {
		out.Limits.Project = LimitProject
	}
	if out.ReviewTurns <= 0 {
		out.ReviewTurns = ReviewTurnsDefault
	}
	return out
}

// ReviewTurnsDefault is how many turns without a memory write trigger a
// review nudge. Counted in turns rather than wall-clock time: a session
// that sat idle overnight has not learned anything new, and one long
// productive turn has.
const ReviewTurnsDefault = 12

// LimitFor reports the budget for one scope.
func (c Config) LimitFor(t Target) int {
	n := c.normalized()
	switch t {
	case TargetUser:
		return n.Limits.User
	case TargetGlobal:
		return n.Limits.Global
	case TargetProject:
		return n.Limits.Project
	}
	return 0
}

// TargetEnabled reports whether writes and rendering include a scope.
func (c Config) TargetEnabled(t Target) bool {
	for _, d := range c.Disabled {
		if strings.EqualFold(strings.TrimSpace(d), string(t)) {
			return false
		}
	}
	return true
}

// LoadConfig reads memory.json. A missing file yields the defaults; a
// file that fails to parse is an error the caller can surface without
// losing the ability to run (see LoadConfigOr).
func LoadConfig(dataDir string) (Config, error) {
	data, err := os.ReadFile(ConfigPath(dataDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultConfig(), nil
		}
		return defaultConfig(), fmt.Errorf("io: read memory.json: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return defaultConfig(), nil
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&cfg); err != nil {
		return defaultConfig(), fmt.Errorf("config: memory.json: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return defaultConfig(), err
	}
	return cfg.normalized(), nil
}

// LoadConfigOr is LoadConfig but never fails: a broken file falls back to
// the defaults so memory keeps working, matching how lsp.json degrades.
func LoadConfigOr(dataDir string) Config {
	cfg, err := LoadConfig(dataDir)
	if err != nil {
		return defaultConfig()
	}
	return cfg
}

// ValidateConfig checks memory.json text before the editor saves it.
func ValidateConfig(text string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil || raw == nil {
		return errors.New("memory.json must be a JSON object")
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("memory.json: %w", err)
	}
	return cfg.validate()
}

func (c Config) validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Provider)) {
	case "", ProviderFile:
	default:
		return fmt.Errorf("memory.json: provider %q is not available (only %q is built in)", c.Provider, ProviderFile)
	}
	for _, d := range c.Disabled {
		name := strings.TrimSpace(d)
		if name == "" {
			return errors.New("memory.json: disabled entries must name a target (user, global or project)")
		}
		if _, err := ParseTarget(name); err != nil {
			return fmt.Errorf("memory.json: %w", err)
		}
	}
	for name, n := range map[string]int{"user": c.Limits.User, "global": c.Limits.Global, "project": c.Limits.Project} {
		if n == 0 {
			continue
		}
		if n < 200 {
			return fmt.Errorf("memory.json: limits.%s must be at least 200 chars", name)
		}
		if n > maxLimit {
			return fmt.Errorf("memory.json: limits.%s must be at most %d chars", name, maxLimit)
		}
	}
	if c.ReviewTurns < 0 {
		return errors.New("memory.json: reviewTurns must be zero (default) or positive")
	}
	return nil
}

// DefaultConfigText is what Settings → Customize shows for a reset.
func DefaultConfigText() string {
	out, err := json.MarshalIndent(struct {
		Limits      Limits   `json:"limits"`
		Disabled    []string `json:"disabled"`
		Provider    string   `json:"provider"`
		ReviewTurns int      `json:"reviewTurns"`
	}{Limits: Limits{User: LimitUser, Global: LimitGlobal, Project: LimitProject}, Disabled: []string{}, Provider: ProviderFile, ReviewTurns: ReviewTurnsDefault}, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(out) + "\n"
}
