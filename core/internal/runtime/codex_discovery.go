package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type codexDiscovery struct {
	once        sync.Once
	wg          sync.WaitGroup
	lifecycleMu sync.Mutex
	// Only metadata creation and the small exclusion file use this lock;
	// Codex requests and history loading never block a manual import on it.
	mu sync.Mutex
	// synced remembers, per session, the thread update time its history was
	// last synced at, so a scan skips threads Codex has not touched.
	synced sync.Map // session id -> int64
}

const codexHiddenFile = "codex-hidden.json"

// StartCodexDiscovery schedules one worker for all windows. Starting it does no
// process, network or disk I/O on the caller's path. The first scan waits for
// startup to settle; later scans also find threads created while Paladin is open.
func (r *AgentRuntime) StartCodexDiscovery() {
	r.codexDiscovery.lifecycleMu.Lock()
	defer r.codexDiscovery.lifecycleMu.Unlock()
	if r.sideCtx.Err() != nil {
		return
	}
	r.codexDiscovery.once.Do(func() {
		r.codexDiscovery.wg.Add(1)
		go func() {
			defer r.codexDiscovery.wg.Done()
			for delay := time.Second; discoveryWait(r.sideCtx, delay); delay = time.Minute {
				if err := r.discoverCodexSessions(r.sideCtx); err != nil && r.sideCtx.Err() == nil {
					log.Printf("codex: background discovery: %v", err)
				}
			}
		}()
	})
}

// discoveryPace scales discovery's pauses. Production paces so a scan
// yields to foreground turns; tests with many pages shorten it so the
// suite does not sleep the sum of every session's pause.
var discoveryPace = time.Duration(1)

func discoveryWait(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay * discoveryPace)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// onDiscoverySync is set by tests to observe which sessions a scan syncs.
var onDiscoverySync func(sessionID string)

func (r *AgentRuntime) discoverCodexSessions(ctx context.Context) error {
	// Codex is optional. Recheck on each background scan so installing it
	// while Paladin is open enables discovery without a restart.
	if !r.CodexStatus(false).Installed {
		return nil
	}
	client, err := r.codex.Client(ctx)
	if err != nil {
		return err
	}
	cursor := ""
	seen := map[string]bool{}
	type pending struct {
		meta    transcript.Meta
		updated int64
	}
	var load []pending
	idx, err := r.newCodexIndex()
	if err != nil {
		return err
	}
	for {
		pageCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		threads, next, err := client.ListThreads(pageCtx, codex.ThreadQuery{Cursor: cursor, Limit: 50})
		cancel()
		if err != nil {
			return err
		}
		changed := false
		for _, thread := range threads {
			if thread.ID == "" || thread.Source == "other" {
				continue
			}
			if !discoveryWait(ctx, 25*time.Millisecond) {
				return ctx.Err()
			}
			sess, created, err := r.ensureCodexSessionIn(idx, thread, thread.Cwd, false)
			if err != nil {
				log.Printf("codex: discover thread %s: %v", thread.ID, err)
				continue
			}
			if sess.Meta.SessionID == "" {
				continue
			} // explicitly hidden
			load = append(load, pending{sess.Meta, thread.UpdatedAt})
			changed = changed || created
		}
		if changed {
			r.hub.PublishSession("", "sessions/changed")
		}
		if next == "" {
			break
		}
		if seen[next] {
			return fmt.Errorf("codex discovery repeated a page cursor")
		}
		seen[next], cursor = true, next
	}
	// Metadata is visible before histories are hydrated. One worker, with a
	// pause between sessions, yields the expensive work to foreground turns.
	for _, p := range load {
		meta := p.meta
		if last, ok := r.codexDiscovery.synced.Load(meta.SessionID); ok && p.updated != 0 && last.(int64) == p.updated {
			continue
		}
		if !discoveryWait(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
		for len(r.hub.List()) > 0 {
			if !discoveryWait(ctx, time.Second) {
				return ctx.Err()
			}
		}
		if onDiscoverySync != nil {
			onDiscoverySync(meta.SessionID)
		}
		if changed, err := r.SyncSession(meta.SessionID, meta.Cwd); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("codex: background history %s: %v", meta.SessionID, err)
		} else {
			r.codexDiscovery.synced.Store(meta.SessionID, p.updated)
			if changed {
				r.hub.PublishSession(meta.SessionID, "session/refresh")
				r.hub.PublishSession("", "sessions/changed")
			}
		}
	}
	return nil
}

