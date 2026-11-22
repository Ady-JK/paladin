package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func openCodex(t *testing.T, rt *AgentRuntime) (id, cwd string) {
	t.Helper()
	cwd = t.TempDir()
	opened, err := rt.OpenSessionWith("", cwd, OpenOpts{Harness: HarnessCodex, Model: "fake-luna"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Harness != HarnessCodex || opened.Provider != HarnessCodex || opened.Model != "fake-luna" {
		t.Fatalf("opened %+v", opened)
	}
	return opened.SessionID, cwd
}

func submitCodex(t *testing.T, rt *AgentRuntime, id, cwd, message string) <-chan TurnInfo {
	t.Helper()
	provider, model := HarnessCodex, "fake-luna"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Message: message, Cwd: cwd, Provider: &provider, Model: &model, Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return done
}

// awaitTurn waits for a Codex turn with a deadline. A raw <-done has
// none: under suite load the fake's stream teardown can race the
// approval answer, and the receive then hangs to the package's 10m
// cap, turning a slow test into a 10-minute stall.
func awaitTurn(t *testing.T, done <-chan TurnInfo) TurnInfo {
	t.Helper()
	select {
	case info := <-done:
		return info
	case <-time.After(30 * time.Second):
		t.Fatal("the codex turn did not finish")
		return TurnInfo{}
	}
}

func TestCodexStatusListsVisibleModels(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	if st := rt.CodexStatus(false); !st.Installed || st.LoggedIn {
		t.Fatalf("shallow status %+v", st)
	}
	st := rt.CodexStatus(true)
	if !st.Installed || !st.LoggedIn || st.Account != "me@example.com" || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	if len(st.Models) != 1 || st.Models[0].ID != "fake-luna" || len(st.Models[0].Efforts) != 2 {
		t.Fatalf("models %+v (hidden models must not be offered)", st.Models)
	}
}

func TestCodexTurnStreamsPersistsAndKeepsItsThread(t *testing.T) {
	rt, log, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)

	done := submitCodex(t, rt, id, cwd, "list files")
	final := awaitTurn(t, done)
	if final.Error != "" || final.Reply != "Looking now.Done." || !final.Persisted {
		t.Fatalf("final %+v", final)
	}
	evs := log.all()
	if countEvents(evs, "chat/delta") != 3 || countEvents(evs, "chat/reasoning") != 1 || countEvents(evs, "chat/usage") != 1 {
		t.Fatalf("events: %s", describe(evs))
	}
	var running, finished bool
	for _, e := range evs {
		if e.Tool != nil && e.Tool.Name == "shell" {
			running = running || e.Tool.Status == "running"
			finished = finished || (e.Tool.Status == "ok" && strings.Contains(e.Tool.Result, `"exit_code":0`))
		}
	}
	if !running || !finished {
		t.Fatalf("shell tool should show running then ok: %s", describe(evs))
	}

	hist, err := rt.SessionHistory(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	var shape []string
	for _, row := range hist {
		shape = append(shape, row.Role)
	}
	// The thinking shows as its own assistant row before the text.
	if got := strings.Join(shape, ","); got != "user,assistant,assistant,tool,assistant" || hist[1].Kind == nil {
		t.Fatalf("history roles = %s (thinking row kind %v)", got, hist[1].Kind)
	}
	if derefString(hist[3].Name) != "shell" || !strings.Contains(derefString(hist[3].Arguments), `"command":"ls"`) {
		t.Fatalf("tool row %+v", hist[3])
	}

	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.Harness == nil || sess.Meta.Harness.ThreadID != "th-1" {
		t.Fatalf("thread not saved: %+v", sess.Meta.Harness)
	}
	saved, _ := sess.History()
	if saved[1].Reasoning != "thinking" {
		t.Fatalf("assistant row should keep the thinking: %+v", saved[1])
	}
	usage, _ := readUsage(sess)
	if usage.TotalTokens != 107 || usage.CompletionTokens != 7 {
		t.Fatalf("usage %+v", usage)
	}

	// The next turn continues the same Codex thread.
	if final := <-submitCodex(t, rt, id, cwd, "again"); final.Error != "" {
		t.Fatalf("second turn: %+v", final)
	}
	log2 := fakeLog(t, fake)
	if strings.Count(log2, "thread/start") != 1 || strings.Count(log2, "turn/start thread=th-1 model=fake-luna") != 2 {
		t.Fatalf("fake codex saw:\n%s", log2)
	}
}

func TestCodexApprovalCardAnswersTheRequest(t *testing.T) {
	for _, tc := range []struct{ decision, want string }{{"once", "accept"}, {"session", "acceptForSession"}, {"deny", "decline"}} {
		t.Run(tc.decision, func(t *testing.T) {
			rt, log, fake := codexRuntime(t, "approval")
			id, cwd := openCodex(t, rt)
			done := submitCodex(t, rt, id, cwd, "touch x")
			evs := log.waitFor(t, "approval card", func(evs []TurnEvent) bool { return countEvents(evs, "chat/approval") >= 1 })
			var card *TurnEvent
			for i := range evs {
				if evs[i].Approval != nil {
					card = &evs[i]
				}
			}
			ap := card.Approval
			if ap.ID != "cmd-1" || ap.Kind != "shell" || ap.Command != "touch x" || ap.State != "pending" || len(ap.Choices) != 3 {
				t.Fatalf("card %+v", ap)
			}
			if ok, err := rt.AnswerApproval(id, ap.ID, tc.decision); err != nil || !ok {
				t.Fatalf("answer: %v %v", ok, err)
			}
			final := awaitTurn(t, done)
			if final.Error != "" || final.Reply != "after "+tc.want {
				t.Fatalf("final %+v", final)
			}
			if !strings.Contains(fakeLog(t, fake), "approval decision="+tc.want) {
				t.Fatalf("fake codex log:\n%s", fakeLog(t, fake))
			}
		})
	}
}

func TestCodexStopInterruptsTheTurn(t *testing.T) {
	rt, log, fake := codexRuntime(t, "hang")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "sleep")
	log.waitFor(t, "tool running", func(evs []TurnEvent) bool { return countEvents(evs, "chat/tool") >= 1 })
	if !rt.StopIn(id, cwd) {
		t.Fatal("stop found nothing to stop")
	}
	final := awaitTurn(t, done)
	if !final.Stopped || final.Error != "" || !final.Persisted {
		t.Fatalf("final %+v", final)
	}
	if !strings.Contains(fakeLog(t, fake), "turn/interrupt") {
		t.Fatalf("codex was never told to stop:\n%s", fakeLog(t, fake))
	}
	// A command Codex left running is stopped too, as t3code does.
	if !strings.Contains(fakeLog(t, fake), "terminal terminate thread=th-1 process=p-1") {
		t.Fatalf("the running command's process should be terminated:\n%s", fakeLog(t, fake))
	}
	hist, _ := rt.SessionHistory(id, cwd)
	if n := len(hist); n != 3 || derefString(hist[2].Status) != "failed" {
		t.Fatalf("an unfinished tool should be recorded as failed: %+v", hist)
	}
}

func TestCodexFailedTurnReportsTheError(t *testing.T) {
	rt, _, _ := codexRuntime(t, "fail")
	id, cwd := openCodex(t, rt)
	final := <-submitCodex(t, rt, id, cwd, "go")
	if final.Error != "model exploded" {
		t.Fatalf("error = %q", final.Error)
	}
	hist, _ := rt.SessionHistory(id, cwd)
	if len(hist) != 1 || hist[0].Role != "user" {
		t.Fatalf("a failed turn still keeps the user's message: %+v", hist)
	}
}

func TestCodexPermissionPresetsPickTheSandbox(t *testing.T) {
	for _, tc := range []struct{ perm, mode, sandbox, approval string }{
		{"auto", "", "read-only", "on-request"},
		{"read-only", "", "read-only", "on-request"},
		{"auto", "plan", "read-only", "on-request"}, // plan mode is Codex's own, not a sandbox
		{"full-access", "", "danger-full-access", "never"},
	} {
		rt, _, fake := codexRuntime(t, "chat")
		id, cwd := openCodex(t, rt)
		provider, model := HarnessCodex, "fake-luna"
		_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Message: "x", Cwd: cwd, Provider: &provider, Model: &model, Permissions: tc.perm, Mode: tc.mode}, SubmitOpts{})
		if err != nil {
			t.Fatal(err)
		}
		awaitTurn(t, done)
		if want := "sandbox=" + tc.sandbox + " approval=" + tc.approval; !strings.Contains(fakeLog(t, fake), want) {
			t.Fatalf("%s/%s: want %q in\n%s", tc.perm, tc.mode, want, fakeLog(t, fake))
		}
	}
}

