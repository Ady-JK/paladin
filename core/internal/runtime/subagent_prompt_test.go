// subagent_prompt_test.go keeps the subagent shell guidance honest: every
// program it offers must really pass the plan-mode guard, every shape it
// calls refused must really be refused, and kinds without the shell tool
// must not see the section at all. Otherwise the prompt teaches a child
// calls the harness rejects.

package runtime

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestSubagentShellGuideNamesEveryAllowedProgram(t *testing.T) {
	for _, prog := range tools.ReadOnlyProgramList() {
		if prog != "git" && prog != "go" {
			// git and go need a read-only subcommand; the next test
			// covers those.
			if tools.ReadOnlyCommand(prog) != nil {
				t.Fatalf("plan mode refuses %q but it is on the allowlist", prog)
			}
		}
		// \b does not work for "[", so require a non-word character on
		// each side instead.
		re := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(prog) + `([^A-Za-z0-9_-]|$)`)
		if !re.MatchString(subagentShellGuide) {
			t.Fatalf("plan mode allows %q but the subagent shell guide never names it", prog)
		}
	}
}

func TestSubagentShellGuideAllowsWhatItClaims(t *testing.T) {
	for _, cmd := range []string{
		"git status", "git diff HEAD", "git log --oneline -20", "git show HEAD",
		"git blame a.go", "git ls-files", "git ls-tree HEAD", "git cat-file -p HEAD",
		"go list ./...", "go version", "go doc fmt", "go vet ./...",
		"sed -n 1,40p a.go", "head -50 a.go", "wc -l a.go", "rg -n foo a.go",
	} {
		if err := tools.ReadOnlyCommand(cmd); err != nil {
			t.Fatalf("the guide offers %q but plan mode refuses it: %v", cmd, err)
		}
	}
}

func TestSubagentShellGuideRefusalsMatchTheGuard(t *testing.T) {
	for _, cmd := range []string{
		"for d in *; do echo $d; done",
		"while true; do ls; done",
		"if [ -f x ]; then ls; fi",
		"echo $(date)",
		"echo `date`",
		"FOO=bar ls",
		"echo hi > out.txt",
		"sleep 1 &",
		"echo hi # note",
		"python3 -c 'print(1)'",
		"node -e 'console.log(1)'",
		"make build",
		"go build ./...",
		"go test ./...",
		"npm run dev",
		"pnpm install",
		"git add -A",
		"git commit -m x",
		"git checkout main",
		"git switch main",
		"git reset --hard",
		"git stash",
		"git clean -fd",
		"git branch new-branch",
		"git tag v1",
		"git remote add origin url",
	} {
		if err := tools.ReadOnlyCommand(cmd); err == nil {
			t.Fatalf("the guide calls %q refused, but plan mode allows it", cmd)
		}
	}
	if !strings.Contains(subagentShellGuide, "ONE simple command") {
		t.Fatal("the guide must say one simple command per call")
	}
}

func TestShellGuideOnlyForKindsWithShell(t *testing.T) {
	testRuntime(t) // registers the customizable items
	for _, k := range subagentKinds {
		got := k.prompt()
		hasGuide := strings.Contains(got, "ONE simple command")
		if hasGuide != k.allows("shell") {
			t.Fatalf("%s: shell guide present=%v, allowlist=%v", k.Name, hasGuide, k.allows("shell"))
		}
		if !strings.HasPrefix(got, "# SUBAGENT") || !strings.Contains(got, "## Role: "+strings.ReplaceAll(k.Name, "-", " ")) {
			t.Fatalf("%s: prompt missing its sections:\n%s", k.Name, got)
		}
	}
}