// codexIndex is what one scan needs to know about the sessions already
// linked to Codex threads, read once instead of for every thread.
type codexIndex struct {
	hidden map[string]bool
	linked map[string]transcript.Meta // thread id -> its session
}

func (r *AgentRuntime) newCodexIndex() (*codexIndex, error) {
	r.codexDiscovery.mu.Lock()
	defer r.codexDiscovery.mu.Unlock()
	hidden, err := r.hiddenCodexThreads()
	if err != nil {
		return nil, err
	}
	metas, err := r.transcripts.ListMeta("")
	if err != nil {
		return nil, err
	}
	idx := &codexIndex{hidden: hidden, linked: map[string]transcript.Meta{}}
	for _, m := range metas {
		if m.Harness != nil && m.Harness.Name == HarnessCodex {
			idx.linked[m.Harness.ThreadID] = m
		}
	}
	return idx, nil
}

// ensureCodexSession creates only the linked metadata, using the original
// workspace even if it was deleted. Viewing old history must not create that
// workspace again. A manual import can supply a replacement folder.
func (r *AgentRuntime) ensureCodexSession(thread codex.ThreadSummary, cwd string, explicit bool) (transcript.Session, bool, error) {
	idx, err := r.newCodexIndex()
	if err != nil {
		return transcript.Session{}, false, err
	}
	return r.ensureCodexSessionIn(idx, thread, cwd, explicit)
}

// ensureCodexSessionIn is ensureCodexSession against a scan's index, which it
// keeps current as it links new threads.
func (r *AgentRuntime) ensureCodexSessionIn(idx *codexIndex, thread codex.ThreadSummary, cwd string, explicit bool) (transcript.Session, bool, error) {
	r.codexDiscovery.mu.Lock()
	defer r.codexDiscovery.mu.Unlock()
	var err error
	if idx.hidden[thread.ID] {
		if !explicit {
			return transcript.Session{}, false, nil
		}
		delete(idx.hidden, thread.ID)
		if err := r.saveHiddenCodexThreads(idx.hidden); err != nil {
			return transcript.Session{}, false, err
		}
	}
	if held, ok := idx.linked[thread.ID]; ok {
		sess, err := r.transcripts.Load(held.Cwd, held.SessionID)
		return sess, false, err
	}
	if strings.TrimSpace(cwd) == "" {
		cwd, err = os.UserHomeDir()
		if err != nil {
			return transcript.Session{}, false, err
		}
	}
	sess, err := r.transcripts.Create(cwd, HarnessCodex, thread.Model)
	if err != nil {
		return sess, false, err
	}
	title := strings.TrimSpace(thread.Name)
	if title == "" {
		title = strings.TrimSpace(thread.Preview)
	}
	sess.Meta.Title = shortenTitle(title)
	if thread.CreatedAt > 0 {
		sess.Meta.CreatedAt = time.Unix(thread.CreatedAt, 0).UTC()
	}
	sess.Meta.Harness = &transcript.Harness{Name: HarnessCodex, ThreadID: thread.ID}
	if err := sess.SaveMeta(); err != nil {
		_ = r.transcripts.Delete(cwd, sess.Meta.SessionID)
		return sess, false, err
	}
	idx.linked[thread.ID] = sess.Meta
	return sess, true, nil
}

// Callers hold codexDiscovery.mu so a manual resume and background discovery
// cannot undo each other's exclusion updates.
func (r *AgentRuntime) hiddenCodexThreads() (map[string]bool, error) {
	hidden := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(r.dataDir, codexHiddenFile))
	if errors.Is(err, os.ErrNotExist) {
		return hidden, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &hidden); err != nil {
		return nil, err
	}
	if hidden == nil {
		hidden = map[string]bool{}
	}
	return hidden, nil
}

func (r *AgentRuntime) saveHiddenCodexThreads(hidden map[string]bool) error {
	data, err := json.Marshal(hidden)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(r.dataDir, codexHiddenFile), data)
}

func (r *AgentRuntime) hideCodexThread(threadID string) error {
	r.codexDiscovery.mu.Lock()
	defer r.codexDiscovery.mu.Unlock()
	hidden, err := r.hiddenCodexThreads()
	if err != nil {
		return err
	}
	hidden[threadID] = true
	return r.saveHiddenCodexThreads(hidden)
}
