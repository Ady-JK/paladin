package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func readAs(t *testing.T, session, ws, path string, offset, limit int) ReadResponse {
	t.Helper()
	args := fmt.Sprintf(`{"path":%q,"offset":%d,"limit":%d}`, path, offset, limit)
	out, err := ReadTool{}.Call(json.RawMessage(args), &ToolCtx{Workspace: ws, SessionID: session, Cancel: context.Background()})
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var r ReadResponse
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func conflictReason(t *testing.T, err error) string {
	t.Helper()
	var ee *EngineError
	if !errors.As(err, &ee) || ee.Kind != "conflict" {
		t.Fatalf("want a conflict, got %v", err)
	}
	if ee.ResolvedPath == "" {
		t.Fatal("conflict has no resolved path")
	}
	return ee.Reason
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteCreateAndOverwriteFlagsAreAccurate(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "new.txt")
	// overwrite=true on an absent file creates it.
	res, err := NewEngine().Write("s1", ws, path, "a\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Overwritten {
		t.Fatalf("absent file with overwrite: %+v", res)
	}
	res, err = NewEngine().Write("s1", ws, path, "b\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || !res.Overwritten {
		t.Fatalf("existing file: %+v", res)
	}
}

func TestOverwriteRequiresAFullReadByThisSession(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewEngine().Write("s1", ws, path, "clobber\n", true)
	if r := conflictReason(t, err); r != "unread" {
		t.Fatalf("reason %q", r)
	}
	readAs(t, "s1", ws, path, 1, 100)
	if _, err := NewEngine().Write("s1", ws, path, "replaced\n", true); err != nil {
		t.Fatalf("overwrite after read: %v", err)
	}
	// Another session has not read it.
	_, err = NewEngine().Write("s2", ws, path, "other\n", true)
	if r := conflictReason(t, err); r != "unread" {
		t.Fatalf("reason %q", r)
	}
	if got, _ := os.ReadFile(path); string(got) != "replaced\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestExternalEditBetweenReadAndOverwriteConflicts(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("v1\n"), 0o644)
	readAs(t, "s1", ws, path, 1, 100)
	os.WriteFile(path, []byte("v2 external\n"), 0o644)
	_, err := NewEngine().Write("s1", ws, path, "mine\n", true)
	if r := conflictReason(t, err); r != "stale" {
		t.Fatalf("reason %q", r)
	}
	if got, _ := os.ReadFile(path); string(got) != "v2 external\n" {
		t.Fatalf("external change clobbered: %q", got)
	}
	// Re-reading clears the conflict.
	readAs(t, "s1", ws, path, 1, 100)
	if _, err := NewEngine().Write("s1", ws, path, "merged\n", true); err != nil {
		t.Fatal(err)
	}
}

func TestSecondSessionOverwriteAfterFirstWritesConflicts(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("v1\n"), 0o644)
	readAs(t, "s1", ws, path, 1, 100)
	readAs(t, "s2", ws, path, 1, 100)
	if _, err := NewEngine().Write("s1", ws, path, "from s1\n", true); err != nil {
		t.Fatal(err)
	}
	_, err := NewEngine().Write("s2", ws, path, "from s2\n", true)
	if r := conflictReason(t, err); r != "stale" {
		t.Fatalf("reason %q", r)
	}
	if got, _ := os.ReadFile(path); string(got) != "from s1\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestConcurrentEditsFromTwoSessionsAreAllApplied(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("MARK\n"), 0o644)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := []string{"s1", "s2"}[i%2]
			_, err := NewEngine().Edit(session, ws, path, "MARK\n", fmt.Sprintf("MARK\nline %d\n", i), false)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	for i := 0; i < 40; i++ {
		if !strings.Contains(string(got), fmt.Sprintf("line %d\n", i)) {
			t.Fatalf("edit %d lost:\n%s", i, got)
		}
	}
	noTempFiles(t, ws)
}

