package permission

import (
	"encoding/json"
	"testing"
)

func args(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestClassifyShell(t *testing.T) {
	req, gated := Classify("shell", args(map[string]any{"cmd": "git status && ls -la"}), "/ws")
	if !gated || req.Kind != KindShell || !req.Safe || req.Destructive {
		t.Fatalf("got %+v gated=%v", req, gated)
	}
	req, _ = Classify("shell", args(map[string]any{"cmd": "npm test -- --watch=false"}), "/ws")
	if req.Safe || req.Destructive {
		t.Fatalf("npm test is neither safe nor destructive: %+v", req)
	}
	// status/cancel on a background job never need approval.
	if _, gated := Classify("shell", args(map[string]any{"action": "status", "job_id": "j1"}), "/ws"); gated {
		t.Fatal("status should not be gated")
	}
}

func TestClassifyWrites(t *testing.T) {
	req, gated := Classify("edit", args(map[string]any{"path": "/ws/src/a.go"}), "/ws")
	if !gated || req.Kind != KindWrite || req.Outside || req.Path != "/ws/src/a.go" {
		t.Fatalf("inside edit: %+v", req)
	}
	req, _ = Classify("write", args(map[string]any{"path": "/etc/hosts"}), "/ws")
	if !req.Outside {
		t.Fatal("outside write not flagged")
	}
	req, _ = Classify("write", args(map[string]any{"path": "/ws/../etc/x"}), "/ws")
	if !req.Outside {
		t.Fatal(".. escape not flagged")
	}
	for _, name := range []string{"read", "todo", "ask", "memory", "skill", "session_search"} {
		if _, gated := Classify(name, args(map[string]any{"path": "/etc/hosts"}), "/ws"); gated {
			t.Errorf("%s should not be gated", name)
		}
	}
}

func TestDestructive(t *testing.T) {
	yes := []string{
		"rm -rf build", "rm -r dist", "rm -fr /", "rm --recursive x", "sudo apt install x",
		"git push --force origin main", "git push -f", "git reset --hard HEAD~1", "git clean -fdx",
		"git checkout -- .", "git branch -D feature", "find . -name '*.o' -delete",
		"dd if=/dev/zero of=/dev/sda", "mkfs.ext4 /dev/sdb1", "chmod -R 777 .", "ls && rm -rf /tmp/x",
		"echo y | xargs rm -rf", "git stash clear", "shred secret.txt", "FOO=1 rm -rf x",
	}
	no := []string{"rm file.txt", "git push origin main", "git reset HEAD file", "git checkout main",
		"ls -rf", "git clean -n", "find . -name x", "chmod +x run.sh"}
	for _, c := range yes {
		if !isDestructive(c) {
			t.Errorf("want destructive: %q", c)
		}
	}
	for _, c := range no {
		if isDestructive(c) {
			t.Errorf("not destructive: %q", c)
		}
	}
}

func TestSuggestedPrefix(t *testing.T) {
	cases := map[string]string{
		"npm test -- --watch":     "npm test",
		"pytest tests/ -q":        "pytest",
		"FOO=1 go build ./...":    "go build",
		"/usr/local/bin/make all": "make all",
		"python scripts/x.py":     "python",
	}
	for cmd, want := range cases {
		segs, err := splitCommand(cmd)
		if err != nil || len(segs) != 1 {
			t.Fatalf("%q: %v", cmd, err)
		}
		if got := prefixOf(segs[0]); got != want {
			t.Errorf("%q: got %q want %q", cmd, got, want)
		}
	}
}
