package codex

import (
	"reflect"
	"testing"
)

func TestChooseFallsBackToASaferPresetNeverALooserOne(t *testing.T) {
	managed := Requirements{Sandboxes: []string{"read-only", "workspace-write"}, Approvals: []string{"on-request", "untrusted"}}
	cases := []struct {
		reqs     Requirements
		preset   string
		sandbox  string
		approval string
		ok       bool
	}{
		{Requirements{}, PresetFullAccess, "danger-full-access", "never", true},
		{Requirements{}, PresetAuto, "workspace-write", "on-request", true},
		{managed, PresetFullAccess, "workspace-write", "on-request", true}, // the enterprise case
		{managed, PresetAuto, "workspace-write", "on-request", true},
		{managed, PresetReadOnly, "read-only", "on-request", true},
		{Requirements{Sandboxes: []string{"read-only"}}, PresetFullAccess, "read-only", "on-request", true},
		{Requirements{Sandboxes: []string{"workspace-write"}, Approvals: []string{"untrusted"}}, PresetAuto, "workspace-write", "untrusted", true},
		{Requirements{Sandboxes: []string{"danger-full-access"}}, PresetAuto, "", "", false},
	}
	for _, tc := range cases {
		got, ok := tc.reqs.Choose(tc.preset)
		if ok != tc.ok || got.Sandbox != tc.sandbox || got.Approval != tc.approval {
			t.Errorf("%+v %s: got %+v ok=%v, want %s/%s ok=%v", tc.reqs, tc.preset, got, ok, tc.sandbox, tc.approval, tc.ok)
		}
	}
}

func TestPresetsListsWhatIsAllowed(t *testing.T) {
	if got := (Requirements{}).Presets(); !reflect.DeepEqual(got, []string{"read-only", "full-access"}) {
		t.Fatalf("unrestricted: %v", got)
	}
	managed := Requirements{Sandboxes: []string{"read-only", "workspace-write"}, Approvals: []string{"on-request", "untrusted"}}
	if got := managed.Presets(); !reflect.DeepEqual(got, []string{"read-only"}) {
		t.Fatalf("managed: %v", got)
	}
}

func TestSandboxPolicyMatchesTheThreadSandbox(t *testing.T) {
	for sandbox, want := range map[string]string{"read-only": "readOnly", "workspace-write": "workspaceWrite", "danger-full-access": "dangerFullAccess"} {
		if got := (Choice{Sandbox: sandbox}).SandboxPolicy()["type"]; got != want {
			t.Errorf("%s -> %v, want %s", sandbox, got, want)
		}
	}
}
