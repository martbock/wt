package manager

import (
	"context"
	"strings"

	"github.com/martbock/wt/internal/gitx"
)

// MergeState is whether a branch's work has reached the base branch.
type MergeState string

const (
	// MergeUnknown is for worktrees without a branch.
	MergeUnknown MergeState = ""

	// MergeNone: the branch has commits that the base does not have.
	MergeNone MergeState = "unmerged"

	// MergeEmpty: nothing beyond the base. A fresh branch, or one that was merged by
	// fast-forward or merge commit; git cannot tell those apart.
	MergeEmpty MergeState = "empty"

	// MergeMerged: every commit is in the base, as the same patch (rebase merge, cherry-pick),
	// or a merged pull request says so.
	MergeMerged MergeState = "merged"

	// MergeRemoteGone: the branch was pushed and its remote branch has since been deleted, which
	// is what a merged and auto-deleted pull request looks like. A squash merge ends up here
	// unless the pull request is looked up.
	MergeRemoteGone MergeState = "remote-gone"
)

// Landed is how the worktree's work relates to the base branch once its pull request, if
// known, is taken into account. A merged pull request wins over what git alone can tell,
// unless the branch has commits made after that merge.
func (i Info) Landed(pr *PullRequest) MergeState {
	if pr != nil && pr.State == PRMerged && (i.Ahead == 0 || pr.HeadSHA == "" || i.Head == "" || pr.HeadSHA == i.Head) {
		return MergeMerged
	}

	return i.Merge
}

// Label is the short text for a MergeState.
func (s MergeState) Label() string {
	switch s {
	case MergeNone:
		return "no"
	case MergeEmpty:
		return "no commits"
	case MergeMerged:
		return "yes"
	case MergeRemoteGone:
		return "remote gone"
	default:
		return "-"
	}
}

func (m Manager) mergeState(ctx context.Context, base string, info Info, remoteGone bool) MergeState {
	switch {
	case info.Ahead == 0:
		return MergeEmpty
	case m.patchesInBase(ctx, base, info.Branch):
		return MergeMerged
	case remoteGone:
		return MergeRemoteGone
	default:
		return MergeNone
	}
}

// patchesInBase reports whether every commit of branch has an equivalent in base, which is how a
// rebase merge or a cherry-pick leaves things.
func (m Manager) patchesInBase(ctx context.Context, base, branch string) bool {
	out, err := gitx.Run(ctx, m.Layout.Main, "cherry", base, branch)
	if err != nil || out == "" {
		return false
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "-") {
			return false
		}
	}

	return true
}

// goneBranches returns the local branches whose upstream branch was deleted on the remote, as of
// the last fetch.
func (m Manager) goneBranches(ctx context.Context) map[string]bool {
	out, err := gitx.Run(ctx, m.Layout.Main, "for-each-ref", "--format=%(refname:short)%09%(upstream:track)", "refs/heads")
	if err != nil {
		return nil
	}

	gone := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if name, track, ok := strings.Cut(line, "\t"); ok && strings.Contains(track, "gone") {
			gone[name] = true
		}
	}

	return gone
}
