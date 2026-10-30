package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func TestHooksMethods(t *testing.T) {
	rt := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	ws := t.TempDir()
	decode := func(r Response) runtime.HooksOverview {
		t.Helper()
		mustOK(t, r)
		var ov runtime.HooksOverview
		data, _ := json.Marshal(r.Result)
		if err := json.Unmarshal(data, &ov); err != nil {
			t.Fatal(err)
		}
		return ov
	}
	ov := decode(callUnary(rt, "1", "hooksList", map[string]any{"cwd": ws}))
	if len(ov.Sources) != 4 || len(ov.Events) != 7 {
		t.Fatalf("%+v", ov)
	}
	mustFail(t, callUnary(rt, "2", "hooksSetConfig", map[string]any{"scope": "global", "cwd": ws, "text": `{"hooks":`}), "invalid JSON")
	ov = decode(callUnary(rt, "3", "hooksSetConfig", map[string]any{"scope": "folder", "cwd": ws, "text": `{"hooks":{"Stop":[{"hooks":[{"command":"true"}]}]}}`}))
	var key, path string
	for _, s := range ov.Sources {
		if s.Scope == "folder" && s.Format == "paladin" {
			if !s.Trusted || len(s.Handlers) != 1 {
				t.Fatalf("%+v", s)
			}
			key, path = s.Handlers[0].Key, s.Path
		}
	}
	_ = decode(callUnary(rt, "4", "hooksToggle", map[string]any{"cwd": ws, "key": key, "enabled": false}))
	ov = decode(callUnary(rt, "5", "hooksTrust", map[string]any{"cwd": ws, "path": path, "trust": false}))
	for _, s := range ov.Sources {
		if s.Path == path && (s.Trusted || s.Handlers[0].Enabled) {
			t.Fatalf("%+v", s)
		}
	}
}