func TestCodexThinkingLevelsComeFromTheModel(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	v, err := rt.ThinkingGet(HarnessCodex, "fake-luna")
	if err != nil || len(v.Levels) != 2 || v.Level != "" {
		t.Fatalf("view %+v %v", v, err)
	}
	if _, err := rt.ThinkingSet(HarnessCodex, "fake-luna", "ultra"); err == nil {
		t.Fatal("a level the model does not take must be refused")
	}
	if v, err = rt.ThinkingSet(HarnessCodex, "fake-luna", "low"); err != nil || v.Level != "low" {
		t.Fatalf("set %+v %v", v, err)
	}
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "x")
	if !strings.Contains(fakeLog(t, fake), "effort=low") {
		t.Fatalf("effort not sent:\n%s", fakeLog(t, fake))
	}
}

func TestCodexProviderIsRefusedOnAPaladinSession(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	paladinSession := openIn(t, rt)
	provider := HarnessCodex
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: paladinSession, Message: "x", Cwd: cwdOf(t, rt, paladinSession), Provider: &provider}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if final := awaitTurn(t, done); !strings.Contains(final.Error, "start a Codex session") {
		t.Fatalf("error = %q", final.Error)
	}
}

func TestForkedCodexSessionNeverSharesTheSourcesThread(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first question")
	fork, err := rt.ForkSession(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Harness != HarnessCodex {
		t.Fatalf("fork lost its harness: %+v", fork)
	}
	forked, err := rt.transcripts.Load(cwd, fork.SessionID)
	if err != nil || forked.Meta.Harness == nil || forked.Meta.Harness.ThreadID != "" {
		t.Fatalf("a fork must not share the source's thread: %+v %v", forked.Meta.Harness, err)
	}
	if of := forked.Meta.Harness.ForkOf; of == nil || of.ThreadID != "th-1" || of.LastTurn != "tu-1" {
		t.Fatalf("the fork should remember where it continues from: %+v", forked.Meta.Harness)
	}
	<-submitCodex(t, rt, fork.SessionID, cwd, "follow up")
	log := fakeLog(t, fake)
	if !strings.Contains(log, "turn/start thread=th-2 ") || strings.Contains(log, "User: first question") {
		t.Fatalf("the fork runs on a thread of its own and does not retell the conversation:\n%s", log)
	}
	forked, _ = rt.transcripts.Load(cwd, fork.SessionID)
	if forked.Meta.Harness.ThreadID != "th-2" || forked.Meta.Harness.ForkOf != nil {
		t.Fatalf("once forked, the session keeps its new thread: %+v", forked.Meta.Harness)
	}
}

// An enterprise-managed Codex refuses full access outright; Paladin must ask
// for the closest mode it accepts instead of failing the session.
func TestCodexManagedRequirementsClampFullAccess(t *testing.T) {
	t.Setenv(fakeCodexManaged, "1")
	rt, _, fake := codexRuntime(t, "chat")
	st := rt.CodexStatus(true)
	if len(st.Permissions) != 1 || st.Permissions[0] != "read-only" {
		t.Fatalf("status should offer only what Codex accepts: %+v", st.Permissions)
	}
	id, cwd := openCodex(t, rt)
	provider, model := HarnessCodex, "fake-luna"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Message: "x", Cwd: cwd, Provider: &provider, Model: &model, Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if final := awaitTurn(t, done); final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	if want := "sandbox=workspace-write approval=on-request"; !strings.Contains(fakeLog(t, fake), want) {
		t.Fatalf("want %q in\n%s", want, fakeLog(t, fake))
	}
}

func TestCodexSessionsRefuseSkillsAndPromptCommands(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	provider, command := HarnessCodex, "review"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Message: "x", Cwd: cwd, Provider: &provider, Command: &command}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if final := awaitTurn(t, done); !strings.Contains(final.Error, "not available in a codex session") {
		t.Fatalf("error = %q", final.Error)
	}
}

