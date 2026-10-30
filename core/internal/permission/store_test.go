package permission

import (
	"context"
	"encoding/json"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreScopes(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if err := s.Add(ScopeGlobal, "", "", Rule{Tool: ToolShell, Pattern: "make", Action: ActionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ScopeFolder, "/proj/a", "", Rule{Tool: ToolShell, Pattern: "npm test", Action: ActionAllow}); err != nil {
		t.Fatal(err)
	}
	_ = s.Add(ScopeFolder, "/proj/a", "", Rule{Tool: ToolShell, Pattern: "npm test", Action: ActionAllow})
	if err := s.Add(ScopeSession, "/proj/a", "s1", Rule{Tool: ToolShell, Pattern: "cargo", Action: ActionAllow}); err != nil {
		t.Fatal(err)
	}
	got := s.Effective("/proj/a", "s1")
	if len(got) != 3 {
		t.Fatalf("want 3 deduped rules, got %+v", got)
	}
	if len(s.Effective("/proj/b", "s2")) != 1 {
		t.Fatal("other folder/session should only see global")
	}
	// Persisted across instances (session rules are not).
	s2 := NewStore(root)
	if len(s2.Effective("/proj/a", "s1")) != 2 {
		t.Fatalf("reload: %+v", s2.Effective("/proj/a", "s1"))
	}
	folders := s2.Folders()
	if len(folders) != 1 || folders[0].Cwd != "/proj/a" {
		t.Fatalf("folders: %+v", folders)
	}
	if err := s2.Set(ScopeFolder, "/proj/a", nil); err != nil {
		t.Fatal(err)
	}
	if len(s2.Effective("/proj/a", "")) != 1 {
		t.Fatal("set nil clears the folder")
	}
}

func TestStoreValidatesRules(t *testing.T) {
	s := NewStore(t.TempDir())
	bad := []Rule{
		{Tool: "net", Pattern: "x", Action: ActionAllow},
		{Tool: ToolShell, Pattern: " ", Action: ActionAllow},
		{Tool: ToolShell, Pattern: "x", Action: "maybe"},
		{Tool: ToolWrite, Pattern: "relative/dir", Action: ActionAllow},
	}
	for _, r := range bad {
		if err := s.Set(ScopeGlobal, "", []Rule{r}); err == nil {
			t.Errorf("want error for %+v", r)
		}
	}
}

func TestBrokerResolveAndCancel(t *testing.T) {
	b := NewBroker()
	ch := b.Open("s", "c1")
	if !b.Pending("s", "c1") {
		t.Fatal("not pending")
	}
	go func() { _ = b.Resolve("s", "c1", ReplySession) }()
	reply, err := b.Wait(context.Background(), "s", "c1", ch)
	if err != nil || reply != ReplySession {
		t.Fatal(reply, err)
	}
	if err := b.Resolve("s", "c1", ReplyOnce); err == nil {
		t.Fatal("resolving a settled call should fail")
	}
	if err := b.Resolve("s", "c9", "bogus"); err == nil {
		t.Fatal("bad reply should fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch = b.Open("s", "c2")
	cancel()
	if _, err := b.Wait(ctx, "s", "c2", ch); err == nil {
		t.Fatal("cancel should end the wait")
	}
	if b.Pending("s", "c2") {
		t.Fatal("cancelled wait left an entry")
	}
}

type recorder struct {
	mu  sync.Mutex
	evs []Approval
}

func (r *recorder) notify(a Approval) { r.mu.Lock(); r.evs = append(r.evs, a); r.mu.Unlock() }
func (r *recorder) last() Approval    { r.mu.Lock(); defer r.mu.Unlock(); return r.evs[len(r.evs)-1] }
func (r *recorder) count() int        { r.mu.Lock(); defer r.mu.Unlock(); return len(r.evs) }

func answer(t *testing.T, b *Broker, sid, id, reply string) {
	t.Helper()
	go func() {
		for i := 0; i < 200; i++ {
			if b.Pending(sid, id) {
				_ = b.Resolve(sid, id, reply)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func newGate(t *testing.T, preset Preset) *Gate {
	return &Gate{Preset: preset, Workspace: "/ws", SessionID: "s1", Store: NewStore(t.TempDir()), Broker: NewBroker(), SandboxKind: "seatbelt"}
}

func TestGateAllowsSafeWithoutAsking(t *testing.T) {
	g := newGate(t, Auto)
	rec := &recorder{}
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "ls"}), rec.notify)
	if !auth.Run || auth.Sandbox == nil || auth.Sandbox.Writable[0] != "/ws" || rec.count() != 0 {
		t.Fatalf("%+v events=%d", auth, rec.count())
	}
}

func TestGateNoSandboxWhenUnavailable(t *testing.T) {
	g := newGate(t, Auto)
	g.SandboxKind = ""
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "ls"}), func(Approval) {})
	if !auth.Run || auth.Sandbox != nil {
		t.Fatalf("%+v", auth)
	}
}

func TestGateAskApproveRemembersForSession(t *testing.T) {
	g := newGate(t, Auto)
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplySession)
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "npm test"}), rec.notify)
	if !auth.Run {
		t.Fatalf("%+v", auth)
	}
	if rec.evs[0].State != StatePending || rec.evs[0].Command != "npm test" || rec.last().State != StateApproved || rec.last().Decision != ReplySession {
		t.Fatalf("events %+v", rec.evs)
	}
	// Second call: no prompt.
	rec2 := &recorder{}
	auth = g.Authorize(context.Background(), "c2", "shell", args(map[string]any{"cmd": "npm test -- -u"}), rec2.notify)
	if !auth.Run || rec2.count() != 0 {
		t.Fatalf("session rule ignored: %+v %d", auth, rec2.count())
	}
}

