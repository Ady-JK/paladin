package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/snapshot"
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

// visibleChangeSteps uses the same step ledger and pending-revert cutoff
// as undo. No project Git repository or current worktree state is needed.
func (r *AgentRuntime) visibleChangeSteps(sessionID, cwd string) (*snapshot.Repo, []Step, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, nil, err
	}
	repo := r.snapRepo(sess.Meta.Cwd)
	if repo == nil {
		return nil, nil, nil
	}
	steps, err := readSteps(sess.Dir)
	if err != nil {
		return nil, nil, err
	}
	if rv := sess.Meta.Revert; rv != nil {
		kept := steps[:0]
		for _, step := range steps {
			if step.Turn < rv.Index {
				kept = append(kept, step)
			}
		}
		steps = kept
	}
	return repo, steps, nil
}

// SessionChanges groups recorded file changes by user turn and across
// the whole session. Status compares the first and last recorded entry.
func (r *AgentRuntime) SessionChanges(sessionID, cwd string) (SessionChangesView, error) {
	view := SessionChangesView{Turns: []ChangeTurn{}, Files: []ChangedFile{}}
	repo, steps, err := r.visibleChangeSteps(sessionID, cwd)
	if err != nil || repo == nil {
		return view, err
	}
	view.Available = true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	byTurn := map[int]map[string]fileState{}
	callIDs := map[int][]string{}
	all := map[string]fileState{}
	for _, step := range steps {
		before, err := repo.Entries(ctx, step.Before, step.Files)
		if err != nil {
			return view, err
		}
		after, err := repo.Entries(ctx, step.After, step.Files)
		if err != nil {
			return view, err
		}
		if byTurn[step.Turn] == nil {
			byTurn[step.Turn] = map[string]fileState{}
		}
		callIDs[step.Turn] = append(callIDs[step.Turn], step.CallID)
		for _, path := range step.Files {
			for _, target := range []map[string]fileState{byTurn[step.Turn], all} {
				st, seen := target[path]
				if !seen {
					st.first = before[path]
				}
				st.last = after[path]
				target[path] = st
			}
		}
	}
	turns := make([]int, 0, len(byTurn))
	for turn := range byTurn {
		turns = append(turns, turn)
	}
	sort.Ints(turns)
	for _, turn := range turns {
		view.Turns = append(view.Turns, ChangeTurn{Turn: turn, Files: changeFiles(byTurn[turn]), CallIDs: callIDs[turn]})
	}
	view.Files = changeFiles(all)
	return view, nil
}

// SessionDiff shows only snapshots belonging to this session and scope.
// Repeating a file across steps preserves attribution instead of mixing
// in intervening edits from another session to the same path.
func (r *AgentRuntime) SessionDiff(sessionID, cwd string, turn int, path string) (SessionDiffView, error) {
	if err := snapshot.CheckPath(path); err != nil {
		return SessionDiffView{}, err
	}
	if turn < -1 {
		return SessionDiffView{}, fmt.Errorf("invalid turn %d", turn)
	}
	repo, steps, err := r.visibleChangeSteps(sessionID, cwd)
	if err != nil {
		return SessionDiffView{}, err
	}
	if repo == nil {
		return SessionDiffView{}, fmt.Errorf("snapshots are unavailable for this session")
	}
	view := SessionDiffView{Path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var patch strings.Builder
	found := false
	for _, step := range steps {
		if turn >= 0 && step.Turn != turn {
			continue
		}
		for _, changed := range step.Files {
			if changed != path {
				continue
			}
			found = true
			part, err := repo.DiffFile(ctx, step.Before, step.After, path)
			if err != nil {
				return SessionDiffView{}, err
			}
			if patch.Len()+len(part) > maxSessionDiff {
				patch.WriteString(part[:max(0, maxSessionDiff-patch.Len())])
				view.Truncated = true
				break
			}
			patch.WriteString(part)
			break
		}
		if view.Truncated {
			break
		}
	}
	if !found {
		return SessionDiffView{}, fmt.Errorf("file was not changed by this session in the selected scope")
	}
	view.Patch = patch.String()
	return view, nil
}
