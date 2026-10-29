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
