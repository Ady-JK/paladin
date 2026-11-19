package permission

import (
	"testing"
)

// "Always allow" for one write must not become a grant over the home folder
// or the folders where credentials and login items live.
func TestNarrowFolderKeepsWriteGrantsSmall(t *testing.T) {
	homeDir = func() (string, error) { return "/Users/me", nil }
	t.Cleanup(func() { homeDir = defaultHomeDir })
	cases := map[string]string{
		"/Users/me/.zshrc":                          "/Users/me/.zshrc",
		"/Users/me/.ssh/known_hosts":                "/Users/me/.ssh/known_hosts",
		"/Users/me/.aws/credentials":                "/Users/me/.aws/credentials",
		"/Users/me/.config/git/config":              "/Users/me/.config/git/config",
		"/Users/me/Library/LaunchAgents/x.plist":    "/Users/me/Library/LaunchAgents/x.plist",
		"/Users/me/Library/Application Support/a/b": "/Users/me/Library/Application Support/a",
		"/Users/me/.nvm/versions/x/y/lib/z":         "/Users/me/.nvm/versions/x/y/lib",
		"/etc/hosts":                                "/etc/hosts",
		"/Users/me/code/project/notes.txt":          "/Users/me/code/project",
		"/Users/me/Documents/report.md":             "/Users/me/Documents",
	}
	for path, want := range cases {
		if got := narrowFolder(path); got != want {
			t.Errorf("narrowFolder(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestWriteOutsideTheWorkspaceOffersANarrowRule(t *testing.T) {
	homeDir = func() (string, error) { return "/Users/me", nil }
	t.Cleanup(func() { homeDir = defaultHomeDir })
	d := Decide(Review, Request{Kind: KindWrite, Path: "/Users/me/.zshrc", Outside: true}, nil)
	if d.Verdict != Ask || len(d.Remember) != 1 || d.Remember[0].Pattern != "/Users/me/.zshrc" {
		t.Fatalf("decision = %+v", d)
	}
}

// A path read out of command output is not trustworthy: the card must not
// offer a standing grant over a credentials folder because output named it.
