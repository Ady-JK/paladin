package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readReq(t *testing.T, path, ws string) (Request, bool) {
	t.Helper()
	return Classify("read", args(map[string]any{"path": path}), ws)
}

func TestClassifyReadOnlyGatesOutsideWorkspace(t *testing.T) {
	ws, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(other, "secret"), []byte("s"), 0o644)
	if _, gated := readReq(t, filepath.Join(ws, "a.txt"), ws); gated {
		t.Fatal("inside read must not be gated")
	}
	req, gated := readReq(t, filepath.Join(other, "secret"), ws)
	if !gated || req.Kind != KindRead || !req.Outside || req.Path != filepath.Join(other, "secret") {
		t.Fatalf("outside read %+v %v", req, gated)
	}
	// A symlink inside the workspace that points outside is an outside read.
	if err := os.Symlink(filepath.Join(other, "secret"), filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	req, gated = readReq(t, filepath.Join(ws, "link"), ws)
	if !gated || req.Kind != KindRead || !strings.HasSuffix(req.Real, "secret") {
		t.Fatalf("symlink read %+v %v", req, gated)
	}
}

func TestDecideRead(t *testing.T) {
	ws, other := t.TempDir(), t.TempDir()
	target := filepath.Join(other, "secret")
	os.WriteFile(target, []byte("s"), 0o644)
	req, _ := readReq(t, target, ws)
	d := Decide(Auto, req, nil)
	if d.Verdict != Ask || len(d.Choices) != 5 || len(d.Remember) != 1 ||
		d.Remember[0].Tool != ToolRead || d.Remember[0].Pattern != other {
		t.Fatalf("outside read asks with a folder grant: %+v", d)
	}
	for _, rule := range []Rule{
		{Tool: ToolRead, Pattern: other, Action: ActionAllow},
		{Tool: ToolWrite, Pattern: other, Action: ActionAllow},
	} {
		if d := Decide(Auto, req, []Rule{rule}); d.Verdict != Allow {
			t.Fatalf("%s rule should allow: %+v", rule.Tool, d)
		}
	}
	if d := Decide(FullAccess, req, nil); d.Verdict != Allow {
		t.Fatalf("full access: %+v", d)
	}
	if d := Decide(FullAccess, req, []Rule{{Tool: ToolRead, Pattern: other, Action: ActionDeny}}); d.Verdict != Deny {
		t.Fatalf("deny wins: %+v", d)
	}
	// Grants never get broader than the file when its folder is home.
	home := t.TempDir()
	defer swapHome(home)()
	os.WriteFile(filepath.Join(home, ".netrc"), []byte("x"), 0o600)
	req, _ = readReq(t, filepath.Join(home, ".netrc"), ws)
	d = Decide(Auto, req, nil)
	if len(d.Remember) != 1 || !strings.HasSuffix(d.Remember[0].Pattern, ".netrc") {
		t.Fatalf("home grant too broad: %+v", d.Remember)
	}
}

func TestReadRuleValidation(t *testing.T) {
	if err := (Rule{Tool: ToolRead, Pattern: "/etc", Action: ActionAllow}).validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Rule{Tool: ToolRead, Pattern: "etc", Action: ActionAllow}).validate(); err == nil {
		t.Fatal("relative read rule accepted")
	}
}

func TestGateRememberedReadGrant(t *testing.T) {
	ws, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(other, "a"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(other, "b"), []byte("b"), 0o644)
	g := newGate(t, Auto)
	g.Workspace = ws
	rec := &recorder{}
	answer(t, g.Broker, "s1", "r1", ReplySession)
	auth := g.Authorize(context.Background(), "r1", "read", args(map[string]any{"path": filepath.Join(other, "a")}), rec.notify)
	if !auth.Run || rec.count() == 0 || rec.evs[0].Kind != KindRead || rec.evs[0].Path == "" {
		t.Fatalf("%+v %+v", auth, rec.evs)
	}
	rec2 := &recorder{}
	if auth := g.Authorize(context.Background(), "r2", "read", args(map[string]any{"path": filepath.Join(other, "b")}), rec2.notify); !auth.Run || rec2.count() != 0 {
		t.Fatalf("session grant should cover the folder: %+v", auth)
	}
	answer(t, g.Broker, "s1", "r3", ReplyDeny)
	auth = g.Authorize(context.Background(), "r3", "read", args(map[string]any{"path": "/etc/hosts"}), func(Approval) {})
	if auth.Run || !strings.Contains(auth.Content, "read /etc/hosts") {
		t.Fatalf("deny %+v", auth)
	}
}

func shellIn(cmd, ws string) Request {
	r, _ := Classify("shell", json.RawMessage(`{"cmd":`+strings.TrimSpace(string(args(cmd)))+`}`), ws)
	return r
}

// Read-only commands auto-run only on workspace paths; naming a path
// outside it (absolute, ~, .., or a variable) asks.
func TestShellReadsOutsideWorkspaceAsk(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	defer swapHome(home)()
	inside := []string{"cat README.md", "ls -la", "grep -r foo src", "cat " + filepath.Join(ws, "x"), "grep foo /dev/null", "ls ./sub/../sub"}
	for _, c := range inside {
		if d := Decide(Auto, shellIn(c, ws), nil); d.Verdict != Allow {
			t.Fatalf("%q should run: %+v", c, d)
		}
	}
	outside := map[string]string{
		"cat /etc/hosts":          "/etc/hosts",
		"ls ~/.ssh":               filepath.Join(home, ".ssh"),
		"head ../other/file":      filepath.Join(filepath.Dir(ws), "other"),
		"grep --file=/etc/x y":    "/etc/x",
		"cat a.txt && cat /etc/x": "/etc/x",
	}
	for c, want := range outside {
		d := Decide(Auto, shellIn(c, ws), nil)
		if d.Verdict != Ask || !strings.Contains(strings.Join(d.Reasons, " "), "outside the workspace") {
			t.Fatalf("%q should ask: %+v", c, d)
		}
		found := false
		for _, r := range d.Remember {
			found = found || (r.Tool == ToolRead && (pathMatches(r.Pattern, want) || pathMatches(want, r.Pattern)))
		}
		if !found {
			t.Fatalf("%q remember %+v, want a read grant near %s", c, d.Remember, want)
		}
	}
	// A variable can't be resolved: ask once, never remember.
	d := Decide(Auto, shellIn("echo $HOME", ws), nil)
	if d.Verdict != Ask || len(d.Remember) != 0 || strings.Join(d.Choices, ",") != "once,deny" {
		t.Fatalf("variable read %+v", d)
	}
	// Read grants let it through; deny-read rules block it.
	if d := Decide(Auto, shellIn("cat /etc/hosts", ws), []Rule{{Tool: ToolRead, Pattern: "/etc", Action: ActionAllow}}); d.Verdict != Allow {
		t.Fatalf("granted read %+v", d)
	}
	if d := Decide(FullAccess, shellIn("cat /etc/hosts", ws), []Rule{{Tool: ToolRead, Pattern: "/etc", Action: ActionDeny}}); d.Verdict != Deny {
		t.Fatalf("denied read %+v", d)
	}
	// Full access still runs everything not denied.
	if d := Decide(FullAccess, shellIn("cat /etc/hosts", ws), nil); d.Verdict != Allow {
		t.Fatalf("full access %+v", d)
	}
}

func swapHome(dir string) func() {
	old := homeDir
	homeDir = func() (string, error) { return dir, nil }
	return func() { homeDir = old }
}
