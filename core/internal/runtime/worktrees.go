package runtime

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/Ady-JK/paladin/core/internal/worktree"
)

const gitTimeout = 2 * time.Minute

// OpenOpts are the options for opening or forking a session.
type OpenOpts struct {
	// Worktree puts the new session in its own git worktree.
	Worktree bool
	// Name names the worktree branch (paladin/<slug>).
	Name string
}

func worktreeSection(wt *transcript.Worktree) string {
	base := wt.BaseBranch
	if base == "" {
		base = "the original checkout"
	}
	return "# Git worktree\n\n" +
		"This session works in its own git worktree on branch `" + wt.Branch + "`, created from `" + base + "`.\n" +
		"- Work only inside the current workspace (" + wt.Path + "). Paths from earlier messages may point at the original repository (" + wt.RepoRoot + "); use the matching path in this worktree instead and never edit files in the original repository.\n" +
		"- The user merges this branch back from the app; don't merge, rebase or push it yourself unless asked."
}

func toTranscriptWorktree(info worktree.Info, origin string) *transcript.Worktree {
	return &transcript.Worktree{RepoRoot: info.RepoRoot, Path: info.Path, Branch: info.Branch, Base: info.Base, BaseBranch: info.BaseBranch, Origin: origin}
}

func fromTranscriptWorktree(wt *transcript.Worktree) worktree.Info {
	return worktree.Info{RepoRoot: wt.RepoRoot, Path: wt.Path, Branch: wt.Branch, Base: wt.Base, BaseBranch: wt.BaseBranch}
}

