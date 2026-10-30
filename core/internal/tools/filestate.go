// filestate.go keeps write/edit from clobbering content a session has
// not seen. Every guarantee here is in-process only: the path locks
// serialise Paladin's own tools across sessions, and the version check
// narrows (but cannot close) the window in which an external program
// can change a file between Paladin's last look and its rename.

package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Conflict reasons reported in EngineError.Reason.
const (
	ConflictUnread  = "unread"  // the session never read the whole current file
	ConflictStale   = "stale"   // the file changed since the session read or wrote it
	ConflictChanged = "changed" // the file changed while the tool was preparing its write
	// ConflictTruncated: the session read to EOF, but read cut at least
	// one long line, so it never saw the whole file.
	ConflictTruncated = "truncated"
)

// maxTrackedFiles bounds the version table; an evicted entry only
// costs the model a re-read.
const maxTrackedFiles = 8192

// pageProgress is a session paging through a file from line 1: the
// file must look the same on every page for the pages to add up to a
// full read.
type pageProgress struct {
	size int64
	mod  time.Time
	next int
	// clipped: an earlier page truncated a line.
	clipped bool
}

type fileTracker struct {
	mu     sync.Mutex
	seen   map[string]string
	paging map[string]pageProgress
	// clipped holds the hash of a version the session read to EOF with
	// truncated lines: not a baseline, but it explains the refusal.
	clipped map[string]string
}

var fileVersions = &fileTracker{seen: map[string]string{}, paging: map[string]pageProgress{}, clipped: map[string]string{}}

func versionKey(session, path string) string { return session + "\x00" + canonicalPath(path) }

// canonicalPath folds symlink aliases of an existing file (or of the
// existing part of a new path) onto one key.
func canonicalPath(path string) string {
	if real, ok := resolveExisting(cleanPath(path)); ok {
		return real
	}
	return cleanPath(path)
}

func (t *fileTracker) seenHash(session, path string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.seen[versionKey(session, path)]
	return h, ok
}

func (t *fileTracker) record(session, path, hash string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := versionKey(session, path)
	if _, ok := t.seen[key]; !ok && len(t.seen) >= maxTrackedFiles {
		for k := range t.seen {
			delete(t.seen, k)
			break
		}
	}
	t.seen[key] = hash
	delete(t.paging, key)
	delete(t.clipped, key)
}

// clippedHash is the version the session read to EOF with truncated
// lines, if any.
func (t *fileTracker) clippedHash(session, path string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.clipped[versionKey(session, path)]
	return h, ok
}

func (t *fileTracker) forget(session, path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := versionKey(session, path)
	delete(t.seen, key)
	delete(t.paging, key)
	delete(t.clipped, key)
}

// notePage records a read page. A read from line 1 to EOF is a full
// read; so is a run of consecutive pages from line 1 to EOF during
// which the file's size and mtime never changed. A page that truncated
// a line spoils the run: reaching EOF then only notes the version as
// clipped. hash is the whole file's content hash and is only
// meaningful when the page hit EOF.
func (t *fileTracker) notePage(session, path string, info os.FileInfo, offset, end int, hasMore, clipped bool, hash string) {
	t.mu.Lock()
	key := versionKey(session, path)
	p, paging := t.paging[key]
	same := paging && offset == p.next && p.size == info.Size() && p.mod.Equal(info.ModTime())
	switch {
	case offset == 1 && !hasMore, same && !hasMore:
		if clipped || (offset != 1 && p.clipped) {
			delete(t.paging, key)
			if len(t.clipped) >= maxTrackedFiles {
				t.clipped = map[string]string{}
			}
			t.clipped[key] = hash
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()
		t.record(session, path, hash)
		return
	case offset == 1:
		if len(t.paging) >= maxTrackedFiles {
			t.paging = map[string]pageProgress{}
		}
		t.paging[key] = pageProgress{size: info.Size(), mod: info.ModTime(), next: end + 1, clipped: clipped}
	case same:
		p.next = end + 1
		p.clipped = p.clipped || clipped
		t.paging[key] = p
	default:
		delete(t.paging, key)
	}
	t.mu.Unlock()
}

// pathLocks serialise resolve → read → validate → commit per file
// across all sessions in this process.
var pathLocks = struct {
	mu sync.Mutex
	m  map[string]*pathLock
}{m: map[string]*pathLock{}}

type pathLock struct {
	sync.Mutex
	refs int
}

func lockPath(path string) (unlock func()) {
	key := canonicalPath(path)
	pathLocks.mu.Lock()
	l := pathLocks.m[key]
	if l == nil {
		l = &pathLock{}
		pathLocks.m[key] = l
	}
	l.refs++
	pathLocks.mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		pathLocks.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(pathLocks.m, key)
		}
		pathLocks.mu.Unlock()
	}
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hashFile is the content hash of path, or "" when it does not exist.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Test seams: beforeCommit runs just before the final version check,
// renameFile commits the temp file.
var (
	beforeCommit = func(string) {}
	renameFile   = os.Rename
)

// errChanged is returned by a commit check that saw the file move on.
var errChanged = errors.New("file changed during the write")

// atomicReplace writes data to a temp file beside path, syncs it, runs
// check (which may refuse the commit), and renames it over path. The
// temp file is removed on every failure path, and there is no
// non-atomic fallback. The rename is atomic on POSIX filesystems; an
// external writer between check and rename can still be lost.
func atomicReplace(path string, data []byte, perm os.FileMode, check func() error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".paladin-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	beforeCommit(path)
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	if err := renameFile(name, path); err != nil {
		return err
	}
	committed = true
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable where the platform allows it.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}
