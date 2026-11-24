package runtime

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestClaudeStopAndSteer(t *testing.T) {
	for _, stop := range []bool{true, false} {
		t.Run(map[bool]string{true: "stop", false: "steer"}[stop], func(t *testing.T) {
			rt, log := claudeRuntime(t)
			id, cwd := openClaude(t, rt)
			done := submitClaude(t, rt, id, cwd, "long-test", "full-access")
			log.waitFor(t, "Claude waiting", func(e []TurnEvent) bool { return countEvents(e, "chat/delta") > 0 })
			if stop {
				if !rt.StopIn(id, cwd) {
					t.Fatal("Claude stop found no turn")
				}
			} else {
				if _, err := rt.SteerTurn(id, "continue", "continue"); err != nil {
					t.Fatal(err)
				}
			}
			final := awaitTurn(t, done)
			if final.Error != "" || final.Stopped != stop || !final.Persisted {
				t.Fatalf("final=%+v", final)
			}
		})
	}
}
func TestClaudeQuestionsUseTheSharedWizard(t *testing.T) {
	rt, log := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	done := submitClaude(t, rt, id, cwd, "ask-test", "read-only")
	log.waitFor(t, "Claude question", func(events []TurnEvent) bool {
		for _, event := range events {
			if event.Tool != nil && event.Tool.Name == "ask" && event.Tool.Status == "running" {
				return true
			}
		}
		return false
	})
	if ok, err := rt.AnswerAsk(id, "ask-1", []tools.AskAnswer{{Question: "Choose a format", Answer: "First"}}); err != nil || !ok {
		t.Fatalf("answer=%v err=%v", ok, err)
	}
	final := awaitTurn(t, done)
	if final.Error != "" || final.Reply != "First" {
		t.Fatalf("question reply=%+v", final)
	}
}
