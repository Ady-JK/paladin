//go:build unix

package procstat

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestRealSamplesReportABusyChild checks the whole path: the platform
// lister, an actual tracked process and the rate between two samples.
func TestRealSamplesReportABusyChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "while :; do :; done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	tr := New(defaultLister, os.Getpid())
	defer tr.Track("busy", cmd.Process.Pid)()
	tr.Snapshot() // warm: there is no rate for a first sighting
	time.Sleep(MinGap + 250*time.Millisecond)
	got := tr.Snapshot().Sessions["busy"]
	if got.Procs < 1 {
		t.Fatalf("busy tree = %+v, want at least the shell", got)
	}
	if got.CPU < 50 {
		t.Fatalf("busy shell CPU = %.1f%%, want roughly one core", got.CPU)
	}
}
