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
	var load []transcript.Meta
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
			sess, created, err := r.ensureCodexSession(thread, thread.Cwd, false)
			if err != nil {
				log.Printf("codex: discover thread %s: %v", thread.ID, err)
				continue
			}
			if sess.Meta.SessionID == "" {
				continue
			} // explicitly hidden
			load = append(load, sess.Meta)
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
	for _, meta := range load {
		if !discoveryWait(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
		for len(r.hub.List()) > 0 {
			if !discoveryWait(ctx, time.Second) {
				return ctx.Err()
			}
		}
		if changed, err := r.SyncSession(meta.SessionID, meta.Cwd); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("codex: background history %s: %v", meta.SessionID, err)
		} else if changed {
			r.hub.PublishSession(meta.SessionID, "session/refresh")
			r.hub.PublishSession("", "sessions/changed")
		}
	}
	return nil
}

// ensureCodexSession creates only the linked metadata, using the original
// workspace even if it was deleted. Viewing old history must not create that
// workspace again. A manual import can supply a replacement folder.
func (r *AgentRuntime) ensureCodexSession(thread codex.ThreadSummary, cwd string, explicit bool) (transcript.Session, bool, error) {
	r.codexDiscovery.mu.Lock()
	defer r.codexDiscovery.mu.Unlock()
	hidden, err := r.hiddenCodexThreads()
	if err != nil {
		return transcript.Session{}, false, err
	}
	if hidden[thread.ID] {
		if !explicit {
			return transcript.Session{}, false, nil
		}
		delete(hidden, thread.ID)
		if err := r.saveHiddenCodexThreads(hidden); err != nil {
			return transcript.Session{}, false, err
		}
	}
	metas, err := r.transcripts.ListMeta("")
	if err != nil {
		return transcript.Session{}, false, err
	}
	for _, held := range metas {
		if held.Harness != nil && held.Harness.Name == HarnessCodex && held.Harness.ThreadID == thread.ID {
			sess, err := r.transcripts.Load(held.Cwd, held.SessionID)
			return sess, false, err
		}
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