// OpenSessionWith opens an existing session, or creates one in cwd,
// optionally in a fresh git worktree of cwd's repository.
func (r *AgentRuntime) OpenSessionWith(sessionID, cwd string, opts OpenOpts) (Session, error) {
	if strings.TrimSpace(sessionID) != "" || !opts.Worktree {
		return r.OpenSession(sessionID, cwd)
	}
	if strings.TrimSpace(cwd) == "" {
		return Session{}, fmt.Errorf("cwd is required to create a session")
	}
	entry, _, provider, err := r.resolveProvider("", "")
	if err != nil {
		return Session{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	info, wcwd, err := r.worktrees.Create(ctx, cwd, opts.Name)
	if err != nil {
		return Session{}, err
	}
	sess, err := r.transcripts.Create(wcwd, entry.Name, provider.Model())
	if err == nil {
		sess.Meta.Worktree = toTranscriptWorktree(info, filepath.Clean(cwd))
		err = sess.SaveMeta()
	}
	if err != nil {
		_ = worktree.Remove(ctx, info, true)
		return Session{}, err
	}
	return Session{SessionID: sess.Meta.SessionID, Cwd: sess.Meta.Cwd, Provider: entry.Name, Model: provider.Model(),
		Worktree: session.WorktreeView(sess.Meta.Worktree)}, nil
}

// ForkSessionWith forks a session, optionally into a fresh worktree of
// its folder's repository. A plain fork stays in the source's folder.
func (r *AgentRuntime) ForkSessionWith(sessionID, cwd string, opts OpenOpts) (session.SessionInfo, error) {
	if !opts.Worktree {
		return r.ForkSession(sessionID, cwd)
	}
	src, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return session.SessionInfo{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	origin := src.Meta.ProjectCwd()
	// Fork from where the source works: a worktree session's fork
	// branches off its worktree's committed HEAD.
	info, wcwd, err := r.worktrees.Create(ctx, src.Meta.Cwd, opts.Name)
	if err != nil {
		return session.SessionInfo{}, err
	}
	fork, err := r.transcripts.ForkTo(cwd, sessionID, wcwd, toTranscriptWorktree(info, origin))
	if err != nil {
		_ = worktree.Remove(ctx, info, true)
		return session.SessionInfo{}, err
	}
	return r.infoOf(fork), nil
}

func (r *AgentRuntime) infoOf(s transcript.Session) session.SessionInfo {
	msgs, _ := s.History()
	count := 0
	for _, m := range msgs {
		if m.Visible() {
			count++
		}
	}
	return session.InfoFrom(s.Meta, count, s.Meta.Title)
}

// WorktreeState is a worktree session's branch and change summary.
type WorktreeState struct {
	worktree.Status
	Branch     string `json:"branch"`
	BaseBranch string `json:"baseBranch,omitempty"`
	Into       string `json:"into"`
	Path       string `json:"path"`
	RepoRoot   string `json:"repoRoot"`
}

func (r *AgentRuntime) worktreeOf(sessionID, cwd string) (*transcript.Worktree, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Meta.Worktree == nil {
		return nil, fmt.Errorf("session %q is not in a worktree", sessionID)
	}
	return sess.Meta.Worktree, nil
}

func (r *AgentRuntime) WorktreeStatus(sessionID, cwd string) (WorktreeState, error) {
	wt, err := r.worktreeOf(sessionID, cwd)
	if err != nil {
		return WorktreeState{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	st, err := worktree.GetStatus(ctx, fromTranscriptWorktree(wt))
	if err != nil {
		return WorktreeState{}, err
	}
	into := r.GitInfo(wt.RepoRoot).Branch
	return WorktreeState{Status: st, Branch: wt.Branch, BaseBranch: wt.BaseBranch, Into: into, Path: wt.Path, RepoRoot: wt.RepoRoot}, nil
}

// WorktreeMerge commits the worktree's changes and merges its branch
// into the repository's current checkout.
func (r *AgentRuntime) WorktreeMerge(sessionID, cwd, message string) (worktree.MergeResult, error) {
	if r.hub.Busy(sessionID) {
		return worktree.MergeResult{}, fmt.Errorf("a turn is running for this session; wait for it or stop it first")
	}
	wt, err := r.worktreeOf(sessionID, cwd)
	if err != nil {
		return worktree.MergeResult{}, err
	}
	if strings.TrimSpace(message) == "" {
		if s, err := r.transcripts.Load(cwd, sessionID); err == nil {
			msgs, _ := s.History()
			message = strings.TrimSpace(s.Meta.Title)
			if message == "" {
				message = transcript.TitleFor(msgs)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	return worktree.Merge(ctx, fromTranscriptWorktree(wt), message)
}

// GitInfo describes the repository containing cwd, for the UI's
// worktree toggle.
type GitInfo struct {
	Repo       bool   `json:"repo"`
	Root       string `json:"root,omitempty"`
	Branch     string `json:"branch,omitempty"`
	HasCommits bool   `json:"hasCommits"`
}

func (r *AgentRuntime) GitInfo(cwd string) GitInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, err := worktree.RepoRoot(ctx, cwd)
	if err != nil {
		return GitInfo{}
	}
	branch, head := worktree.Head(ctx, root)
	return GitInfo{Repo: true, Root: root, Branch: branch, HasCommits: head != ""}
}

// DeleteSessionWith deletes a session; when it was the last one in its
// worktree, the worktree goes too if clean (or if discard is set).
func (r *AgentRuntime) DeleteSessionWith(sessionID, cwd string, discard bool) (bool, error) {
	var wt *transcript.Worktree
	if sess, err := r.transcripts.Load(cwd, sessionID); err == nil {
		wt = sess.Meta.Worktree
	}
	ok, err := r.DeleteSession(sessionID, cwd)
	if err != nil || wt == nil {
		return ok, err
	}
	if r.worktreeInUse(wt.Path) {
		return ok, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if err := worktree.Remove(ctx, fromTranscriptWorktree(wt), discard); err != nil {
		log.Printf("worktree: kept %s: %v", wt.Path, err)
	}
	return ok, nil
}

// worktreeInUse reports whether any session still works in path.
func (r *AgentRuntime) worktreeInUse(path string) bool {
	infos, err := r.transcripts.List("", 0)
	if err != nil {
		return true
	}
	prefix := path + string(filepath.Separator)
	for _, info := range infos {
		if wt := info.Meta.Worktree; wt != nil && wt.Path == path {
			return true
		}
		if c := info.Meta.Cwd; c == path || strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}
