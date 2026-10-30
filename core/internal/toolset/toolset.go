// Package toolset decides which tools a turn sees. Each tool is on
// unless switched off; a session's own switch beats the global one.
//
// A session also remembers every tool it has seen. Tools that appear
// later (a new MCP server, a server growing its list) change the tool
// list sent to the model and so throw away its prompt cache; they are
// held back as pending until the user accepts them for that session.
package toolset

import "sort"

// State is one session's tool settings, stored in its meta.json.
type State struct {
	// Overrides switches tools on or off for this session only.
	Overrides map[string]bool `json:"overrides,omitempty"`
	// Known is every tool this session has seen or accepted.
	Known []string `json:"known,omitempty"`
	// Seeded is set once the first turn recorded Known; before that
	// every tool is taken without asking.
	Seeded bool `json:"seeded,omitempty"`
}

// Enabled reports whether name is on: the session switch if set, else
// the global one, else on.
func Enabled(name string, global, session map[string]bool) bool {
	if v, ok := session[name]; ok {
		return v
	}
	if v, ok := global[name]; ok {
		return v
	}
	return true
}

// Resolution is what one turn uses.
type Resolution struct {
	// Enabled are the tools the turn gets.
	Enabled []string
	// Pending are new, switched-on tools waiting for the user's ok.
	Pending []string
	// Known is the session's updated seen-set.
	Known []string
}

// Resolve applies the switches and the pending rule to the available
// tools. st may be nil for a session that has no tool state yet.
func Resolve(available []string, global map[string]bool, st *State) Resolution {
	var session map[string]bool
	known := map[string]bool{}
	seeded := false
	if st != nil {
		session, seeded = st.Overrides, st.Seeded
		for _, n := range st.Known {
			known[n] = true
		}
	}
	var r Resolution
	for _, name := range available {
		if !Enabled(name, global, session) {
			known[name] = true
			continue
		}
		_, chosen := session[name]
		if seeded && !known[name] && !chosen {
			r.Pending = append(r.Pending, name)
			continue
		}
		known[name] = true
		r.Enabled = append(r.Enabled, name)
	}
	sort.Strings(r.Enabled)
	sort.Strings(r.Pending)
	r.Known = sortedKeys(known)
	return r
}

// Accept marks names as seen so the next turn includes them.
func (s *State) Accept(names []string) {
	set := map[string]bool{}
	for _, n := range s.Known {
		set[n] = true
	}
	for _, n := range names {
		set[n] = true
	}
	s.Known = sortedKeys(set)
}

// SetOverride sets (or, with a nil value, clears) the switch for each
// name. An empty result is nil so it drops out of the JSON.
func SetOverride(m map[string]bool, names []string, value *bool) map[string]bool {
	if m == nil {
		m = map[string]bool{}
	}
	for _, n := range names {
		if value == nil {
			delete(m, n)
		} else {
			m[n] = *value
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