func TestGateFolderRulePersists(t *testing.T) {
	g := newGate(t, Auto)
	answer(t, g.Broker, "s1", "c1", ReplyFolder)
	g.Authorize(context.Background(), "c1", "write", args(map[string]any{"path": "/etc/hosts"}), func(Approval) {})
	rules := g.Store.Effective("/ws", "other-session")
	if len(rules) != 1 || rules[0].Pattern != "/etc" || rules[0].Tool != ToolWrite {
		t.Fatalf("%+v", rules)
	}
}

func TestGateDenyTellsTheModel(t *testing.T) {
	g := newGate(t, Auto)
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplyDeny)
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "npm publish"}), rec.notify)
	if auth.Run || !strings.Contains(auth.Content, "denied") || !json.Valid([]byte(auth.Content)) {
		t.Fatalf("%+v", auth)
	}
	if rec.last().State != StateDenied {
		t.Fatalf("%+v", rec.last())
	}
}

func TestGateStopEndsWait(t *testing.T) {
	g := newGate(t, Auto)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	auth := g.Authorize(ctx, "c1", "shell", args(map[string]any{"cmd": "npm test"}), func(Approval) {})
	if auth.Run || !strings.Contains(auth.Content, "interrupted") {
		t.Fatalf("%+v", auth)
	}
}

func TestGateRuleDenyNeedsNoPrompt(t *testing.T) {
	g := newGate(t, FullAccess)
	_ = g.Store.Add(ScopeGlobal, "", "", Rule{Tool: ToolShell, Pattern: "curl", Action: ActionDeny})
	rec := &recorder{}
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "curl x"}), rec.notify)
	if auth.Run || rec.count() != 0 || !strings.Contains(auth.Content, "rule") {
		t.Fatalf("%+v", auth)
	}
}

func TestGateEscalation(t *testing.T) {
	g := newGate(t, Auto)
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplyOnce)
	pol := &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite, Writable: []string{"/ws"}}
	esc := g.Escalate(context.Background(), "c1", "shell", args(map[string]any{"cmd": "npm i -g x"}),
		Blocked{Policy: pol, Output: "Operation not permitted"}, rec.notify)
	if !esc.Run || esc.Sandbox != nil || rec.evs[0].Kind != KindEscalate || len(rec.evs[0].Choices) != 2 {
		t.Fatalf("esc=%+v %+v", esc, rec.evs)
	}
	g.Preset = FullAccess
	if g.Escalate(context.Background(), "c2", "shell", args(map[string]any{"cmd": "x"}), Blocked{Policy: pol}, rec.notify).Run {
		t.Fatal("full access never sandboxes, so never escalates")
	}
}

func TestGateAddsExtraWritableRoots(t *testing.T) {
	g := newGate(t, Auto)
	g.ExtraWritable = []string{"/repo/.git"}
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "ls"}), func(Approval) {})
	if auth.Sandbox == nil || strings.Join(auth.Sandbox.Writable, ",") != "/ws,/repo/.git" {
		t.Fatalf("%+v", auth.Sandbox)
	}
}
