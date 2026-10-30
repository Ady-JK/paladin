package toolset

import (
	"reflect"
	"testing"
)

func on(b bool) *bool { return &b }

func TestEnabledSessionBeatsGlobal(t *testing.T) {
	cases := []struct {
		global, session map[string]bool
		want            bool
	}{
		{nil, nil, true},
		{map[string]bool{"shell": false}, nil, false},
		{map[string]bool{"shell": false}, map[string]bool{"shell": true}, true},
		{map[string]bool{"shell": true}, map[string]bool{"shell": false}, false},
		{nil, map[string]bool{"other": false}, true},
	}
	for i, c := range cases {
		if got := Enabled("shell", c.global, c.session); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestResolveFreshSessionTakesEverything(t *testing.T) {
	r := Resolve([]string{"read", "shell", "mcp__a__x"}, map[string]bool{"shell": false}, nil)
	if !reflect.DeepEqual(r.Enabled, []string{"mcp__a__x", "read"}) || len(r.Pending) != 0 {
		t.Fatalf("%+v", r)
	}
	// Disabled tools are still seen, so turning them on later is not "new".
	if !reflect.DeepEqual(r.Known, []string{"mcp__a__x", "read", "shell"}) {
		t.Fatalf("known %v", r.Known)
	}
}

func TestResolveHoldsBackNewTools(t *testing.T) {
	st := &State{Seeded: true, Known: []string{"read", "shell"}}
	r := Resolve([]string{"read", "shell", "mcp__a__x", "mcp__a__y"}, nil, st)
	if !reflect.DeepEqual(r.Enabled, []string{"read", "shell"}) {
		t.Fatalf("enabled %v", r.Enabled)
	}
	if !reflect.DeepEqual(r.Pending, []string{"mcp__a__x", "mcp__a__y"}) {
		t.Fatalf("pending %v", r.Pending)
	}
	if !reflect.DeepEqual(r.Known, []string{"read", "shell"}) {
		t.Fatalf("pending tools must stay unknown: %v", r.Known)
	}
}

func TestResolveNewButDisabledIsNotPending(t *testing.T) {
	st := &State{Seeded: true, Known: []string{"read"}}
	r := Resolve([]string{"read", "mcp__a__x"}, map[string]bool{"mcp__a__x": false}, st)
	if len(r.Pending) != 0 || !reflect.DeepEqual(r.Known, []string{"mcp__a__x", "read"}) {
		t.Fatalf("%+v", r)
	}
}

func TestResolveSessionSwitchOnCountsAsAccepted(t *testing.T) {
	st := &State{Seeded: true, Known: []string{"read"}, Overrides: map[string]bool{"mcp__a__x": true}}
	r := Resolve([]string{"read", "mcp__a__x"}, nil, st)
	if len(r.Pending) != 0 || !reflect.DeepEqual(r.Enabled, []string{"mcp__a__x", "read"}) {
		t.Fatalf("%+v", r)
	}
}

func TestResolveKeepsVanishedToolsKnown(t *testing.T) {
	st := &State{Seeded: true, Known: []string{"read", "mcp__gone__x"}}
	r := Resolve([]string{"read"}, nil, st)
	if !reflect.DeepEqual(r.Enabled, []string{"read"}) || !reflect.DeepEqual(r.Known, []string{"mcp__gone__x", "read"}) {
		t.Fatalf("%+v", r)
	}
}

func TestAccept(t *testing.T) {
	st := &State{Seeded: true, Known: []string{"read"}}
	st.Accept([]string{"mcp__a__x", "read"})
	if !reflect.DeepEqual(st.Known, []string{"mcp__a__x", "read"}) {
		t.Fatalf("%v", st.Known)
	}
}

func TestSetOverride(t *testing.T) {
	m := SetOverride(nil, []string{"a", "b"}, on(false))
	if !reflect.DeepEqual(m, map[string]bool{"a": false, "b": false}) {
		t.Fatalf("%v", m)
	}
	m = SetOverride(m, []string{"a"}, nil)
	if !reflect.DeepEqual(m, map[string]bool{"b": false}) {
		t.Fatalf("%v", m)
	}
	if m = SetOverride(m, []string{"b"}, nil); m != nil {
		t.Fatalf("empty overrides collapse to nil: %v", m)
	}
}
