package tools

import "testing"

// Each of these ran a program or read a protected file under zsh while the
// read-only parser called it safe. They must all be refused.
func TestReadOnlyCommandRefusesParserDifferentials(t *testing.T) {
	refused := map[string]string{
		// The segment splitter and the word splitter disagreed on backslashes.
		`find . -\exec sh -c id \;`:                          "backslash inside a flag",
		`find . -name x\' -o -exec sh -c id \; -o -name \'y`: "escaped quote read as a quote opener",
		`git -\c core.pager=sh log`:                          "backslash inside git -c",
		// ANSI-C and locale quoting.
		`awk $'BEGIN{sys\x74em("id")}'`: "ANSI-C quoted awk program",
		`find . $'-exec' id $';'`:       "ANSI-C quoted flag",
		`cat $"x"`:                      "locale quoting",
		// zsh-only code execution.
		`cat =(echo hi)`: "zsh =(...) substitution",
		`ls *(e:'id':)`:  "zsh glob qualifier",
		`echo "${(e)x}"`: "zsh parameter flag e",
		`ls ~[x]`:        "zsh named directory",
		`cat <(echo hi)`: "process substitution",
		`ls *(+id)`:      "glob qualifier with a function",
		// Brace expansion hides the real arguments from the path checks.
		`cat {/etc/passwd,}`:   "brace expansion of a path",
		`cat {~/.ssh/id_rsa,}`: "brace expansion of a home path",
		`cat {a..c}`:           "sequence expansion",
		// Option abbreviations and attached values.
		`sort -S1 --comp=sh file`: "abbreviated --compress-program",
		`sort --out=x file`:       "abbreviated --output",
		`sort --o=x file`:         "abbreviated --output",
		`awk -fx.awk file`:        "awk program file, attached",
		`awk -iinplace file`:      "awk include, attached",
		// A program picked by path is whatever sits at that path.
		`./ls`:           "workspace executable named like ls",
		`bin/git status`: "relative git",
		`/tmp/x/cat f`:   "cat from a temp folder",
	}
	for cmd, why := range refused {
		if err := ReadOnlyCommand(cmd); err == nil {
			t.Errorf("%s was accepted as read-only (%s)", cmd, why)
		}
	}
}

// Everyday read-only commands keep working: the hardening must not turn
// every inspection into a prompt.
func TestReadOnlyCommandStillAcceptsEverydayReads(t *testing.T) {
	for _, cmd := range []string{
		`ls -la`,
		`/bin/ls -la /tmp`,
		`/usr/bin/git status`,
		`git status --short`,
		`git log --oneline -5`,
		`git diff HEAD~1`,
		`git log stash@{0}`,
		`git show HEAD@{1}`,
		`grep -rn "a\|b" .`,
		`grep -rn 'foo(bar)' src`,
		`rg "func (\w+)" internal`,
		`find . -name "*.go" -type f`,
		`find . \( -name a -o -name b \) -type f`,
		`cat file.txt | head -5`,
		`sort -u file.txt`,
		`sort -rn file.txt | uniq -c`,
		`awk -F: '{print $1}' /etc/passwd`,
		`awk '{print $2}' file.txt`,
		`echo 'it'"'"'s fine'`,
		`wc -l *.go`,
		`ls src/{a}`,
		`head -n 20 README.md`,
	} {
		if err := ReadOnlyCommand(cmd); err != nil {
			t.Errorf("%s was refused: %v", cmd, err)
		}
	}
}
