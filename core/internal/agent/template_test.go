package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type fakeSessions struct {
	sess    transcript.Session
	err     error
	lastCwd string
	lastID  string
}

func (f *fakeSessions) LoadSession(cwd, sessionID string) (transcript.Session, error) {
	f.lastCwd, f.lastID = cwd, sessionID
	if f.err != nil {
		return transcript.Session{}, f.err
	}
	return f.sess, nil
}

type fakeProviders struct {
	provider inference.Provider
	err      error
	name     string
	model    string
}

func (f *fakeProviders) ResolveProvider(name, model string) (inference.Provider, error) {
	f.name, f.model = name, model
	if f.err != nil {
		return nil, f.err
	}
	return f.provider, nil
}

type fakePrompts struct {
	base     string
	guidance string
}

func (f fakePrompts) SystemPrompt(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return f.base
}
func (f fakePrompts) ToolGuidance() string { return f.guidance }

func testDeps(t *testing.T, sess transcript.Session) (Deps, *fakeSessions, *fakeProviders, *fakePrompts) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sessions := &fakeSessions{sess: sess}
	providers := &fakeProviders{provider: &mockProvider{}}
	prompts := &fakePrompts{base: "runtime base", guidance: "runtime guidance"}
	registry := tools.BuiltinRegistry()
	shell, _ := registry.Get("shell")
	shellTool, _ := shell.(*tools.ShellTool)
	d := Deps{
		Sessions:  sessions,
		Providers: providers,
		Prompts:   prompts,
		Tools:     registry,
		Shell:     shellTool,
		MaxRounds: 42,
		Context:   ctx,
		Cancel:    cancel,
		Tx:        make(chan ChatChunk, 8),
		Done:      make(chan struct{}),
	}
	return d, sessions, providers, prompts
}

func TestBuildValidation(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000020")
	d, _, _, _ := testDeps(t, sess)
	tpl := DefaultTemplate()

	if _, err := tpl.Build(d, Request{Message: "hi", Cwd: "/w"}); err == nil {
		t.Fatal("missing session accepted")
	}
	if _, err := tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Cwd: "/w"}); err == nil {
		t.Fatal("missing message accepted")
	}
	// Resume lifts the message requirement.
	if _, err := tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Resume: true, Cwd: "/w"}); err != nil {
		t.Fatalf("resume rejected: %v", err)
	}

	// Missing runtime dependencies.
	broken := d
	broken.Sessions = nil
	if _, err := tpl.Build(broken, Request{SessionID: sess.Meta.SessionID, Message: "hi"}); err == nil {
		t.Fatal("nil sessions accepted")
	}
	broken = d
	broken.Providers = nil
	if _, err := tpl.Build(broken, Request{SessionID: sess.Meta.SessionID, Message: "hi"}); err == nil {
		t.Fatal("nil providers accepted")
	}
	broken = d
	broken.Prompts = nil
	if _, err := tpl.Build(broken, Request{SessionID: sess.Meta.SessionID, Message: "hi"}); err == nil {
		t.Fatal("nil prompts accepted")
	}
	broken = d
	broken.Tools = nil
	if _, err := tpl.Build(broken, Request{SessionID: sess.Meta.SessionID, Message: "hi"}); err == nil {
		t.Fatal("nil tools accepted")
	}

	// Source errors propagate unchanged.
	badSess := &fakeSessions{err: errors.New("no such session")}
	broken = d
	broken.Sessions = badSess
	if _, err := tpl.Build(broken, Request{SessionID: "x", Message: "hi"}); err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Fatalf("got %v", err)
	}
	badProv := &fakeProviders{err: errors.New("no provider")}
	broken = d
	broken.Providers = badProv
	if _, err := tpl.Build(broken, Request{SessionID: sess.Meta.SessionID, Message: "hi"}); err == nil || !strings.Contains(err.Error(), "no provider") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildResolvesEverything(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000021")
	if err := sess.Append(transcript.RoleUser, "earlier"); err != nil {
		t.Fatal(err)
	}
	d, sessions, providers, _ := testDeps(t, sess)
	d.MaxRounds = 17
	providerName, modelName := "openrouter", "model-x"
	a, err := DefaultTemplate().Build(d, Request{
		SessionID: sess.Meta.SessionID, Message: "hi", Cwd: "/proj",
		Provider: &providerName, Model: &modelName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sessions.lastCwd != "/proj" || sessions.lastID != sess.Meta.SessionID {
		t.Fatalf("session source got %q %q", sessions.lastCwd, sessions.lastID)
	}
	if providers.name != "openrouter" || providers.model != "model-x" {
		t.Fatalf("provider source got %q %q", providers.name, providers.model)
	}
	if len(a.history) != 1 || a.history[0].Content != "earlier" {
		t.Fatalf("history not resolved: %+v", a.history)
	}
	if a.maxRounds != 17 {
		t.Fatalf("max rounds %d", a.maxRounds)
	}
	if a.session.Meta.SessionID != sess.Meta.SessionID {
		t.Fatalf("session %+v", a.session.Meta)
	}
	if a.currentMessage != "hi" || a.currentResume {
		t.Fatalf("input %q %v", a.currentMessage, a.currentResume)
	}
	if len(a.toolDefs) != 4 {
		t.Fatalf("default template should use every tool: %+v", a.toolDefs)
	}
	if a.shell == nil {
		t.Fatal("default template must keep the shared shell")
	}
	if !strings.Contains(a.system, "runtime base") || !strings.Contains(a.system, "runtime guidance") {
		t.Fatalf("system prompt not composed: %q", a.system)
	}
	// The tool context is derived from the session, not the request.
	if a.toolCtx.Workspace != sess.Meta.Cwd || a.toolCtx.SessionDir != sess.Dir {
		t.Fatalf("tool ctx %+v", a.toolCtx)
	}
}

func TestBuildPromptPrecedence(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000022")
	d, _, _, _ := testDeps(t, sess)
	tpl := Template{Name: "custom"}

	// The runtime default (the prompt files) is used as-is.
	a, err := tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.system, "runtime base") || !strings.Contains(a.system, "runtime guidance") {
		t.Fatalf("runtime prompt lost: %q", a.system)
	}

	// Request beats the runtime default.
	override := "request base"
	a, err = tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi", SystemPrompt: &override})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.system, "request base") || strings.Contains(a.system, "runtime base") {
		t.Fatalf("request prompt lost: %q", a.system)
	}

	// Whitespace-only overrides do not replace the file contents.
	blank := "   "
	a, err = tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi", SystemPrompt: &blank})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.system, "runtime base") {
		t.Fatalf("blank override clobbered runtime prompt: %q", a.system)
	}
}