func TestExternalEditDuringEditConflictsAndWritesNothing(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("hello\n"), 0o644)
	beforeCommit = func(p string) { os.WriteFile(p, []byte("hello external\n"), 0o644) }
	defer func() { beforeCommit = func(string) {} }()
	_, err := NewEngine().Edit("s1", ws, path, "hello", "bye", false)
	if r := conflictReason(t, err); r != "changed" {
		t.Fatalf("reason %q", r)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello external\n" {
		t.Fatalf("file = %q", got)
	}
	noTempFiles(t, ws)
}

func TestExternalCreateDuringWriteConflicts(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	beforeCommit = func(p string) { os.WriteFile(p, []byte("raced\n"), 0o644) }
	defer func() { beforeCommit = func(string) {} }()
	_, err := NewEngine().Write("s1", ws, path, "mine\n", false)
	if r := conflictReason(t, err); r != "changed" {
		t.Fatalf("reason %q", r)
	}
	if got, _ := os.ReadFile(path); string(got) != "raced\n" {
		t.Fatalf("file = %q", got)
	}
	noTempFiles(t, ws)
}

func TestFailedRenameLeavesOriginalAndNoTemp(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("keep\n"), 0o644)
	readAs(t, "s1", ws, path, 1, 100)
	renameFile = func(string, string) error { return errors.New("injected rename failure") }
	defer func() { renameFile = os.Rename }()
	if _, err := NewEngine().Write("s1", ws, path, "new\n", true); err == nil {
		t.Fatal("write succeeded despite a failed rename")
	}
	if _, err := NewEngine().Edit("s1", ws, path, "keep", "gone", false); err == nil {
		t.Fatal("edit succeeded despite a failed rename")
	}
	if got, _ := os.ReadFile(path); string(got) != "keep\n" {
		t.Fatalf("file = %q", got)
	}
	noTempFiles(t, ws)
}

func TestOverwritePreservesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	ws := t.TempDir()
	path := filepath.Join(ws, "run.sh")
	os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
	os.Chmod(path, 0o750)
	readAs(t, "s1", ws, path, 1, 100)
	if _, err := NewEngine().Write("s1", ws, path, "#!/bin/sh\necho hi\n", true); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestAmbiguousEditWritesNothing(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("x\nx\n"), 0o644)
	_, err := NewEngine().Edit("s1", ws, path, "x", "y", false)
	var ee *EngineError
	if !errors.As(err, &ee) || ee.Kind != "ambiguous" || len(ee.Occurrences) != 2 {
		t.Fatalf("got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "x\nx\n" {
		t.Fatalf("file = %q", got)
	}
}

func bigFile(t *testing.T, path string, lines int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPagedReadToEOFIsABaseline(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "big.txt")
	bigFile(t, path, 5000)
	r := readAs(t, "s1", ws, path, 1, 2000)
	if !r.HasMore {
		t.Fatal("first page should have more")
	}
	// A partial read is not a baseline.
	_, err := NewEngine().Write("s1", ws, path, "x\n", true)
	if r := conflictReason(t, err); r != "unread" {
		t.Fatalf("reason %q", r)
	}
	readAs(t, "s1", ws, path, 2001, 2000)
	last := readAs(t, "s1", ws, path, 4001, 2000)
	if last.HasMore {
		t.Fatal("last page should not have more")
	}
	if _, err := NewEngine().Write("s1", ws, path, "x\n", true); err != nil {
		t.Fatalf("overwrite after paging to EOF: %v", err)
	}
}

func TestPagedReadWithSkipOrChangeIsNotABaseline(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "big.txt")
	bigFile(t, path, 5000)
	// Skipping the middle page.
	readAs(t, "s1", ws, path, 1, 2000)
	readAs(t, "s1", ws, path, 4001, 2000)
	if _, err := NewEngine().Write("s1", ws, path, "x\n", true); err == nil {
		t.Fatal("skipped page counted as a full read")
	}
	// The file changing between pages.
	readAs(t, "s2", ws, path, 1, 2000)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("appended\n")
	f.Close()
	readAs(t, "s2", ws, path, 2001, 2000)
	readAs(t, "s2", ws, path, 4001, 2000)
	if _, err := NewEngine().Write("s2", ws, path, "x\n", true); err == nil {
		t.Fatal("pages of different versions counted as a full read")
	}
}

