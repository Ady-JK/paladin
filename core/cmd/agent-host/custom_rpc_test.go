package main

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func TestCustomizeRPCs(t *testing.T) {
	rt := testRuntime(t)
	list := decodeAs[[]runtime.CustomItem](t, callUnary(rt, "1", "customList", map[string]any{}))
	if len(list) < 20 {
		t.Fatalf("only %d items", len(list))
	}
	got := decodeAs[runtime.CustomState](t, callUnary(rt, "2", "customGet", map[string]any{"id": "reminder.plan"}))
	if got.Customized || got.Value != got.DefaultVal || len(got.Vars) != 1 {
		t.Fatalf("get %+v", got)
	}
	set := decodeAs[runtime.CustomState](t, callUnary(rt, "3", "customSet", map[string]any{"id": "reminder.plan", "text": "Finish {{steps}}."}))
	if !set.Customized || set.Value != "Finish {{steps}}." {
		t.Fatalf("set %+v", set)
	}
	reset := decodeAs[runtime.CustomState](t, callUnary(rt, "4", "customReset", map[string]any{"id": "reminder.plan"}))
	if reset.Customized {
		t.Fatalf("reset %+v", reset)
	}
	if ok := decodeAs[bool](t, callUnary(rt, "5", "skillEnabledSet", map[string]any{"name": "web-research", "enabled": false})); !ok {
		t.Fatal("skillEnabledSet")
	}
	for _, it := range decodeAs[[]runtime.CustomItem](t, callUnary(rt, "6", "customList", map[string]any{})) {
		if it.ID == "skill.web-research" && (it.Enabled == nil || *it.Enabled) {
			t.Fatalf("skill still on: %+v", it)
		}
	}
}
