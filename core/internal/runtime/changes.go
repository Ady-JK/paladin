package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const maxSessionDiff = 256 * 1024

type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type ChangeTurn struct {
	Turn    int           `json:"turn"`
	Files   []ChangedFile `json:"files"`
	CallIDs []string      `json:"callIds"`
}

type SessionChangesView struct {
	Available bool          `json:"available"`
	Turns     []ChangeTurn  `json:"turns"`
	Files     []ChangedFile `json:"files"`
}

type SessionDiffView struct {
	Path      string `json:"path"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

type fileState struct{ first, last string }

func changeStatus(f fileState) string {
	if f.first == "" && f.last != "" {
		return "created"
	}
	if f.first != "" && f.last == "" {
		return "deleted"
	}
	return "modified"
}

func changeFiles(states map[string]fileState) []ChangedFile {
	files := make([]ChangedFile, 0, len(states))
	for path, st := range states {
		files = append(files, ChangedFile{Path: path, Status: changeStatus(st)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// visibleTurns loads the session's turn snapshots, hiding the turns a
// legacy pending revert would remove, plus the messages those turns
// cover (needed to map tool calls to their turn).
func (r *AgentRuntime) visibleTurns(sessionID, cwd string) (*snapshot.Repo, []TurnSnapshot, []transcript.Message, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	repo := r.snapRepo(sess.Meta.Cwd)
	if repo == nil {
		return nil, nil, nil, nil
	}
	turns, err := readTurnSnapshots(sess.Dir)
	if err != nil {
		return nil, nil, nil, err
	}
	msgs, err := sess.History()
	if err != nil {
		return nil, nil, nil, err
	}
	msgs = visibleMessages(sess.Meta, msgs)
	if rv := sess.Meta.Revert; rv != nil {
		kept := turns[:0]
		for _, t := range turns {
			if t.Turn < rv.Index {
				kept = append(kept, t)
			}
		}
		turns = kept
	}
	return repo, turns, msgs, nil
}

// turnCallIDs groups every tool call under the turn whose user row
// starts before it, keyed by that turn's message index. The renderer
// matches a just-settled call to its turn while the turn is running.
func turnCallIDs(msgs []transcript.Message, turns []TurnSnapshot) map[int][]string {
	out := make(map[int][]string, len(turns))
	if len(turns) == 0 {
		return out
	}
	ti := -1
	for i, m := range msgs {
		for ti+1 < len(turns) && turns[ti+1].Turn <= i {
			ti++
		}
		if ti < 0 || m.Role != transcript.RoleAssistant || m.ToolCalls == nil {
			continue
		}
		for _, call := range *m.ToolCalls {
			if call.ID != "" {
				out[turns[ti].Turn] = append(out[turns[ti].Turn], call.ID)
			}
		}
	}
	return out
}

// turnStates compares two trees and returns each path's first and last
// entry, so a created/deleted/modified status can be derived.
func turnStates(ctx context.Context, repo *snapshot.Repo, from, to string) (map[string]fileState, error) {
	changed, err := repo.Changed(ctx, from, to)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, nil
	}
	before, err := repo.Entries(ctx, from, changed)
	if err != nil {
		return nil, err
	}
	after, err := repo.Entries(ctx, to, changed)
	if err != nil {
		return nil, err
	}
	states := make(map[string]fileState, len(changed))
	for _, p := range changed {
		states[p] = fileState{first: before[p], last: after[p]}
	}
	return states, nil
}

// SessionChanges groups files by the user turn that changed them. A
// turn is compared with the next turn's snapshot, and the last turn
// with the folder now, so no separate "after" snapshot is needed.
func (r *AgentRuntime) SessionChanges(sessionID, cwd string) (SessionChangesView, error) {
	view := SessionChangesView{Turns: []ChangeTurn{}, Files: []ChangedFile{}}
	repo, turns, msgs, err := r.visibleTurns(sessionID, cwd)
	if err != nil || repo == nil {
		return view, err
	}
	view.Available = true
	if len(turns) == 0 {
		return view, nil
	}
	callIDs := turnCallIDs(msgs, turns)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	current, err := repo.Snapshot(ctx)
	if err != nil {
		return view, err
	}
	for i, ts := range turns {
		to := current
		if i+1 < len(turns) {
			to = turns[i+1].Tree
		}
		states, err := turnStates(ctx, repo, ts.Tree, to)
		if err != nil {
			return view, err
		}
		if len(states) > 0 {
			view.Turns = append(view.Turns, ChangeTurn{Turn: ts.Turn, Files: changeFiles(states), CallIDs: callIDs[ts.Turn]})
		}
	}
	all, err := turnStates(ctx, repo, turns[0].Tree, current)
	if err != nil {
		return view, err
	}
	view.Files = changeFiles(all)
	return view, nil
}

// SessionDiff shows the patch for one file in one turn, or across the
// whole session when turn is negative.
func (r *AgentRuntime) SessionDiff(sessionID, cwd string, turn int, path string) (SessionDiffView, error) {
	if err := snapshot.CheckPath(path); err != nil {
		return SessionDiffView{}, err
	}
	if turn < -1 {
		return SessionDiffView{}, fmt.Errorf("invalid turn %d", turn)
	}
	repo, turns, _, err := r.visibleTurns(sessionID, cwd)
	if err != nil {
		return SessionDiffView{}, err
	}
	if repo == nil || len(turns) == 0 {
		return SessionDiffView{}, fmt.Errorf("snapshots are unavailable for this session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	current, err := repo.Snapshot(ctx)
	if err != nil {
		return SessionDiffView{}, err
	}
	from := turns[0].Tree
	to := current
	if turn >= 0 {
		found := false
		for i, ts := range turns {
			if ts.Turn != turn {
				continue
			}
			from, found = ts.Tree, true
			to = current
			if i+1 < len(turns) {
				to = turns[i+1].Tree
			}
			break
		}
		if !found {
			return SessionDiffView{}, fmt.Errorf("file was not changed by this session in the selected scope")
		}
	}
	changed, err := repo.Changed(ctx, from, to)
	if err != nil {
		return SessionDiffView{}, err
	}
	found := false
	for _, p := range changed {
		if p == path {
			found = true
			break
		}
	}
	if !found {
		return SessionDiffView{}, fmt.Errorf("file was not changed by this session in the selected scope")
	}
	view := SessionDiffView{Path: path}
	patch, err := repo.DiffFile(ctx, from, to, path)
	if err != nil {
		return SessionDiffView{}, err
	}
	if len(patch) > maxSessionDiff {
		view.Truncated = true
		patch = patch[:maxSessionDiff]
	}
	view.Patch = strings.TrimRight(patch, "\n")
	return view, nil
}