func TestBuildToolSubset(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000023")
	d, _, _, _ := testDeps(t, sess)

	tpl := Template{Name: "reader", Tools: []string{"read"}}
	a, err := tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.toolDefs) != 1 || a.toolDefs[0].Name != "read" {
		t.Fatalf("subset lost: %+v", a.toolDefs)
	}
	if a.shell != nil {
		t.Fatal("shell should be dropped from a read-only template")
	}
	if a.tools.Len() != 1 {
		t.Fatalf("registry len %d", a.tools.Len())
	}

	// A template that keeps shell shares the live instance so background
	// jobs survive across turns.
	tpl = Template{Name: "sheller", Tools: []string{"shell"}}
	a, err = tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := d.Tools.Get("shell")
	if a.shell == nil || a.shell != got {
		t.Fatal("shell instance not shared")
	}

	// Unknown names fail loudly.
	tpl = Template{Name: "typo", Tools: []string{"raed"}}
	if _, err := tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildMaxRounds(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000024")
	d, _, _, _ := testDeps(t, sess)
	req := Request{SessionID: sess.Meta.SessionID, Message: "hi"}

	a, err := (Template{Name: "t", MaxRounds: 7}).Build(d, req)
	if err != nil {
		t.Fatal(err)
	}
	if a.maxRounds != 7 {
		t.Fatalf("got %d", a.maxRounds)
	}

	// Template caps are clamped like runtime settings.
	a, err = (Template{Name: "t", MaxRounds: 9999}).Build(d, req)
	if err != nil {
		t.Fatal(err)
	}
	if a.maxRounds != config.MaxMaxToolRounds {
		t.Fatalf("got %d", a.maxRounds)
	}

	// Zero keeps the runtime value.
	a, err = (Template{Name: "t"}).Build(d, req)
	if err != nil {
		t.Fatal(err)
	}
	if a.maxRounds != 42 {
		t.Fatalf("got %d", a.maxRounds)
	}
}

func TestRegistryRegisterGetNamesDefault(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Default(); ok {
		t.Fatal("empty registry has a default")
	}
	if err := r.Register(Template{Name: "  "}); err == nil {
		t.Fatal("blank name accepted")
	}
	first := Template{Name: "first"}
	if err := r.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Template{Name: "default", MaxRounds: 3}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Template{Name: "first", MaxRounds: 9}); err != nil {
		t.Fatal(err)
	}
	if names := r.Names(); len(names) != 2 || names[0] != "first" || names[1] != "default" {
		t.Fatalf("got %+v", names)
	}
	got, ok := r.Get("first")
	if !ok || got.MaxRounds != 9 {
		t.Fatalf("got %+v %v", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("unknown template found")
	}
	def, ok := r.Default()
	if !ok || def.Name != "default" {
		t.Fatalf("got %+v %v", def, ok)
	}

	// Without "default", the first registered template wins.
	r2 := NewRegistry()
	_ = r2.Register(Template{Name: "alpha"})
	_ = r2.Register(Template{Name: "beta"})
	def, ok = r2.Default()
	if !ok || def.Name != "alpha" {
		t.Fatalf("got %+v %v", def, ok)
	}
}

func TestDefaultTemplateName(t *testing.T) {
	if DefaultTemplate().Name != "default" {
		t.Fatalf("got %+v", DefaultTemplate())
	}
}