// A file change shows as the edit and write cards Paladin's own agent uses,
// so its diff renders, with one card per new file and per changed hunk.
func TestCodexFileChangesBecomeWriteAndEditCards(t *testing.T) {
	rt, log, _ := codexRuntime(t, "patch")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "patch"); final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	type call struct{ name, args, result string }
	var final []call
	for _, e := range log.all() {
		if e.Tool != nil && e.Tool.Status == "ok" {
			final = append(final, call{e.Tool.Name, e.Tool.Arguments, e.Tool.Result})
		}
	}
	want := []call{
		{"write", `{"content":"hello\nworld\n","path":"/w/new.txt"}`, `{"bytes":12,"created":true,"lines":2}`},
		{"edit", `{"new_string":"a\nB\nc","old_string":"a\nb\nc","path":"/w/old.txt"}`, `{"replacements":1}`},
		{"edit", `{"new_string":"y\nz","old_string":"x\nz","path":"/w/old.txt"}`, `{"replacements":1}`},
	}
	if len(final) != len(want) {
		t.Fatalf("tool results %+v", final)
	}
	for i := range want {
		if final[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, final[i], want[i])
		}
	}
	ids := map[string]bool{}
	for _, e := range log.all() {
		if e.Tool != nil {
			ids[e.Tool.ID] = true
		}
	}
	if !ids["fc-1"] || !ids["fc-1#2"] || !ids["fc-1#3"] {
		t.Fatalf("card ids %v (the first takes over the running card)", ids)
	}
	hist, _ := rt.SessionHistory(id, cwd)
	var rows []string
	for _, row := range hist {
		rows = append(rows, row.Role+":"+derefString(row.Name))
	}
	if got := strings.Join(rows, " "); got != "user: assistant: tool:write assistant: tool:edit assistant: tool:edit" {
		t.Fatalf("history %s", got)
	}
}

func codexReply(t *testing.T, rt *AgentRuntime, id, cwd, message string) TurnInfo {
	t.Helper()
	final := <-submitCodex(t, rt, id, cwd, message)
	if final.Error != "" {
		t.Fatalf("%q: %+v", message, final)
	}
	return final
}

func TestCodexCompactRunsCodexsOwnCompaction(t *testing.T) {
	rt, log, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	final := codexReply(t, rt, id, cwd, "/compact")
	if final.Reply != "" {
		t.Fatalf("reply %q", final.Reply)
	}
	if !strings.Contains(fakeLog(t, fake), "thread/compact/start") {
		t.Fatalf("codex was not asked to compact:\n%s", fakeLog(t, fake))
	}
	var phases []string
	for _, e := range log.all() {
		if e.Compaction != nil {
			phases = append(phases, e.Compaction.Phase)
		}
	}
	if strings.Join(phases, ",") != "start,done" {
		t.Fatalf("compaction events %v", phases)
	}
	hist, _ := rt.SessionHistory(id, cwd)
	if len(hist) != 2 || hist[0].Content != "/compact" || derefString(hist[1].Kind) != "compaction" ||
		hist[1].Role != "system" || hist[1].Content != "" || hist[1].Compaction == nil || hist[1].Compaction.Reason != "manual" {
		t.Fatalf("history %+v", hist)
	}

	// Paladin's own compact request (the composer's /compact) does the same.
	provider := HarnessCodex
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Cwd: cwd, Provider: &provider, Compact: true}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if f := awaitTurn(t, done); f.Error != "" || f.Reply != "" {
		t.Fatalf("compact request: %+v", f)
	}
	if strings.Count(fakeLog(t, fake), "thread/compact/start") != 2 {
		t.Fatalf("log:\n%s", fakeLog(t, fake))
	}
}

func TestCodexGoalCommandsAndGoalTurns(t *testing.T) {
	rt, log, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)

	if r := codexReply(t, rt, id, cwd, "/goal").Reply; !strings.Contains(r, "No goal is set") {
		t.Fatalf("empty goal: %q", r)
	}
	if r := codexReply(t, rt, id, cwd, "/goal ship the thing").Reply; !strings.Contains(r, "Goal set") || !strings.Contains(r, "ship the thing") {
		t.Fatalf("set: %q", r)
	}
	// Codex then works on the goal by itself; Paladin shows that as a turn.
	evs := log.waitFor(t, "the goal's own turn", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "turn/done" && e.Turn.Reply == "Working on the goal." {
				return true
			}
		}
		return false
	})
	var adopted *TurnInfo
	for _, e := range evs {
		if e.Event == "turn/done" && e.Turn.Reply == "Working on the goal." {
			adopted = e.Turn
		}
	}
	if adopted == nil || !adopted.Resume || adopted.Error != "" || !adopted.Persisted {
		t.Fatalf("adopted turn %+v", adopted)
	}
	for _, tc := range []struct{ cmd, want string }{
		{"/goal", "(active): ship the thing"},
		{"/goal pause", "(paused)"},
		{"/goal resume", "(active)"},
		{"/goal clear", "Goal cleared."},
	} {
		if r := codexReply(t, rt, id, cwd, tc.cmd).Reply; !strings.Contains(r, tc.want) {
			t.Fatalf("%s: %q, want %q", tc.cmd, r, tc.want)
		}
	}
	if !strings.Contains(fakeLog(t, fake), "goal/set objective= status=paused") {
		t.Fatalf("log:\n%s", fakeLog(t, fake))
	}
	hist, _ := rt.SessionHistory(id, cwd)
	var roles []string
	for _, row := range hist[:5] {
		roles = append(roles, row.Role)
	}
	// user /goal, assistant, user /goal ship…, assistant, then the goal's own
	// turn with no user row before it.
	if got := strings.Join(roles, ","); got != "user,assistant,user,assistant,assistant" {
		t.Fatalf("roles %s", got)
	}
}

func TestCodexReviewTargets(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	for _, msg := range []string{"/review", "/review base main", "/review commit abc123", "/review focus on auth"} {
		if r := codexReply(t, rt, id, cwd, msg).Reply; r != "Looks fine." {
			t.Fatalf("%s: %q", msg, r)
		}
	}
	log := fakeLog(t, fake)
	for _, want := range []string{
		`review/start {"type":"uncommittedChanges"}`,
		`review/start {"branch":"main","type":"baseBranch"}`,
		`review/start {"sha":"abc123","type":"commit"}`,
		`review/start {"instructions":"focus on auth","type":"custom"}`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in\n%s", want, log)
		}
	}
}

func TestCodexFeedbackUploadsTheThread(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	r := codexReply(t, rt, id, cwd, "/feedback it stopped early").Reply
	if !strings.Contains(r, "fb-123") || !strings.Contains(fakeLog(t, fake), "feedback reason=it stopped early") {
		t.Fatalf("reply %q\n%s", r, fakeLog(t, fake))
	}
}

func TestOtherSlashLinesGoToCodexAsText(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	codexReply(t, rt, id, cwd, "/init please")
	if !strings.Contains(fakeLog(t, fake), "text=/init please") {
		t.Fatalf("log:\n%s", fakeLog(t, fake))
	}
}