func TestEditWithStaleBaselineDropsIt(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("a\nb\n"), 0o644)
	readAs(t, "s1", ws, path, 1, 100)
	os.WriteFile(path, []byte("a\nb\nexternal\n"), 0o644)
	// The anchored edit still applies on top of the external change...
	if _, err := NewEngine().Edit("s1", ws, path, "a\n", "A\n", false); err != nil {
		t.Fatal(err)
	}
	// ...but the session never saw "external", so a blind overwrite is refused.
	_, err := NewEngine().Write("s1", ws, path, "clobber\n", true)
	if r := conflictReason(t, err); r != "unread" {
		t.Fatalf("reason %q", r)
	}
}

func TestEditAfterFullReadKeepsTheBaseline(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("a\nb\n"), 0o644)
	readAs(t, "s1", ws, path, 1, 100)
	if _, err := NewEngine().Edit("s1", ws, path, "a\n", "A\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine().Write("s1", ws, path, "whole\n", true); err != nil {
		t.Fatalf("overwrite after read+edit: %v", err)
	}
}

func TestSymlinkAliasSharesTheBaseline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	ws := t.TempDir()
	path := filepath.Join(ws, "real.txt")
	alias := filepath.Join(ws, "alias.txt")
	os.WriteFile(path, []byte("v1\n"), 0o644)
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	readAs(t, "s1", ws, alias, 1, 100)
	os.WriteFile(path, []byte("v2\n"), 0o644)
	if _, err := NewEngine().Write("s1", ws, path, "mine\n", true); err == nil {
		t.Fatal("stale write through the real path accepted")
	}
}

func TestWriteToolReportsConflict(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	os.WriteFile(path, []byte("v1\n"), 0o644)
	ctx := &ToolCtx{Workspace: ws, SessionID: "s1", Cancel: context.Background()}
	_, err := NewWriteTool(NewEngine()).Call(json.RawMessage(`{"path":"`+path+`","content":"x","overwrite":true}`), ctx)
	if err == nil {
		t.Fatal("blind overwrite accepted")
	}
	content := err.(*ToolError).Content
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) != nil || payload["conflict"] != true || payload["resolved_path"] != path {
		t.Fatalf("payload %q", content)
	}
	if !strings.Contains(payload["error"].(string), "read") {
		t.Fatalf("message should tell the model to read: %v", payload["error"])
	}
}

func TestReadWithTruncatedLineIsNotABaseline(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "min.js")
	long := strings.Repeat("v", maxLineRunes+50)
	if err := os.WriteFile(path, []byte("head\n"+long+"\ntail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := readAs(t, "s1", ws, path, 1, 2000)
	if r.HasMore || !strings.Contains(r.Content, truncatedMarker) {
		t.Fatalf("setup: %+v", r.StopReason)
	}
	_, err := NewEngine().Write("s1", ws, path, "x\n", true)
	if r := conflictReason(t, err); r != ConflictTruncated {
		t.Fatalf("reason %q", r)
	}
	if !strings.Contains(err.Error(), "edit") {
		t.Fatalf("message should point to edit: %v", err)
	}
	// Paged: the long line sits on the first page only.
	readAs(t, "s2", ws, path, 1, 2)
	readAs(t, "s2", ws, path, 3, 2)
	_, err = NewEngine().Write("s2", ws, path, "x\n", true)
	if r := conflictReason(t, err); r != ConflictTruncated {
		t.Fatalf("paged reason %q", r)
	}
	// edit still works and does not grant a baseline.
	if _, err := NewEngine().Edit("s1", ws, path, "head", "HEAD", false); err != nil {
		t.Fatalf("edit after a clipped read: %v", err)
	}
	_, err = NewEngine().Write("s1", ws, path, "x\n", true)
	if r := conflictReason(t, err); r != ConflictUnread {
		t.Fatalf("after edit: reason %q", r)
	}
}
