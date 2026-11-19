package permission

import "testing"

// "Remember" must not save a rule broader than what the user saw: a shell,
// interpreter or fetch tool approved once for one script must not later
// run anything.
func TestPrefixOfNeverSavesABlanketRuleForCodeRunners(t *testing.T) {
	cases := map[string]string{
		"bash x.sh":                  "bash x.sh",
		"sh -c ls":                   "sh -c ls",
		"python3 -m pytest":          "python3 -m pytest",
		"python3 tool.py":            "python3 tool.py",
		"node app.js":                "node app.js",
		"curl -s https://e.example/": "curl -s https://e.example/",
		"env FOO=1 make all":         "make all", // env is looked through
		"xargs rm":                   "rm",       // xargs is looked through
		"git -C dir status":          "git -C dir status",
		"git -c a=b log":             "git -c a=b log",
		"docker --context x ps":      "docker --context x ps",
	}
	for cmd, want := range cases {
		if got := prefixOf(splitWords(cmd)); got != want {
			t.Errorf("prefixOf(%q) = %q, want %q", cmd, got, want)
		}
	}
	// Ordinary tools keep their short, useful rule.
	for cmd, want := range map[string]string{
		"npm test":       "npm test",
		"go build ./...": "go build",
		"make all":       "make all",
		"git status":     "git status",
		"ls -la":         "ls",
	} {
		if got := prefixOf(splitWords(cmd)); got != want {
			t.Errorf("prefixOf(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestRiskyEnvironmentSeesAssignmentsAfterAWrapper(t *testing.T) {
	for _, cmd := range []string{
		"time GIT_CONFIG_COUNT=1 git status",
		"nohup LD_PRELOAD=/tmp/x.so ls",
		"command PATH=/tmp/evil git status",
		"env time NODE_OPTIONS=--require=x node a.js",
		"exec BASH_ENV=/tmp/x bash -c true",
	} {
		if !riskyEnvironment(splitWords(cmd)) {
			t.Errorf("%q was not flagged", cmd)
		}
	}
	for _, cmd := range []string{"time make all", "env FOO=1 make", "nohup ls"} {
		if riskyEnvironment(splitWords(cmd)) {
			t.Errorf("%q was flagged", cmd)
		}
	}
}

func splitWords(cmd string) []string {
	var words []string
	cur := ""
	for _, r := range cmd {
		if r == ' ' {
			if cur != "" {
				words = append(words, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		words = append(words, cur)
	}
	return words
}