func TestCodexPlanModeAndPlanUpdates(t *testing.T) {
	rt, log, fake := codexRuntime(t, "plan")
	id, cwd := openCodex(t, rt)
	provider, model := HarnessCodex, "fake-luna"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Message: "plan it", Cwd: cwd, Provider: &provider, Model: &model, Mode: "plan"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	final := awaitTurn(t, done)
	if final.Error != "" || final.Reply != "1. Read\n2. Write\n" {
		t.Fatalf("final %+v", final)
	}
	if !strings.Contains(fakeLog(t, fake), "collaboration mode=plan") {
		t.Fatalf("plan mode not sent:\n%s", fakeLog(t, fake))
	}
	todos, err := rt.SessionTodos(id, cwd)
	if err != nil || len(todos) != 3 || todos[2].Status != "in_progress" || todos[1].Status != "finished" || todos[0].Content != "Read the code" {
		t.Fatalf("todos %+v %v", todos, err)
	}
	var cards int
	for _, e := range log.all() {
		if e.Tool != nil && e.Tool.Name == "todo" {
			cards++
			if e.Tool.ID != "plan-tu-1" {
				t.Fatalf("every update shares one card id: %q", e.Tool.ID)
			}
		}
	}
	if cards != 2 {
		t.Fatalf("todo events = %d", cards)
	}
	hist, _ := rt.SessionHistory(id, cwd)
	rows := 0
	for _, row := range hist {
		if derefString(row.Name) == "todo" {
			rows++
			if !strings.Contains(row.Content, `"Run tests","status":"in_progress"`) {
				t.Fatalf("the recorded list should be the latest: %s", row.Content)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("todo rows = %d", rows)
	}

	// Leaving plan mode is told to Codex too.
	provider2 := HarnessCodex
	_, done, _ = rt.SubmitTurn(agent.Request{SessionID: id, Message: "go", Cwd: cwd, Provider: &provider2, Model: &model}, SubmitOpts{})
	awaitTurn(t, done)
	if !strings.Contains(fakeLog(t, fake), "collaboration mode=default") {
		t.Fatalf("default mode not sent:\n%s", fakeLog(t, fake))
	}
}

func TestCodexQuestionsGoThroughTheAskWizard(t *testing.T) {
	rt, log, fake := codexRuntime(t, "ask")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "build it")
	evs := log.waitFor(t, "the ask card", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Tool != nil && e.Tool.Name == "ask" && e.Tool.Status == "running" {
				return true
			}
		}
		return false
	})
	var card *TurnEvent
	for i := range evs {
		if evs[i].Tool != nil && evs[i].Tool.Name == "ask" {
			card = &evs[i]
		}
	}
	if card.Tool.ID != "q-1" || !strings.Contains(card.Tool.Arguments, `"options":["hello","hello_world"]`) || !strings.Contains(card.Tool.Arguments, "Name: What should it be called?") {
		t.Fatalf("ask card %+v", card.Tool)
	}
	if ok, err := rt.AnswerAsk(id, "q-1", []tools.AskAnswer{{Question: "a", Answer: "hello_world", Selected: nil}, {Question: "b", Answer: "main.py"}}); err != nil || !ok {
		t.Fatalf("answer %v %v", ok, err)
	}
	final := awaitTurn(t, done)
	if final.Error != "" || final.Reply != "thanks" {
		t.Fatalf("final %+v", final)
	}
	if want := `answers={"name":{"answers":["hello_world"]},"where":{"answers":["main.py"]}}`; !strings.Contains(fakeLog(t, fake), want) {
		t.Fatalf("want %s in\n%s", want, fakeLog(t, fake))
	}
	hist, _ := rt.SessionHistory(id, cwd)
	if derefString(hist[2].Name) != "ask" || hist[2].Role != "tool" {
		t.Fatalf("history %+v", hist)
	}
}

func TestImportedUnfinishedCodexCommandDoesNotClaimFailure(t *testing.T) {
	turn := codex.Decode([]byte(`{"items":[{"type":"commandExecution","id":"cmd-1","command":"sleep 30","status":"inProgress"}]}`))
	rows := codexTurnMessages(turn)
	for _, row := range rows {
		if row.Role == transcript.RoleTool {
			if row.IsError || !strings.Contains(row.Content, `"status":"unknown"`) {
				t.Fatalf("unfinished command was reported as a failure: %+v", row)
			}
			return
		}
	}
	t.Fatalf("missing command result: %+v", rows)
}

func TestCodexCompletedTurnDoesNotInventCommandFailure(t *testing.T) {
	tx := make(chan agent.ChatChunk, 4)
	run := &codexRun{tx: tx, status: "completed", tools: map[string]*codexTool{"cmd": {id: "cmd", name: "shell"}}}
	run.closeOpenTools()
	if len(run.records) != 2 || run.records[1].IsError || !strings.Contains(run.records[1].Content, `"status":"unknown"`) {
		t.Fatalf("unfinished command status: %+v", run.records)
	}
}

func TestDismissingCodexQuestionsStopsTheTurn(t *testing.T) {
	rt, log, _ := codexRuntime(t, "ask")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "build it")
	log.waitFor(t, "the ask card", func(evs []TurnEvent) bool { return countEvents(evs, "chat/tool") >= 1 })
	if !rt.CancelAsk(id, "q-1") {
		t.Fatal("nothing was waiting")
	}
	if final := awaitTurn(t, done); !final.Stopped {
		t.Fatalf("final %+v", final)
	}
}

// Codex says how full its context is and what window it really runs with
// (258,400 for its current models, not the 1M a table might claim), and it
// repeats a usage notice when nothing new was billed.
func TestCodexReportsItsOwnContextWindowAndFill(t *testing.T) {
	rt, _, _ := codexRuntime(t, "usage")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "go"); final.Error != "" {
		t.Fatalf("turn: %+v", final)
	}
	view, err := rt.SessionUsage(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	// 1000 + 1600 input and 20 + 40 output, with the repeated notice counted once.
	if view.PromptTokens != 2600 || view.CompletionTokens != 60 || view.Calls != 2 {
		t.Fatalf("billed %+v", view.Counters)
	}
	if view.ContextWindow != 258400 || !view.ContextKnown || view.ContextTokens != 1640 {
		t.Fatalf("context %d tokens of %d (known %v)", view.ContextTokens, view.ContextWindow, view.ContextKnown)
	}
	if view.FillPercent == nil || *view.FillPercent < 0.634 || *view.FillPercent > 0.635 {
		t.Fatalf("fill %v, want 1640 of 258400", view.FillPercent)
	}
}

func TestCodexRemembersAModelsWindowForNewSessions(t *testing.T) {
	rt, _, _ := codexRuntime(t, "usage")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "go")

	other, ocwd := openCodex(t, rt)
	view, err := rt.SessionUsage(other, ocwd)
	if err != nil {
		t.Fatal(err)
	}
	if view.ContextWindow != 258400 || !view.ContextKnown || view.FillPercent == nil || *view.FillPercent != 0 {
		t.Fatalf("a fresh session on a model Codex already reported should show its window: %+v", view)
	}
}

