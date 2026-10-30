package runtime

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Two sessions mutating one folder at once must each record only their
// own files: the snapshot → run → snapshot bracket is serialised per
// workspace.
func TestConcurrentStepsInOneFolderAreAttributedCorrectly(t *testing.T) {
	rt, ws, _ := undoRuntime(t)
	repo := rt.snapRepo(ws)
	if repo == nil {
		t.Fatal("no snapshot repo")
	}
	dirA, dirB := t.TempDir(), t.TempDir()
	recA := &stepRecorder{repo: repo, path: filepath.Join(dirA, stepsFile)}
	recB := &stepRecorder{repo: repo, path: filepath.Join(dirB, stepsFile)}
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		recA.Track(context.Background(), "a1", "write", func() tools.ToolResult {
			os.WriteFile(filepath.Join(ws, "a.txt"), []byte("a\n"), 0o644)
			close(started)
			time.Sleep(300 * time.Millisecond)
			return tools.ToolResult{}
		})
	}()
	go func() {
		defer wg.Done()
		<-started
		recB.Track(context.Background(), "b1", "write", func() tools.ToolResult {
			os.WriteFile(filepath.Join(ws, "b.txt"), []byte("b\n"), 0o644)
			return tools.ToolResult{}
		})
	}()
	wg.Wait()
	stepsA, _ := readSteps(dirA)
	stepsB, _ := readSteps(dirB)
	if len(stepsA) != 1 || strings.Join(stepsA[0].Files, ",") != "a.txt" {
		t.Fatalf("session A steps %+v", stepsA)
	}
	if len(stepsB) != 1 || strings.Join(stepsB[0].Files, ",") != "b.txt" {
		t.Fatalf("session B steps %+v", stepsB)
	}
}

// A Track waiting for another session's bracket gives up when its turn
// is cancelled, without running the tool.
func TestTrackWaitHonoursCancellation(t *testing.T) {
	rt, ws, _ := undoRuntime(t)
	repo := rt.snapRepo(ws)
	rec := &stepRecorder{repo: repo, path: filepath.Join(t.TempDir(), stepsFile)}
	release := make(chan struct{})
	holding := make(chan struct{})
	go rec.Track(context.Background(), "hold", "shell", func() tools.ToolResult {
		close(holding)
		<-release
		return tools.ToolResult{}
	})
	<-holding
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ran := false
	res := rec.Track(ctx, "c2", "write", func() tools.ToolResult { ran = true; return tools.ToolResult{} })
	if ran || !res.IsError {
		t.Fatalf("ran=%v result=%+v", ran, res)
	}
}

// Session B changes a file session A wrote; A's revert must leave B's
// version alone.
func TestRevertKeepsASecondSessionsLaterChange(t *testing.T) {
	rt, ws, sidA := undoRuntime(t, "echo from-a > shared.txt", "", "echo from-b > shared.txt", "")
	turn(t, rt, sidA, ws, "a writes")
	sessB, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sessB.SessionID, ws, "b writes")
	res, err := rt.RevertSession(sidA, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Skipped, ",") != "shared.txt" || len(res.Restored) != 0 {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "shared.txt")) != "from-b" {
		t.Fatal("second session's change was overwritten")
	}
}

// One file's restore fails (its folder became read-only) while another
// succeeds: the result says which is which and the revert records only
// what it really wrote.
func TestRevertReportsPartialRestoreFailure(t *testing.T) {
	if goruntime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions as a non-root user")
	}
	rt, ws, sid := undoRuntime(t, "mkdir -p ro && echo new > ro/a.txt && echo changed > b.txt", "")
	os.WriteFile(filepath.Join(ws, "b.txt"), []byte("orig\n"), 0o644)
	turn(t, rt, sid, ws, "edit")
	os.Chmod(filepath.Join(ws, "ro"), 0o555)
	defer os.Chmod(filepath.Join(ws, "ro"), 0o755)
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "b.txt" || strings.Join(res.Failed, ",") != "ro/a.txt" {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "b.txt")) != "orig" || readFile(t, filepath.Join(ws, "ro", "a.txt")) != "new" {
		t.Fatal("files not as reported")
	}
}
