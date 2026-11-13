package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type ContextOption struct {
	Window      int64 `json:"window"`
	TotalWindow int64 `json:"totalWindow,omitempty"`
	Surcharge   bool  `json:"surcharge,omitempty"`
}

type CompactionView struct {
	Provider    string          `json:"provider"`
	Model       string          `json:"model"`
	Window      int64           `json:"window"`
	TotalWindow int64           `json:"totalWindow,omitempty"`
	Known       bool            `json:"known"`
	Percent     float64         `json:"percent"`
	Options     []ContextOption `json:"options,omitempty"`
}

func (r *AgentRuntime) CompactionGet(provider, model string) (CompactionView, error) {
	entry, _, err := r.store.Resolve(provider)
	if err != nil {
		return CompactionView{}, err
	}
	if m := strings.TrimSpace(model); m != "" {
		entry.Model = m
	}
	if strings.TrimSpace(entry.Model) == "" {
		entry.Model = r.defaultModel(entry)
	}
	meta := r.store.ModelMeta(entry.Name, entry.Model)
	settings := r.loadSettings()
	_, known := inference.WindowFor(meta, entry.Model)
	window := selectedContextWindow(meta, entry.Model, settings.ContextInputs[entry.Name][entry.Model])
	pct := settings.CompactPercent[entry.Name][entry.Model]
	var options []ContextOption
	if meta.DefaultInput > 0 && meta.MaxInput > meta.DefaultInput {
		options = []ContextOption{{Window: meta.DefaultInput}, {Window: meta.MaxInput, TotalWindow: meta.ContextWindow, Surcharge: meta.LongContextSurcharge}}
	}
	if pct <= 0 || pct > 100 {
		pct = 90
	}
	return CompactionView{Provider: entry.Name, Model: entry.Model, Window: window, TotalWindow: meta.ContextWindow, Known: known, Percent: pct, Options: options}, nil
}

func selectedContextWindow(meta config.ModelMeta, model string, requested int64) int64 {
	full, _ := inference.WindowFor(meta, model)
	if meta.DefaultInput > 0 && meta.DefaultInput < full {
		if requested == meta.DefaultInput || requested == full {
			return requested
		}
		if meta.LongContextSurcharge {
			return meta.DefaultInput
		}
	}
	return full
}

func (r *AgentRuntime) CompactionSet(provider, model string, pct float64) (CompactionView, error) {
	return r.CompactionSetWindow(provider, model, pct, nil)
}

func (r *AgentRuntime) CompactionSetWindow(provider, model string, pct float64, requested *int64) (CompactionView, error) {
	if math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 1 || pct > 100 {
		return CompactionView{}, fmt.Errorf("compaction threshold must be at least 1%% and at most 100%%")
	}
	view, err := r.CompactionGet(provider, model)
	if err != nil {
		return CompactionView{}, err
	}
	if requested != nil {
		valid := false
		for _, option := range view.Options {
			if option.Window == *requested {
				valid = true
			}
		}
		if len(view.Options) == 0 && *requested == view.Window {
			valid = true
		}
		if !valid {
			return CompactionView{}, fmt.Errorf("context window is not offered by this provider")
		}
	}
	err = r.updateSettings(func(s *Settings) {
		if s.CompactPercent == nil {
			s.CompactPercent = map[string]map[string]float64{}
		}
		if s.CompactPercent[view.Provider] == nil {
			s.CompactPercent[view.Provider] = map[string]float64{}
		}
		s.CompactPercent[view.Provider][view.Model] = pct
		if requested != nil {
			if s.ContextInputs == nil {
				s.ContextInputs = map[string]map[string]int64{}
			}
			if s.ContextInputs[view.Provider] == nil {
				s.ContextInputs[view.Provider] = map[string]int64{}
			}
			s.ContextInputs[view.Provider][view.Model] = *requested
		}
	})
	if err != nil {
		return CompactionView{}, err
	}
	return r.CompactionGet(view.Provider, view.Model)
}

// CompactionGetForSession applies a session's model-specific choice over app defaults.
func (r *AgentRuntime) CompactionGetForSession(provider, model, sessionID, cwd string) (CompactionView, error) {
	view, err := r.CompactionGet(provider, model)
	if err != nil || sessionID == "" {
		return view, err
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return CompactionView{}, err
	}
	if choice, ok := sess.Meta.ContextChoices[view.Provider][view.Model]; ok {
		meta := r.store.ModelMeta(view.Provider, view.Model)
		view.Window = selectedContextWindow(meta, view.Model, choice.Window)
		if choice.Percent >= 1 && choice.Percent <= 100 {
			view.Percent = choice.Percent
		}
	}
	return view, nil
}

// CompactionSetForSession persists without changing other sessions or defaults.
func (r *AgentRuntime) CompactionSetForSession(provider, model string, pct float64, window *int64, sessionID, cwd string) (CompactionView, error) {
	if sessionID == "" {
		return r.CompactionSetWindow(provider, model, pct, window)
	}
	if math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 1 || pct > 100 {
		return CompactionView{}, fmt.Errorf("compaction threshold must be at least 1%% and at most 100%%")
	}
	view, err := r.CompactionGetForSession(provider, model, sessionID, cwd)
	if err != nil {
		return CompactionView{}, err
	}
	chosen := view.Window
	if window != nil {
		chosen = *window
	}
	valid := chosen == view.Window && len(view.Options) == 0
	for _, option := range view.Options {
		if option.Window == chosen {
			valid = true
		}
	}
	if !valid {
		return CompactionView{}, fmt.Errorf("context window is not offered by this provider")
	}
	err = r.transcripts.UpdateMeta(cwd, sessionID, func(m *transcript.Meta) error {
		if m.ContextChoices == nil {
			m.ContextChoices = map[string]map[string]transcript.ContextChoice{}
		}
		if m.ContextChoices[view.Provider] == nil {
			m.ContextChoices[view.Provider] = map[string]transcript.ContextChoice{}
		}
		m.ContextChoices[view.Provider][view.Model] = transcript.ContextChoice{Window: chosen, Percent: pct}
		// Reopening the session should return to the model whose context was selected.
		m.Provider = &view.Provider
		m.Model = &view.Model
		return nil
	})
	if err != nil {
		return CompactionView{}, err
	}
	return r.CompactionGetForSession(view.Provider, view.Model, sessionID, cwd)
}

// sessionContextChoice freezes defaults at the first turn and preserves concurrent edits.
func (r *AgentRuntime) sessionContextChoice(cwd, sessionID string, meta transcript.Meta, provider, model string, window int64, percent float64) (transcript.ContextChoice, error) {
	if choice, ok := meta.ContextChoices[provider][model]; ok {
		return choice, nil
	}
	if percent < 1 || percent > 100 {
		percent = 90
	}
	choice := transcript.ContextChoice{Window: window, Percent: percent}
	err := r.transcripts.UpdateMeta(cwd, sessionID, func(m *transcript.Meta) error {
		if m.ContextChoices == nil {
			m.ContextChoices = map[string]map[string]transcript.ContextChoice{}
		}
		if m.ContextChoices[provider] == nil {
			m.ContextChoices[provider] = map[string]transcript.ContextChoice{}
		}
		if existing, ok := m.ContextChoices[provider][model]; ok {
			choice = existing
		} else {
			m.ContextChoices[provider][model] = choice
		}
		return nil
	})
	return choice, err
}