// Paladin's own table has windows for models it calls itself; it says nothing
// about what Codex runs them with, so a Codex session shows no percentage until
// Codex reports one.
func TestCodexSessionShowsNoPercentageBeforeAReport(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	opened, err := rt.OpenSessionWith("", t.TempDir(), OpenOpts{Harness: HarnessCodex, Model: "gpt-5.6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := rt.SessionUsage(opened.SessionID, opened.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if view.ContextKnown || view.FillPercent != nil {
		t.Fatalf("a table value must not stand in for Codex's window: %+v", view)
	}
}

// With no model chosen Codex runs its default; the ledger names it.
func TestCodexNamesTheDefaultModelItRan(t *testing.T) {
	rt, _, _ := codexRuntime(t, "usage")
	cwd := t.TempDir()
	opened, err := rt.OpenSessionWith("", cwd, OpenOpts{Harness: HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	provider := HarnessCodex
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: opened.SessionID, Message: "go", Cwd: cwd, Provider: &provider}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if final := awaitTurn(t, done); final.Error != "" {
		t.Fatalf("turn: %+v", final)
	}
	view, _ := rt.SessionUsage(opened.SessionID, cwd)
	if view.Model != "fake-default" || !view.ContextKnown || view.ContextWindow != 258400 {
		t.Fatalf("usage %+v", view)
	}
}

// firstUserIndex returns the history index of the nth (from 0) user message.
func userIndex(t *testing.T, rt *AgentRuntime, id, cwd string, nth int) int {
	t.Helper()
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := sess.History()
	seen := 0
	for i, m := range msgs {
		if m.Role == "user" {
			if seen == nth {
				return i
			}
			seen++
		}
	}
	t.Fatalf("no user message %d in %d rows", nth, len(msgs))
	return -1
}

// An undo is also Codex's undo: its thread forgets the turns the user took back,
// so the next message does not build on them.
func TestUndoRevertsTheCodexThreadToo(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first")
	<-submitCodex(t, rt, id, cwd, "second")
	<-submitCodex(t, rt, id, cwd, "third")

	if _, err := rt.RevertSession(id, cwd, userIndex(t, rt, id, cwd, 1)); err != nil {
		t.Fatal(err)
	}
	log := fakeLog(t, fake)
	if strings.Count(log, "thread/revert") != 1 || !strings.Contains(log, "thread/revert thread=th-1 before=tu-2") {
		t.Fatalf("Codex should drop everything from the second turn on:\n%s", log)
	}
	hist, _ := rt.SessionHistory(id, cwd)
	for _, row := range hist {
		if row.Role == "user" && row.Content != "first" {
			t.Fatalf("history still has %q", row.Content)
		}
	}
	// The conversation carries on in the same thread.
	<-submitCodex(t, rt, id, cwd, "again")
	log = fakeLog(t, fake)
	if strings.Count(log, "thread/start") != 1 || !strings.Contains(log, "turn/start thread=th-1 model=fake-luna effort= text=again") {
		t.Fatalf("a reverted session keeps its thread:\n%s", log)
	}
}

func TestUndoToTheStartRevertsEveryCodexTurn(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first")
	<-submitCodex(t, rt, id, cwd, "second")
	if _, err := rt.RevertSession(id, cwd, 0); err != nil {
		t.Fatal(err)
	}
	if log := fakeLog(t, fake); !strings.Contains(log, "thread/revert thread=th-1 before=tu-1") {
		t.Fatalf("log:\n%s", log)
	}
}

// If Codex cannot revert (a thread with legacy history), the undo still happens
// and the next message starts a new thread told what was said so far.
func TestUndoFallsBackToAFreshThreadWhenCodexCannotRevert(t *testing.T) {
	rt, _, fake := codexRuntime(t, "revert-fails")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first")
	<-submitCodex(t, rt, id, cwd, "second")
	if _, err := rt.RevertSession(id, cwd, userIndex(t, rt, id, cwd, 1)); err != nil {
		t.Fatalf("a Codex that cannot revert must not block the undo: %v", err)
	}
	sess, _ := rt.transcripts.Load(cwd, id)
	if sess.Meta.Harness == nil || sess.Meta.Harness.ThreadID != "" {
		t.Fatalf("the old thread should be dropped: %+v", sess.Meta.Harness)
	}
	<-submitCodex(t, rt, id, cwd, "again")
	log := fakeLog(t, fake)
	if strings.Count(log, "thread/start") != 2 || !strings.Contains(log, "The conversation so far") || !strings.Contains(log, "User: first") {
		t.Fatalf("the new thread should be seeded from the kept history:\n%s", log)
	}
	if strings.Contains(log, "User: second") {
		t.Fatalf("the undone message must not come back:\n%s", log)
	}
}

func TestCodexRewindTargetFindsTheFirstTurnToDrop(t *testing.T) {
	msgs := []transcript.Message{
		{Role: "user", HarnessTurn: "t1"}, {Role: "assistant"},
		{Role: "user", HarnessTurn: "t2"}, {Role: "assistant"},
		{Role: "assistant", HarnessTurn: "t3"}, // a turn Codex started itself
	}
	cases := []struct {
		index  int
		want   string
		tagged bool
	}{{0, "t1", true}, {2, "t2", true}, {3, "t3", true}, {5, "", true}}
	for _, c := range cases {
		got, tagged := codexRewindTarget(msgs, c.index)
		if got != c.want || tagged != c.tagged {
			t.Errorf("index %d: got %q tagged=%v, want %q tagged=%v", c.index, got, tagged, c.want, c.tagged)
		}
	}
	if _, tagged := codexRewindTarget([]transcript.Message{{Role: "user"}, {Role: "assistant"}}, 0); tagged {
		t.Error("a session from before turns were tagged has no ids to revert to")
	}
}

// Forking a Codex session forks its Codex thread at the same point (Codex keeps
// the turns' own history), instead of starting a thread and retelling the
// conversation as text.
func TestForkedCodexSessionForksTheCodexThread(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first")
	<-submitCodex(t, rt, id, cwd, "second")

	info, err := rt.ForkSession(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	// The fork's thread is made when it is first used, not when it is created.
	if strings.Contains(fakeLog(t, fake), "thread/fork") {
		t.Fatal("the fork should be lazy")
	}
	<-submitCodex(t, rt, info.SessionID, cwd, "and then?")
	log := fakeLog(t, fake)
	if !strings.Contains(log, "thread/fork thread=th-1 last=tu-2") {
		t.Fatalf("Codex should fork the source thread through its last turn:\n%s", log)
	}
	if strings.Count(log, "thread/start") != 1 || !strings.Contains(log, "turn/start thread=th-2 model=fake-luna effort= text=and then?") {
		t.Fatalf("the fork continues on the forked thread, with its own text untouched:\n%s", log)
	}
	// The source keeps its own thread.
	<-submitCodex(t, rt, id, cwd, "source again")
	if log := fakeLog(t, fake); !strings.Contains(log, "turn/start thread=th-1 model=fake-luna effort= text=source again") {
		t.Fatalf("the source thread is untouched:\n%s", log)
	}
}

func TestForkFallsBackToRetellingWhenCodexCannotFork(t *testing.T) {
	rt, _, fake := codexRuntime(t, "fork-fails")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first")
	info, err := rt.ForkSession(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	<-submitCodex(t, rt, info.SessionID, cwd, "next")
	log := fakeLog(t, fake)
	if strings.Count(log, "thread/start") != 2 || !strings.Contains(log, "The conversation so far") || !strings.Contains(log, "User: first") {
		t.Fatalf("a fork that Codex cannot make starts a thread told the conversation:\n%s", log)
	}
}

func TestCodexThreadsListsWhatCodexHasAndFlagsImportedOnes(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)

	page, err := rt.CodexThreads(CodexThreadQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 2 || page.Threads[0].ID != "th-old" || page.Threads[0].Preview != "Fix the bug" || page.Threads[0].Cwd != cwd {
		t.Fatalf("threads %+v", page.Threads)
	}
	if page.Threads[1].Title != "Named thread" {
		t.Fatalf("a named thread shows its name: %+v", page.Threads[1])
	}
	if found, _ := rt.CodexThreads(CodexThreadQuery{Search: "bug"}); len(found.Threads) != 1 {
		t.Fatalf("search: %+v", found.Threads)
	}
	if here, _ := rt.CodexThreads(CodexThreadQuery{Cwd: cwd}); len(here.Threads) != 1 || here.Threads[0].ID != "th-old" {
		t.Fatalf("folder filter: %+v", here.Threads)
	}
	opened, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = rt.CodexThreads(CodexThreadQuery{})
	if page.Threads[0].SessionID != opened.SessionID || page.Threads[1].SessionID != "" {
		t.Fatalf("an imported thread says which session holds it: %+v", page.Threads)
	}
}

func TestImportingACodexThreadKeepsItsHistoryAndThread(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)

	opened, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Harness != HarnessCodex || opened.Provider != HarnessCodex || opened.Model != "fake-luna" || opened.Cwd != cwd {
		t.Fatalf("opened %+v", opened)
	}
	hist, err := rt.SessionHistory(opened.SessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	var shape []string
	for _, row := range hist {
		shape = append(shape, row.Role)
	}
	// user, the thinking, the command (call and result), the new file (call and result), the answer, then turn two.
	if got := strings.Join(shape, ","); got != "user,assistant,assistant,tool,assistant,tool,assistant,user,assistant" {
		t.Fatalf("history roles = %s\n%+v", got, hist)
	}
	if hist[0].Content != "Fix the bug" || hist[len(hist)-1].Content != "You are welcome." {
		t.Fatalf("messages not carried over: %+v", hist)
	}
	if derefString(hist[3].Name) != "shell" || !strings.Contains(derefString(hist[3].Arguments), `"command":"ls"`) {
		t.Fatalf("the command should show as a shell card: %+v", hist[3])
	}
	if derefString(hist[5].Name) != "write" {
		t.Fatalf("the new file should show as a write card: %+v", hist[5])
	}
	if hist[0].CreatedAt != 1794590100 {
		t.Fatalf("a message keeps the time its turn started, not the time of the import: %d", hist[0].CreatedAt)
	}
	sess, _ := rt.transcripts.Load(cwd, opened.SessionID)
	if sess.Meta.Harness == nil || sess.Meta.Harness.ThreadID != "th-old" || sess.Meta.Title != "Fix the bug" {
		t.Fatalf("meta %+v title %q", sess.Meta.Harness, sess.Meta.Title)
	}
	msgs, _ := sess.History()
	tags := map[string]string{}
	for _, m := range msgs {
		if m.HarnessTurn != "" {
			tags[m.HarnessTurn] = m.Content
		}
	}
	if tags["tu-a1"] != "Fix the bug" || tags["tu-a2"] != "Thanks" || len(tags) != 2 {
		t.Fatalf("each turn's first row carries its id, which undo and fork use: %v", tags)
	}

	// Importing it again opens the same session.
	again, err := rt.ImportCodexThread("th-old", "")
	if err != nil || again.SessionID != opened.SessionID {
		t.Fatalf("a thread is imported once: %+v %v", again, err)
	}
	// The next message continues the very thread.
	<-submitCodex(t, rt, opened.SessionID, cwd, "one more thing")
	log := fakeLog(t, fake)
	if strings.Count(log, "thread/start") != 0 || !strings.Contains(log, "thread/resume th-old") || !strings.Contains(log, "turn/start thread=th-old model=fake-luna effort= text=one more thing") {
		t.Fatalf("the session should resume the Codex thread:\n%s", log)
	}
}

func TestCodexSkillsListsTheEnabledOnes(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	skills, err := rt.CodexSkills(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "guava" || skills[0].Title != "Guava" || skills[0].Description != "Answers with a fixed word." {
		t.Fatalf("skills %+v (a disabled skill must not be offered)", skills)
	}
}

// A $skill in the message is also sent as the structured skill item Codex
// understands; a message with no $ never asks Codex for its skills.
func TestCodexMessageNamingASkillCarriesTheSkillItem(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "plain message with a price of 5 dollars")
	if log := fakeLog(t, fake); strings.Contains(log, "skills/list") {
		t.Fatalf("no $name, so no skill lookup:\n%s", log)
	}
	<-submitCodex(t, rt, id, cwd, "please $guava now, and $unknown too")
	log := fakeLog(t, fake)
	if !strings.Contains(log, "text=please $guava now, and $unknown too items=text,skill:guava@/w/.agents/skills/guava/SKILL.md summary=") {
		t.Fatalf("the text stays as typed and the known skill rides along:\n%s", log)
	}
}

func TestSkillMentionsFindsDollarNames(t *testing.T) {
	got := skillMentions("use $guava, then ($Plan:x) and $guava again; not a$b or 5$ or $")
	want := []string{"guava", "Plan:x"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func waitForAsk(t *testing.T, log *eventLog) TurnEvent {
	t.Helper()
	evs := log.waitFor(t, "the ask card", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Tool != nil && e.Tool.Name == "ask" && e.Tool.Status == "running" {
				return true
			}
		}
		return false
	})
	for _, e := range evs {
		if e.Tool != nil && e.Tool.Name == "ask" && e.Tool.Status == "running" {
			return e
		}
	}
	t.Fatal("no ask card")
	return TurnEvent{}
}

// An MCP server's prompt (Codex's app approvals arrive this way) is asked in
// Paladin's question wizard: each form field is a question, and the answers
// go back as the form's values.
func TestCodexMCPPromptGoesThroughTheAskWizard(t *testing.T) {
	rt, log, fake := codexRuntime(t, "elicit")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "use safari")
	card := waitForAsk(t, log)
	for _, want := range []string{"Allow ChatGPT to use Safari?", `"options":["Allow","Do not allow"]`, "How many", "Note", `"multiple":true`, "Remember this choice?", "For this session"} {
		if !strings.Contains(card.Tool.Arguments, want) {
			t.Fatalf("the card should carry %q:\n%s", want, card.Tool.Arguments)
		}
	}
	answers := []tools.AskAnswer{
		{Question: "q1", Answer: "Allow", Selected: []string{"Allow"}},
		{Question: "q2", Answer: "3"},
		{Question: "q3", Answer: "hi"},
		{Question: "q4", Answer: "a, b", Selected: []string{"a", "b"}},
		{Question: "q5", Answer: "For this session", Selected: []string{"For this session"}},
	}
	if ok, err := rt.AnswerAsk(id, card.Tool.ID, answers); err != nil || !ok {
		t.Fatalf("answer %v %v", ok, err)
	}
	if final := awaitTurn(t, done); final.Error != "" || final.Reply != "done" {
		t.Fatalf("final %+v", final)
	}
	want := `elicitation={"_meta":{"persist":"session"},"action":"accept","content":{"approval":"accept","count":3,"note":"hi","tags":["a","b"]}}`
	if !strings.Contains(fakeLog(t, fake), want) {
		t.Fatalf("want %s in\n%s", want, fakeLog(t, fake))
	}
}

func TestDismissingAnMCPPromptCancelsIt(t *testing.T) {
	rt, log, fake := codexRuntime(t, "elicit")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "use safari")
	card := waitForAsk(t, log)
	if !rt.CancelAsk(id, card.Tool.ID) {
		t.Fatal("nothing was waiting")
	}
	awaitTurn(t, done)
	if got := fakeLog(t, fake); !strings.Contains(got, `elicitation={"action":"cancel"}`) {
		t.Fatalf("a dismissed prompt is a cancel:\n%s", got)
	}
}

// A prompt that needs a web page is declined: Paladin does not open links an
// MCP server hands it.
func TestCodexURLPromptsAreDeclined(t *testing.T) {
	rt, _, fake := codexRuntime(t, "elicit-url")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "sign in"); final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	if got := fakeLog(t, fake); !strings.Contains(got, `elicitation={"action":"decline"}`) {
		t.Fatalf("log:\n%s", got)
	}
}

// Codex's own "for the session" covers only the exact command, so Paladin
// remembers the choice per kind of request and answers the next ones itself.
func TestCodexSessionApprovalCoversLaterRequestsOfThatKind(t *testing.T) {
	rt, log, fake := codexRuntime(t, "approval")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "touch x")
	log.waitFor(t, "approval card", func(evs []TurnEvent) bool { return countEvents(evs, "chat/approval") >= 1 })
	if ok, err := rt.AnswerApproval(id, "cmd-1", "session"); err != nil || !ok {
		t.Fatalf("answer: %v %v", ok, err)
	}
	if final := awaitTurn(t, done); final.Error != "" {
		t.Fatalf("first turn: %+v", final)
	}
	before := countEvents(log.all(), "chat/approval")

	// The next command of the same kind is accepted for the session without asking.
	if final := <-submitCodex(t, rt, id, cwd, "touch y"); final.Error != "" || final.Reply != "after acceptForSession" {
		t.Fatalf("second turn: %+v", final)
	}
	log2 := fakeLog(t, fake)
	if strings.Count(log2, "approval decision=acceptForSession") != 2 {
		t.Fatalf("both requests should be accepted for the session:\n%s", log2)
	}
	// It still shows as an approved card, so the turn reads the same.
	var approved bool
	for _, e := range log.all()[before:] {
		if e.Approval != nil {
			if e.Approval.State == "pending" {
				t.Fatalf("a remembered choice must not ask again: %+v", e.Approval)
			}
			approved = approved || e.Approval.State == "approved"
		}
	}
	if !approved {
		t.Fatal("the auto-accepted command should still show as approved")
	}
}

