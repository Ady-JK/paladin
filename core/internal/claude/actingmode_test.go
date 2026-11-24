package claude

import (
	"errors"
	"testing"
)

func TestChooseActingMode(t *testing.T) {
	fail := errors.New("disabled")
	tests := []struct {
		name        string
		current     string
		autoErr     error
		want        string
		wantSetAuto bool
	}{
		{"bypass enabled is kept", "bypassPermissions", nil, "bypassPermissions", false},
		{"bypass disabled by the org falls back to auto", "default", nil, "auto", true},
		{"neither bypass nor auto leaves the default mode", "default", fail, "default", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calledAuto := false
			got := chooseActingMode(tc.current, func() error { calledAuto = true; return tc.autoErr })
			if got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
			if calledAuto != tc.wantSetAuto {
				t.Fatalf("set auto called = %v, want %v", calledAuto, tc.wantSetAuto)
			}
		})
	}
}
