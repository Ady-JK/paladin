//go:build unix

package procstat

import (
	"os"
	"os/exec"
	"testing"
)

func TestRealPSSeesATrackedChildTree(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 5 & sleep 5; wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	tr := New(listPS, os.Getpid())
	defer tr.Track("s1", cmd.Process.Pid)()
	snap := tr.Snapshot()
	if !snap.Supported {
		t.Skip("ps unavailable")
	}
	if snap.Self.RSS <= 0 {
		t.Errorf("self rss = %d, want > 0", snap.Self.RSS)
	}
	if got := snap.Sessions["s1"]; got.Procs < 1 || got.RSS <= 0 {
		t.Errorf("s1 = %+v, want the shell tree", got)
	}
}
