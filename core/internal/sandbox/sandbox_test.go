package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/sandbox/sandboxtest"
)

func TestNoneLeavesArgvAlone(t *testing.T) {
	argv := []string{"/bin/sh", "-c", "echo hi"}
	got, err := Wrap(Policy{Mode: ModeNone}, argv)
	if err != nil || len(got) != 3 || got[0] != "/bin/sh" {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestUnknownModeIsRejected(t *testing.T) {
	if _, err := Wrap(Policy{Mode: "bogus"}, []string{"/bin/sh"}); err == nil {
		t.Fatal("want error")
	}
}

func TestEmptyArgvIsRejected(t *testing.T) {
	if _, err := Wrap(Policy{Mode: ModeWorkspaceWrite, Writable: []string{"/tmp"}}, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestBlockedOutputDetection(t *testing.T) {
	for _, s := range []string{
		"touch: /etc/x: Operation not permitted",
		"mkdir: cannot create directory '/usr/x': Read-only file system",
		"bash: /Users/a/x: Operation not permitted\n",
	} {
		if !LooksBlocked(s) {
			t.Errorf("want blocked: %q", s)
		}
	}
	if LooksBlocked("no such file or directory") {
		t.Error("plain failure is not a sandbox block")
	}
}

func requireSandbox(t *testing.T) {
	t.Helper()
	if Kind() == "" {
		t.Skip("no OS sandbox on this platform")
	}
	sandboxtest.Helper(t)
}

func run(t *testing.T, p Policy, script string) (string, error) {
	t.Helper()
	argv, err := Wrap(p, []string{"/bin/sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

func TestWorkspaceWriteConfinesWrites(t *testing.T) {
	requireSandbox(t)
	ws := t.TempDir()
	outside := t.TempDir()
	p := Policy{Mode: ModeWorkspaceWrite, Writable: []string{ws}, NoTemp: true}
	if out, err := run(t, p, "echo ok > "+filepath.Join(ws, "in.txt")); err != nil {
		t.Fatalf("inside write failed: %v %s", err, out)
	}
	out, err := run(t, p, "echo bad > "+filepath.Join(outside, "out.txt"))
	if err == nil {
		t.Fatal("outside write should fail")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "out.txt")); statErr == nil {
		t.Fatal("outside file was created")
	}
	if !LooksBlocked(out) {
		t.Fatalf("block not recognised: %q", out)
	}
	// Reads stay open everywhere.
	if err := os.WriteFile(filepath.Join(outside, "r.txt"), []byte("READ-OK"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, p, "cat "+filepath.Join(outside, "r.txt")); err != nil || out != "READ-OK" {
		t.Fatalf("read outside failed: %v %q", err, out)
	}
}

func TestReadOnlyBlocksWorkspaceWrites(t *testing.T) {
	requireSandbox(t)
	ws := t.TempDir()
	p := Policy{Mode: ModeReadOnly, Writable: []string{ws}, NoTemp: true}
	if _, err := run(t, p, "echo x > "+filepath.Join(ws, "f")); err == nil {
		t.Fatal("read-only should block workspace writes")
	}
	if out, err := run(t, p, "echo fine > /dev/null && ls "+ws); err != nil {
		t.Fatalf("read-only should allow reads and /dev/null: %v %s", err, out)
	}
}

func TestTempIsWritableByDefault(t *testing.T) {
	requireSandbox(t)
	ws := t.TempDir()
	p := Policy{Mode: ModeWorkspaceWrite, Writable: []string{ws}}
	if out, err := run(t, p, `f=$(mktemp) && echo x > "$f" && rm "$f"`); err != nil {
		t.Fatalf("temp write failed: %v %s", err, out)
	}
}

func TestSymlinkedRootIsResolved(t *testing.T) {
	requireSandbox(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ln")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	p := Policy{Mode: ModeWorkspaceWrite, Writable: []string{link}, NoTemp: true}
	if out, err := run(t, p, "echo ok > "+filepath.Join(link, "f")); err != nil {
		t.Fatalf("write through symlinked root failed: %v %s", err, out)
	}
}

func TestBlockedPathFindsTheDeniedPath(t *testing.T) {
	cases := map[string]string{
		"npm error Error: EPERM: operation not permitted, mkdir '/Users/a/.nvm/lib/node_modules/is-number'": "/Users/a/.nvm/lib/node_modules/is-number",
		"touch: /etc/x: Operation not permitted":                                                            "/etc/x",
		"mkdir: cannot create directory '/usr/x': Read-only file system":                                    "/usr/x",
		"/bin/sh: /Users/a/out.txt: Operation not permitted":                                                "/Users/a/out.txt",
		"PermissionError: [Errno 1] Operation not permitted: '/opt/y/z.txt'":                                "/opt/y/z.txt",
		"open /var/lib/foo/bar: operation not permitted":                                                    "/var/lib/foo/bar",
		"touch: cannot touch '/root/f': Permission denied":                                                  "/root/f",
		"cp: cannot create regular file '/opt/a b/c': Permission denied":                                    "/opt/a b/c",
		"zsh:1: permission denied: /etc/hosts":                                                              "/etc/hosts",
		"reading /src/a.go\nnpm ERR! EACCES: permission denied, open \"/opt/n/x.json\"":                     "/opt/n/x.json",
		"Operation not permitted":                                                                           "",
		"touch: x: Operation not permitted":                                                                 "",
		"wrote /tmp/a.txt fine":                                                                             "",
	}
	for in, want := range cases {
		if got := BlockedPath(in); got != want {
			t.Errorf("BlockedPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGrantedRootsAreWritableInEveryMode(t *testing.T) {
	for _, mode := range []Mode{ModeReadOnly, ModeWorkspaceWrite} {
		roots := writableRoots(Policy{Mode: mode, Granted: []string{"/opt/granted"}, NoTemp: true})
		if len(roots) != 1 || roots[0] != "/opt/granted" {
			t.Errorf("%s: roots %v", mode, roots)
		}
	}
}

func TestGrantedRootOpensOneFolderOnly(t *testing.T) {
	requireSandbox(t)
	ws, granted, other := t.TempDir(), t.TempDir(), t.TempDir()
	p := Policy{Mode: ModeReadOnly, Writable: []string{ws}, Granted: []string{granted}, NoTemp: true}
	if out, err := run(t, p, "echo ok > "+filepath.Join(granted, "f")); err != nil {
		t.Fatalf("granted write failed: %v %s", err, out)
	}
	if _, err := run(t, p, "echo x > "+filepath.Join(other, "f")); err == nil {
		t.Fatal("ungranted write should fail")
	}
	if _, err := run(t, p, "echo x > "+filepath.Join(ws, "f")); err == nil {
		t.Fatal("read-only still blocks the workspace")
	}
}