func TestCodexSessionApprovalDoesNotSurviveAChangeOfPermissions(t *testing.T) {
	rt, log, _ := codexRuntime(t, "approval")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "touch x")
	log.waitFor(t, "approval card", func(evs []TurnEvent) bool { return countEvents(evs, "chat/approval") >= 1 })
	if _, err := rt.AnswerApproval(id, "cmd-1", "session"); err != nil {
		t.Fatal(err)
	}
	awaitTurn(t, done)
	before := countEvents(log.all(), "chat/approval")

	provider, model := HarnessCodex, "fake-luna"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Message: "touch z", Cwd: cwd, Provider: &provider, Model: &model, Permissions: "read-only"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	log.waitFor(t, "a fresh approval card", func(evs []TurnEvent) bool { return countEvents(evs, "chat/approval") > before })
	if _, err := rt.AnswerApproval(id, "cmd-1", "deny"); err != nil {
		t.Fatal(err)
	}
	awaitTurn(t, done)
}

func TestMcpCommandListsCodexsServers(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	final := <-submitCodex(t, rt, id, cwd, "/mcp")
	if final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	for _, want := range []string{"`codex_apps`", "connected", "2 tools", "`docs`", "needs sign-in", "`bad`", "failed", "spawn failed", "`idle`", "1 tool"} {
		if !strings.Contains(final.Reply, want) {
			t.Fatalf("the list should say %q:\n%s", want, final.Reply)
		}
	}
	if log := fakeLog(t, fake); strings.Contains(log, "turn/start") || !strings.Contains(log, "mcpServerStatus/list thread=th-1 detail=toolsAndAuthOnly") {
		t.Fatalf("/mcp asks Codex for its status and starts no turn:\n%s", log)
	}
}

