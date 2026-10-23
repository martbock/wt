package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/martbock/wt/internal/gitx"
)

// RemoveOptions tunes Remove.
type RemoveOptions struct {
	// Force removes the worktree even with uncommitted or untracked changes.
	Force bool

	// SkipFetch skips the fetch that lets Remove see branches deleted on the remote.
	SkipFetch bool

	// BranchMerged says the caller checked that the branch is the head of a merged pull request,
	// so deleting it loses nothing, whether or not its remote branch is gone.
	BranchMerged bool
}

// Removed reports what Remove did.
type Removed struct {
	Path   string
	Branch string

	// BranchDeleted is true when the branch went away with the worktree.
	BranchDeleted bool

	// BranchKept explains why a branch was left behind.
	BranchKept string
}

// ErrDirty means the worktree has changes that removing it would lose.
var ErrDirty = errors.New("worktree has uncommitted or untracked changes (use --force to discard them)")

// Remove deletes a worktree. The branch is deleted too, but only if that loses no commits: it
// has nothing beyond the base, or its remote branch is gone, which is what a merged and
// auto-deleted PR looks like. Otherwise the branch stays and the result says why.
func (m Manager) Remove(ctx context.Context, slug string, opts RemoveOptions) (Removed, error) {
	info, err := m.Find(ctx, slug)
	if err != nil {
		return Removed{}, err
	}

	if info.Dirty && !opts.Force {
		return Removed{}, fmt.Errorf("%s: %w", slug, ErrDirty)
	}

	if !opts.SkipFetch {
		// Best effort: without it a merged branch is just kept, which is the safe outcome.
		_ = m.Fetch(ctx)
	}

	args := []string{"worktree", "remove"}
	if opts.Force || info.Missing {
		args = append(args, "--force")
	}
	if _, err := gitx.Run(ctx, m.Layout.Main, append(args, info.Path)...); err != nil {
		return Removed{}, err
	}

	res := Removed{Path: info.Path, Branch: info.Branch}
	if info.Branch == "" {
		return res, nil
	}

	safe, reason := true, ""
	if !opts.BranchMerged {
		safe, reason = m.branchDisposable(ctx, info.Branch)
	}
	if !safe {
		res.BranchKept = reason
		return res, nil
	}

	if _, err := gitx.Run(ctx, m.Layout.Main, "branch", "-D", info.Branch); err != nil {
		res.BranchKept = err.Error()
		return res, nil
	}
	res.BranchDeleted = true

	return res, nil
}

func (m Manager) branchDisposable(ctx context.Context, branch string) (bool, string) {
	base := m.DefaultBase(ctx)
	if m.ahead(ctx, base, branch) == 0 {
		return true, ""
	}

	if m.goneBranches(ctx)[branch] {
		return true, ""
	}

	return false, fmt.Sprintf("it has commits that are not on %s and its remote branch still exists (or was never pushed)", base)
}
