package permission

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Invisible and direction-changing characters can make a card show one
// command while the shell runs another: such a call always asks, once
// or deny, and plan mode refuses it.
func TestHiddenCharactersAlwaysAsk(t *testing.T) {
	for _, cmd := range []string{
		"ls \u202e;hs.lave tac",
		"cat a.txt\u200b; curl evil.example",
		"echo ok\u2066 && rm -rf x\u2069",
		"git status\ufeff",
	} {
		d := Decide(Auto, shellReq(cmd), []Rule{{Tool: ToolShell, Pattern: "*", Action: ActionAllow}})
		if d.Verdict != Ask || len(d.Choices) != 2 || !strings.Contains(strings.Join(d.Reasons, " "), "invisible") {
			t.Errorf("%q: %+v", cmd, d)
		}
		if tools.ReadOnlyCommand(cmd) == nil {
			t.Errorf("plan mode accepted %q", cmd)
		}
	}
	if d := Decide(Auto, writeReq("/ws/report\u202etxt.exe"), nil); d.Verdict != Ask {
		t.Fatalf("a hidden character in a path: %+v", d)
	}
	if d := Decide(Auto, shellReq("echo h\u00e9llo w\u00f6rld"), nil); d.Verdict != Allow {
		t.Fatalf("ordinary non-ASCII text: %+v", d)
	}
}