// Codex only streams its reasoning summaries when the turn asks for them.
func TestCodexTurnsAskForReasoningSummaries(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "think")
	if log := fakeLog(t, fake); !strings.Contains(log, "summary=auto") {
		t.Fatalf("turns ask for summaries by default:\n%s", log)
	}
}

// A summary style the user configured in Codex is kept: a provider can refuse
// the others.
func TestCodexTurnsKeepTheConfiguredSummaryStyle(t *testing.T) {
	t.Setenv("PALADIN_FAKE_CODEX_SUMMARY", "concise")
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "think")
	if log := fakeLog(t, fake); !strings.Contains(log, "summary=concise") {
		t.Fatalf("log:\n%s", log)
	}
}

func lineWith(t *testing.T, log, needle string) string {
	t.Helper()
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	t.Fatalf("no line with %q in:\n%s", needle, log)
	return ""
}

func TestFastCommandTurnsTheFastTierOnAndOff(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	if r := <-submitCodex(t, rt, id, cwd, "/fast"); !strings.Contains(r.Reply, "Fast mode is off") {
		t.Fatalf("status: %q", r.Reply)
	}
	if r := <-submitCodex(t, rt, id, cwd, "/fast on"); !strings.Contains(r.Reply, "Fast mode is on") || !strings.Contains(r.Reply, "1.5x speed") {
		t.Fatalf("on: %q", r.Reply)
	}
	<-submitCodex(t, rt, id, cwd, "hello")
	if line := lineWith(t, fakeLog(t, fake), "text=hello "); !strings.HasSuffix(line, "tier=priority") {
		t.Fatalf("a turn in fast mode asks for the fast tier: %s", line)
	}
	if r := <-submitCodex(t, rt, id, cwd, "/fast off"); !strings.Contains(r.Reply, "Fast mode is off") {
		t.Fatalf("off: %q", r.Reply)
	}
	<-submitCodex(t, rt, id, cwd, "again")
	if line := lineWith(t, fakeLog(t, fake), "text=again "); !strings.HasSuffix(line, "tier=") {
		t.Fatalf("after /fast off no tier is requested: %s", line)
	}
	if strings.Contains(fakeLog(t, fake), "text=/fast") {
		t.Fatal("/fast is the core's own command; it never reaches Codex as a turn")
	}
}

