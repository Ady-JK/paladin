// Per-model thinking (reasoning effort) levels, kept app-wide in
// settings.json as thinkingLevels: {provider: {model: level}}.

package runtime

import (
	"log"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// ThinkingView is thinkingGet/thinkingSet: the levels the model accepts
// (weakest first; empty when it has no adjustable thinking) and the
// stored one ("" is the provider default).
type ThinkingView struct {
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Levels   []string `json:"levels"`
	Level    string   `json:"level"`
}

// ThinkingGet reports the model's levels and stored choice; an empty
// provider or model resolves to the default one.
func (r *AgentRuntime) ThinkingGet(provider, model string) (ThinkingView, error) {
	entry, _, err := r.store.Resolve(provider)
	if err != nil {
		return ThinkingView{}, err
	}
	if m := strings.TrimSpace(model); m != "" {
		entry.Model = m
	}
	if strings.TrimSpace(entry.Model) == "" {
		entry.Model = r.defaultModel(entry)
	}
	v := ThinkingView{Provider: entry.Name, Model: entry.Model, Levels: []string{}}
	if levels := inference.ModelEffortLevels(entry.ApiType, entry.ApiURL, entry.Model); levels != nil {
		v.Levels = levels
	}
	v.Level = r.loadSettings().ThinkingLevels[entry.Name][entry.Model]
	if inference.ValidEffort(v.Levels, v.Level) != nil {
		v.Level = ""
	}
	return v, nil
}

// ThinkingSet stores the model's level; "" clears it back to the
// provider default. It applies from the next model call.
func (r *AgentRuntime) ThinkingSet(provider, model, level string) (ThinkingView, error) {
	level = strings.TrimSpace(level)
	v, err := r.ThinkingGet(provider, model)
	if err != nil {
		return ThinkingView{}, err
	}
	if err := inference.ValidEffort(v.Levels, level); err != nil {
		return ThinkingView{}, err
	}
	err = r.updateSettings(func(s *Settings) {
		models := s.ThinkingLevels[v.Provider]
		if level == "" {
			delete(models, v.Model)
		} else {
			if models == nil {
				models = map[string]string{}
			}
			models[v.Model] = level
		}
		if s.ThinkingLevels == nil {
			s.ThinkingLevels = map[string]map[string]string{}
		}
		s.ThinkingLevels[v.Provider] = models
		if len(models) == 0 {
			delete(s.ThinkingLevels, v.Provider)
		}
		if len(s.ThinkingLevels) == 0 {
			s.ThinkingLevels = nil
		}
	})
	if err != nil {
		return ThinkingView{}, err
	}
	v.Level = level
	return v, nil
}

// withThinking applies the stored level for (provider, model) to p; a
// level the model no longer accepts is ignored.
func (r *AgentRuntime) withThinking(name, model string, p inference.Provider) inference.Provider {
	level := r.loadSettings().ThinkingLevels[name][model]
	if level == "" {
		return p
	}
	key := name + "\x00" + model + "\x00" + level
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if cached, ok := r.thinkingCache[key]; ok {
		return cached
	}
	out, err := inference.WithEffort(p, level)
	if err != nil {
		log.Printf("thinking: %s/%s: %v; using the provider default", name, model, err)
		return p
	}
	r.thinkingCache[key] = out
	return out
}
