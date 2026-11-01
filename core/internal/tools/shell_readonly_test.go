package tools

import "testing"

func TestShellReadOnly(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{`{"command":"ls -la"}`, true},
		{`{"command":"git status --short"}`, true},
		{`{"command":"rg TODO core"}`, true},
		{`{"command":"echo hi > /dev/null"}`, true},
		{`{"command":"go build ./..."}`, false},
		{`{"command":"go test ./..."}`, false},
		{`{"command":"touch x"}`, false},
		{`{"command":"ls","background":true}`, false},
		{`{"action":"status","job_id":"job-1"}`, true},
		{`{"action":"cancel","job_id":"job-1"}`, true},
		{`{}`, false},
		{`not json`, false},
	}
	for _, tc := range cases {
		if got := ShellReadOnly(tc.args); got != tc.want {
			t.Errorf("ShellReadOnly(%s) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