func TestFastCommandSaysWhenAModelHasNoFastTier(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	opened, err := rt.OpenSessionWith("", t.TempDir(), OpenOpts{Harness: HarnessCodex, Model: "no-such-model"})
	if err != nil {
		t.Fatal(err)
	}
	provider, model := HarnessCodex, "no-such-model"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: opened.SessionID, Message: "/fast on", Cwd: opened.Cwd, Provider: &provider, Model: &model}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if r := <-done; !strings.Contains(r.Reply, "no fast mode") {
		t.Fatalf("reply %q", r.Reply)
	}
}

func TestCodexRateLimitWindowsAreNamedByTheirLength(t *testing.T) {
	soon := time.Now().Add(time.Hour).Unix()
	limits := codex.RateLimits{Plan: "plus", Primary: &codex.RateWindow{UsedPercent: 42, DurationMins: 300, ResetsAt: soon},
		Secondary: &codex.RateWindow{UsedPercent: 130, DurationMins: 10080, ResetsAt: 0}}
	got := limitWindows(limits)
	if len(got) != 2 || got[0].Label != "Session" || got[0].UsedPercent != 42 || got[0].ResetsAt != soon {
		t.Fatalf("session window: %+v", got)
	}
	if got[1].Label != "Weekly" || got[1].UsedPercent != 100 || got[1].ResetsAt != 0 {
		t.Fatalf("a window over 100%% is clamped and one with no reset time has none: %+v", got[1])
	}
	// Without a length, a paid plan's windows are 5 hours and a week, a free plan's a month.
	paid := limitWindows(codex.RateLimits{Plan: "pro", Primary: &codex.RateWindow{UsedPercent: 1}, Secondary: &codex.RateWindow{UsedPercent: 2}})
	if paid[0].Label != "Session" || paid[1].Label != "Weekly" {
		t.Fatalf("paid defaults: %+v", paid)
	}
	free := limitWindows(codex.RateLimits{Plan: "free", Primary: &codex.RateWindow{UsedPercent: 1}})
	if free[0].Label != "Monthly" {
		t.Fatalf("free default: %+v", free)
	}
	if got := limitWindows(codex.RateLimits{}); len(got) != 0 {
		t.Fatalf("no windows reported: %+v", got)
	}
}

// A Codex session's usage shows the plan's limits next to the context fill.
func TestCodexSessionUsageCarriesThePlanLimits(t *testing.T) {
	rt, _, _ := codexRuntime(t, "usage")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "go"); final.Error != "" {
		t.Fatalf("turn: %+v", final)
	}
	view, err := rt.SessionUsage(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Limits) != 2 || view.Limits[0].Label != "Session" || view.Limits[0].UsedPercent != 42 || view.Limits[1].Label != "Weekly" || view.Limits[1].UsedPercent != 100 {
		t.Fatalf("limits %+v", view.Limits)
	}
}

func notices(evs []TurnEvent) []string {
	var out []string
	for _, e := range evs {
		if e.Notice != nil {
			out = append(out, e.Notice.Kind+": "+e.Notice.Text)
		}
	}
	return out
}

// What Codex warns about during a turn shows in the turn, in words.
func TestCodexWarningsAndReroutesShowAsNotices(t *testing.T) {
	rt, log, _ := codexRuntime(t, "notices")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "go"); final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	got := strings.Join(notices(log.all()), "\n")
	for _, want := range []string{
		"warning: Codex is running low on context",
		"info: Codex switched from gpt-a to gpt-b (high-risk cyber activity)",
		"warning: MCP server docs failed to start: spawn failed",
		"warning: A stop hook stopped the turn: tests are failing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "fine") || strings.Contains(got, "ignored") {
		t.Fatalf("only what needs attention is shown:\n%s", got)
	}
}

// Codex's account-level warnings (a broken config) reach no thread, so the
// first turn after they arrive shows them, once.
func TestCodexConfigWarningsShowOnTheFirstTurnOnly(t *testing.T) {
	t.Setenv("PALADIN_FAKE_CODEX_CONFIGWARN", "1")
	rt, log, _ := codexRuntime(t, "chat")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "first")
	got := notices(log.all())
	if len(got) != 2 || got[0] != "warning: Invalid config.toml: unknown key tools.bad (/h/.codex/config.toml)" || got[1] != "warning: model_reasoning is deprecated" {
		t.Fatalf("notices %v", got)
	}
	<-submitCodex(t, rt, id, cwd, "second")
	if again := notices(log.all()); len(again) != 2 {
		t.Fatalf("a warning is shown once: %v", again)
	}
}

// A subagent Codex spawns shows what it was asked and how it ended, not an
// empty card.
func TestCodexSubagentCallsShowThePromptAndTheOutcome(t *testing.T) {
	rt, log, _ := codexRuntime(t, "collab")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "split the work"); final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	var card *agent.ToolActivity
	for _, e := range log.all() {
		if e.Tool != nil && e.Tool.ID == "co-1" && e.Tool.Status == "ok" {
			card = e.Tool
		}
	}
	if card == nil {
		t.Fatalf("no finished subagent card: %s", describe(log.all()))
	}
	if card.Name != "codex_spawnAgent" || !strings.Contains(card.Arguments, `"prompt":"Look at the parser"`) || !strings.Contains(card.Arguments, `"model":"fake-luna"`) {
		t.Fatalf("card %+v", card)
	}
	if !strings.Contains(card.Result, "found 2 bugs") || !strings.Contains(card.Result, `"completed"`) {
		t.Fatalf("the result should carry what the agent reported: %s", card.Result)
	}
}
